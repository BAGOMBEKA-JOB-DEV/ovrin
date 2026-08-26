# Every command this repository runs.
#
# This file is the single definition. CI calls these targets rather than
# restating the commands, and CONTRIBUTING.md points here rather than listing
# them a third time. Three copies of one command set is how the gate and the
# documentation of the gate drift apart, and this project has spent a lot of
# effort preventing exactly that kind of drift everywhere else.
#
# Run `make` or `make help` for the list.

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

# Modules are discovered, never listed.
#
# A hand-written list goes stale the moment somebody adds an adapter, and the
# failure is silent: the module is simply never built. This is the same
# reasoning the `discover` job in .github/workflows/ci.yml gives, and it must
# stay the same reasoning, because CI now calls these targets.
#
# Overridable, so CI's per-module matrix can reuse one target:
#     make build MODULES=ocr/azure
MODULES ?= $(shell find . -name go.mod -not -path './.git/*' \
                   -not -path '*/testdata/*' -exec dirname {} \; | sort)

# The root module owns three checks that are meaningless elsewhere: the
# zero-dependency assertion, the cgo-free cross-compile, and the coverage
# floor.
ROOT_MODULE := .

GO      ?= go
PYTHON  ?= python3
DOCKER  ?= docker

# Pinned to what CI installs. A different golangci-lint reports different
# findings, which makes `make lint` passing locally mean nothing.
GOLANGCI_LINT_VERSION ?= v2.12.2
GOVULNCHECK_VERSION   ?= v1.1.4

# docs/rules.md §3.7. Never lower this to make a red build green.
COVERAGE_FLOOR ?= 85

# Fuzzing is off by default and time-boxed when asked for: `go test -fuzz`
# runs until it finds something or is stopped, which is not a thing to put in
# a gate.
FUZZTIME ?= 60s

# Built binaries go here rather than beside their source. `go build ./...`
# inside examples/receipt drops a 9MB binary in the tree, and that binary
# reached git six times before anybody noticed.
BIN := $(CURDIR)/.bin

# go.work is local to each contributor, gitignored, and reaches a sibling
# checkout outside this repository. Resolving through it would let a module
# pass every check here and be unbuildable the day it is published — which is
# why CI sets this too.
export GOWORK = off

# A go.mod `toolchain` line can make the go command silently download a
# different toolchain mid-build. Failing loudly is better than a build that
# quietly used something other than what you installed.
export GOTOOLCHAIN = local

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Runs a command in every module. Modules are separate modules, so `./...`
# from the root does not reach them: each needs its own directory.
define each_module
	@set -e; for m in $(MODULES); do \
		printf '\033[1m==> %s\033[0m\n' "$$m"; \
		( cd "$$m" && $(1) ); \
	done
endef

.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Getting started
# ---------------------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@printf '\033[1movrin\033[0m — make targets\n\n'
	@awk 'BEGIN { FS = ":.*## " } \
		/^# --- .* ---$$/ { next } \
		/^##@ / { printf "\n\033[1m%s\033[0m\n", substr($$0, 5); next } \
		/^[a-zA-Z0-9_-]+:.*## / { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST)
	@printf '\nModules: $(words $(MODULES)) discovered.\n'

.PHONY: setup
setup: hooks tools ## Prepare a fresh clone for development
	@printf '\nReady. Run `make check` to run the gate.\n'

.PHONY: hooks
hooks: ## Enable the commit-message sign-off hook
	@git config core.hooksPath .githooks
	@echo "core.hooksPath = .githooks"

.PHONY: tools
tools: tools-lint tools-vuln ## Install golangci-lint and govulncheck at the versions CI pins

.PHONY: tools-lint
tools-lint:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Split out so CI's vuln job can install just this one and still take the
# version from here. Two copies of a pinned version is two things to forget.
.PHONY: tools-vuln
tools-vuln:
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

##@ Build and test

.PHONY: build
build: ## Build every module
	@# -o /dev/null, not a bare `go build ./...`. Both compile and link every
	@# package; the bare form additionally *writes* a binary for each main
	@# package into the working directory. That is how a 9MB
	@# examples/receipt/receipt reached git six times. Discarding the output
	@# is the same check without the litter. (Use `make run-example` to get a
	@# binary you actually want; it goes to .bin/.)
	$(call each_module,$(GO) build -o /dev/null ./...)

.PHONY: test
test: ## Run the offline test suite, with the race detector
	$(call each_module,$(GO) test -count=1 -race ./...)

.PHONY: test-sandbox
test-sandbox: ## Run the suite over real sockets against the adversarial fake
	$(call each_module,$(GO) test -count=1 -race -tags=sandbox ./...)

.PHONY: test-cover
test-cover: ## Run the suite and write cover.out (this is what CI runs)
	$(call each_module,$(GO) test -count=1 -race -tags=sandbox \
		-coverpkg=./... -covermode=atomic -coverprofile=cover.out ./...)

.PHONY: cover-floor
cover-floor: ## Assert root coverage is at or above the floor
	@test -f $(ROOT_MODULE)/cover.out || { \
		echo "no cover.out — run 'make test-cover' first"; exit 1; }
	@cd $(ROOT_MODULE) && \
		grep -vE '/internal/(adaptertest|sandbox|testutil)/' cover.out > c.out; \
		pct=$$($(GO) tool cover -func=c.out | awk '/^total:/ {print substr($$3, 1, length($$3)-1)}'); \
		echo "coverage $${pct}% (floor $(COVERAGE_FLOOR)%)"; \
		awk -v p="$$pct" -v f="$(COVERAGE_FLOOR)" \
			'BEGIN { if (p+0 < f+0) { print "coverage below floor"; exit 1 } }'

.PHONY: cover-html
cover-html: ## Open the root coverage profile in a browser
	@cd $(ROOT_MODULE) && $(GO) tool cover -html=cover.out

.PHONY: bench
bench: ## Run benchmarks (render/pdfium is the only package with any)
	@cd render/pdfium && $(GO) test -run='^$$' -bench=. -benchmem ./...

.PHONY: fuzz
fuzz: ## Fuzz every target for FUZZTIME each (default 60s)
	@set -e; for t in \
		internal/prompt:FuzzBuild \
		internal/pdf:FuzzPDF \
		internal/pdf:FuzzContent \
		internal/pdf:FuzzCMap \
		internal/img:FuzzDecode \
		internal/compare:FuzzCompare \
		internal/detect:FuzzDetect \
		internal/office:FuzzOffice \
		internal/normalise:FuzzNormalise; do \
		pkg=$${t%%:*}; fn=$${t##*:}; \
		printf '\033[1m==> %s %s\033[0m\n' "$$pkg" "$$fn"; \
		$(GO) test -run='^$$' -fuzz="^$$fn$$" -fuzztime=$(FUZZTIME) "./$$pkg"; \
	done

.PHONY: test-integration
test-integration: ## Run against real providers. Costs real money
	@echo "These tests contact real providers with real credentials."
	$(call each_module,$(GO) test -count=1 -tags=integration ./...)

.PHONY: eval
eval: ## Measure accuracy against the corpus. Needs OPENAI_API_KEY, costs money
	@test -n "$$OPENAI_API_KEY" || { \
		echo "set OPENAI_API_KEY — this suite needs credentials and costs money"; exit 1; }
	$(GO) test -count=1 -tags=eval ./eval/... -run TestCorpus

##@ Quality — each of these is one CI step

.PHONY: fmt
fmt: ## Format every module
	$(call each_module,gofmt -w .)

.PHONY: fmt-check
fmt-check: ## Fail if anything is not gofmt'd
	@set -e; for m in $(MODULES); do \
		out=$$(cd "$$m" && gofmt -l .); \
		if [ -n "$$out" ]; then \
			echo "$$out" | while read -r f; do echo "$$m/$$f is not gofmt'd"; done; \
			exit 1; \
		fi; \
	done
	@echo "gofmt clean"

.PHONY: vet
vet: ## Vet every build, tagged ones included
	$(call each_module,$(GO) vet ./... \
		&& $(GO) vet -tags=sandbox ./... \
		&& $(GO) vet -tags=integration ./... \
		&& $(GO) vet -tags=eval ./...)

.PHONY: lint
lint: ## Run golangci-lint on every module
	$(call each_module,golangci-lint run)

.PHONY: vuln
vuln: ## Run govulncheck on every module
	$(call each_module,govulncheck ./...)

.PHONY: tidy
tidy: ## Tidy every module's go.mod
	$(call each_module,$(GO) mod tidy)

.PHONY: tidy-check
tidy-check: tidy ## Fail if tidying left anything to commit
	@git diff --exit-code -- '*/go.mod' '*/go.sum' go.mod go.sum || { \
		echo "go mod tidy left a diff — commit it"; exit 1; }
	@echo "go.mod files are tidy"

.PHONY: deps-check
deps-check: ## Assert the core module has zero external dependencies (§4.1)
	@cd $(ROOT_MODULE) && \
	if $(GO) list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... \
	   | grep -v '^github.com/BAGOMBEKA-JOB-DEV/ovrin' | grep .; then \
		echo "the core module must have zero external dependencies"; exit 1; \
	fi
	@echo "core has zero external dependencies"

.PHONY: cross
cross: ## Assert the core cross-compiles with cgo disabled (§4.3)
	@cd $(ROOT_MODULE) && set -e; \
	for t in linux/arm64 darwin/arm64 windows/amd64; do \
		echo "  CGO_ENABLED=0 GOOS=$${t%/*} GOARCH=$${t#*/}"; \
		CGO_ENABLED=0 GOOS=$${t%/*} GOARCH=$${t#*/} $(GO) build ./...; \
	done

##@ Documentation and generated files

.PHONY: docs
docs: ## Check links, citations, ADR hygiene and API references
	@$(PYTHON) scripts/check-docs.py

.PHONY: api
api: ## Regenerate api/ovrin.txt from the source
	@cd $(ROOT_MODULE) && $(GO) test -run TestAPI -update .
	@echo "api/ovrin.txt regenerated"

.PHONY: report
report: ## Regenerate the committed no-run evaluation report
	@$(GO) test ./eval/ -run TestNoRunReport -update

.PHONY: corpus
corpus: ## Regenerate the synthetic evaluation corpus in place
	@$(GO) run ./eval/corpusgen

##@ Aggregates

.PHONY: check
check: fmt-check build vet test test-sandbox tidy-check lint vuln docs ## The gate to run before opening a pull request
	@printf '\n\033[32mcheck passed\033[0m\n'

.PHONY: ci
ci: check test-cover cover-floor deps-check cross ## Everything CI runs
	@printf '\n\033[32mci passed\033[0m\n'

# Checks and reports. It never tags and it never pushes.
#
# That separation is deliberate and is the reason RELEASING.md gives: a script
# that both verifies and publishes will eventually publish on the strength of
# a check that was silently skipped. Tagging stays a thing a person does by
# hand, having read this output.
#
#     make release-check VERSION=v0.2.0
.PHONY: release-check
release-check: ## Report whether this tree is fit to tag. VERSION=v0.2.0
	@test -n "$(VERSION)" || { echo "usage: make release-check VERSION=v0.2.0"; exit 1; }
	@fail=0; \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "  FAIL  the working tree is dirty"; fail=1; \
	else echo "  ok    the working tree is clean"; fi; \
	if git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null; then \
		echo "  FAIL  tag $(VERSION) already exists"; fail=1; \
	else echo "  ok    tag $(VERSION) does not exist yet"; fi; \
	if grep -q '^## \[\?$(VERSION)' CHANGELOG.md 2>/dev/null; then \
		echo "  ok    CHANGELOG.md has a section for $(VERSION)"; \
	else echo "  FAIL  CHANGELOG.md has no section for $(VERSION)"; fail=1; fi; \
	for m in $(MODULES); do \
		if grep -q '^replace ' "$$m/go.mod"; then \
			echo "  FAIL  $$m/go.mod has a replace directive"; fail=1; \
		fi; \
		if grep -qE '^[[:space:]]+[^[:space:]]+ v0\.0\.0([[:space:]]|$$)' "$$m/go.mod"; then \
			echo "  FAIL  $$m/go.mod pins a v0.0.0 placeholder"; fail=1; \
		fi; \
	done; \
	if [ $$fail -eq 0 ]; then echo "  ok    no replace directives or placeholder versions"; fi; \
	echo; \
	if [ $$fail -ne 0 ]; then \
		echo "not releasable. nothing was tagged and nothing was pushed."; exit 1; \
	fi; \
	echo "releasable. tag and push by hand:"; \
	echo "    git tag -s $(VERSION) -m \"$(VERSION)\" && git push origin main $(VERSION)"

.PHONY: clean
clean: ## Remove build and coverage output
	@rm -rf $(BIN)
	@rm -f examples/receipt/receipt
	@find . -name cover.out -o -name c.out -o -name coverage.html | xargs -r rm -f
	@echo "cleaned"

##@ Running things

.PHONY: run-example
run-example: ## Extract the example receipt with a real model. Needs OPENAI_API_KEY
	@test -n "$$OPENAI_API_KEY" || { \
		echo "set OPENAI_API_KEY to run this example"; exit 1; }
	@# Built from inside its own module because that is where its go.mod is,
	@# and run from the repository root because main.go names its fixture
	@# relative to there. Output goes to .bin so `go build ./...` never leaves
	@# a binary in the tree again.
	@mkdir -p $(BIN)
	@cd examples/receipt && $(GO) build -o $(BIN)/receipt .
	@$(BIN)/receipt

##@ Docker

.PHONY: docker-build
docker-build: ## Build the toolchain image
	$(DOCKER) build --target ci -t ovrin:ci .

.PHONY: docker-ci
docker-ci: docker-build ## Run the whole gate inside the container
	$(DOCKER) run --rm ovrin:ci

.PHONY: docker-test
docker-test: docker-build ## Run the test suites inside the container
	$(DOCKER) run --rm ovrin:ci make test test-sandbox

.PHONY: docker-test-offline
docker-test-offline: docker-build ## Prove the default suite needs no network
	$(DOCKER) run --rm --network=none ovrin:ci make test test-sandbox

.PHONY: docker-shell
docker-shell: ## Open a shell in the container with the repo mounted
	$(DOCKER) build --target dev -t ovrin:dev .
	$(DOCKER) run --rm -it -v "$(CURDIR):/src" ovrin:dev

.PHONY: docker-example
docker-example: ## Run the receipt example inside the container
	$(DOCKER) build --target example -t ovrin:example .
	$(DOCKER) run --rm -e OPENAI_API_KEY -e OVRIN_MODEL ovrin:example

.PHONY: docker-eval
docker-eval: ## Run the evaluation harness inside the container
	$(DOCKER) build --target eval -t ovrin:eval .
	$(DOCKER) run --rm -e OPENAI_API_KEY -e OPENAI_BASE_URL -e OVRIN_EVAL_MODEL \
		-v "$(CURDIR)/eval/report:/src/eval/report" ovrin:eval

.PHONY: docker-clean
docker-clean: ## Remove the images this Makefile builds
	-$(DOCKER) rmi ovrin:ci ovrin:dev ovrin:example ovrin:eval
