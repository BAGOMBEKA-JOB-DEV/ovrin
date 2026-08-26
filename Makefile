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
ACTIONLINT_VERSION    ?= v1.7.12

# docs/rules.md §3.7. Never lower this to make a red build green.
COVERAGE_FLOOR ?= 85

# Fuzzing is off by default and time-boxed when asked for: `go test -fuzz`
# runs until it finds something or is stopped, which is not a thing to put in
# a gate.
FUZZTIME ?= 60s

# How long Go may spend shrinking a newly interesting input before it goes back
# to fuzzing. The default is unbounded in practice, and in internal/pdf that
# swallowed almost the whole budget. See the fuzz target.
FUZZMINIMIZETIME ?= 2s

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

# `go install` puts binaries in GOPATH/bin, which is not on everyone's PATH.
# Adding it here means `make setup && make check` works on a fresh machine
# without also asking the contributor to edit a shell profile first.
export PATH := $(shell $(GO) env GOPATH)/bin:$(PATH)

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
tools: tools-lint tools-vuln tools-actions ## Install the linters and checkers CI uses, at CI's versions

# GOTOOLCHAIN=auto is set for these two, and only these two.
#
# The file-level GOTOOLCHAIN=local exists so that *building this library* never
# silently uses a toolchain other than the one you installed. Installing a
# developer tool is a different thing: golangci-lint v2.12.2 needs Go >= 1.25
# to compile, so on a machine at the 1.22 floor `local` turns "fetch a
# toolchain to build a linter" into "you cannot have a linter". The tool is not
# part of ovrin's build and nothing it produces goes into ovrin, so letting the
# go command fetch what it needs here costs the guarantee nothing.
.PHONY: tools-lint
tools-lint:
	GOTOOLCHAIN=auto $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Split out so CI's vuln job can install just this one and still take the
# version from here. Two copies of a pinned version is two things to forget.
# actionlint validates the workflow files themselves. Worth its own target
# because a workflow file that GitHub rejects fails in zero seconds with
# "this run likely failed because of a workflow file issue" and no line number
# — which is how an invalid `if:` sat in ci.yml through every push, failing
# every run, while the repository looked like it had CI.
.PHONY: tools-actions
tools-actions:
	GOTOOLCHAIN=auto $(GO) install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

.PHONY: tools-vuln
tools-vuln:
	GOTOOLCHAIN=auto $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

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
	@# What is filtered out, and why. The rule is that a package which exists
	@# to support tests inflates the number without testing anything:
	@#   internal/adaptertest  the provider contract suite
	@#   internal/sandbox      the adversarial fake server
	@#   internal/testutil     (reserved; does not exist yet)
	@#   eval/corpusgen        the generator that draws the evaluation corpus
	@# corpusgen is a maintainer tool run by `make corpus`, never by a user and
	@# never by the library. It is 4,032 statements, and counting them dragged
	@# the total from 86.6% to 82.6% — a number about the generator, not about
	@# ovrin. Note this filters what is *measured*; the floor itself has not
	@# moved and must not (docs/rules.md §3.7).
	@cd $(ROOT_MODULE) && \
		grep -vE '/internal/(adaptertest|sandbox|testutil)/|/eval/corpusgen/' cover.out > c.out; \
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
	@# Targets are discovered, not listed, for the same reason modules are: a
	@# hand-written list goes stale silently, and a fuzz target nobody runs is
	@# worse than none because it looks like coverage.
	@#
	@# -fuzzminimizetime is capped. Go minimises every newly interesting input
	@# before continuing, and in internal/pdf that minimisation is pathological
	@# — measured at roughly a hundredfold fewer executions with the default,
	@# so most of the budget went to shrinking inputs rather than finding them.
	@# A crasher minimised for two seconds is still a reproducer.
	@set -e; \
	targets=$$(grep -rho '^func Fuzz[A-Za-z0-9_]*' --include='*_test.go' . \
		| sed 's/^func //' | sort -u); \
	for fn in $$targets; do \
		for pkg in $$(grep -rl "^func $$fn(" --include='*_test.go' . | xargs -n1 dirname | sort -u); do \
			printf '\033[1m==> %s %s\033[0m\n' "$$pkg" "$$fn"; \
			$(GO) test -run='^$$' -fuzz="^$$fn$$" -fuzztime=$(FUZZTIME) \
				-fuzzminimizetime=$(FUZZMINIMIZETIME) "$$pkg"; \
		done; \
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

# `golangci-lint: not found` from inside a for-loop is a confusing way to
# learn you have not run `make setup`. Say the useful thing instead.
.PHONY: actions
actions: ## Validate the GitHub Actions workflow files
	@command -v actionlint >/dev/null || { \
		echo "actionlint is not installed — run 'make tools-actions' (or 'make setup')"; \
		exit 1; }
	@actionlint
	@echo "workflows valid"

.PHONY: lint
lint: ## Run golangci-lint on every module
	@command -v golangci-lint >/dev/null || { \
		echo "golangci-lint is not installed — run 'make tools-lint' (or 'make setup')"; \
		exit 1; }
	$(call each_module,golangci-lint run)

.PHONY: vuln
vuln: ## Run govulncheck on every module
	@command -v govulncheck >/dev/null || { \
		echo "govulncheck is not installed — run 'make tools-vuln' (or 'make setup')"; \
		exit 1; }
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
check: fmt-check build vet test test-sandbox tidy-check lint vuln docs actions ## The gate to run before opening a pull request
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
# VERSION is the tag exactly as it will be pushed. In a multi-module repository
# that means it may carry the module's path prefix:
#
#     make release-check VERSION=v0.3.0              the core
#     make release-check VERSION=model/skyl/v0.1.0   one module
#
# The module being released is derived from that prefix. MODULE= overrides the
# derivation for the rare case where the tag and the directory disagree.
#
# The scoping is the point, not a convenience. Every non-root module has to
# carry
#
#     replace github.com/BAGOMBEKA-JOB-DEV/ovrin => ../..
#
# until the core is tagged and the proxy has fetched it, because until then the
# version its go.mod requires does not exist. A check that failed on *any*
# module's replace therefore made the very first release impossible: the
# replace cannot come out before the core tag, and the core tag could not be
# cut while the replace was there. So the replace and placeholder checks read
# the module under release, and the rest are reported as context rather than as
# failures.
#
# The changelog is matched on the version number with the path prefix removed
# and the leading v optional, because CHANGELOG.md follows Keep a Changelog and
# writes `## [0.3.0] - 2026-08-26`. One changelog serves the whole repository,
# so a module release looks for its own number in that same file.
.PHONY: release-check
release-check: ## Report whether this tree is fit to tag. VERSION=v0.3.0
	@test -n "$(VERSION)" || { \
		echo "usage: make release-check VERSION=v0.3.0"; \
		echo "       make release-check VERSION=model/skyl/v0.1.0"; \
		exit 1; }
	@tag='$(VERSION)'; mod='$(MODULE)'; \
	num="$${tag##*/}"; num="$${num#v}"; \
	if [ -z "$$mod" ]; then \
		case "$$tag" in */*) mod="$${tag%/*}";; *) mod=".";; esac; \
	fi; \
	mod="$${mod#./}"; mod="$${mod%/}"; [ -n "$$mod" ] || mod="."; \
	if [ ! -f "$$mod/go.mod" ]; then \
		echo "  FAIL  $$mod is not a module — no $$mod/go.mod"; \
		echo; \
		echo "not releasable. nothing was tagged and nothing was pushed."; \
		exit 1; \
	fi; \
	echo "releasing $$mod as tag $$tag"; \
	echo; \
	fail=0; \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "  FAIL  the working tree is dirty"; fail=1; \
	else echo "  ok    the working tree is clean"; fi; \
	if git rev-parse -q --verify "refs/tags/$$tag" >/dev/null; then \
		echo "  FAIL  tag $$tag already exists"; fail=1; \
	else echo "  ok    tag $$tag does not exist yet"; fi; \
	re=$$(printf '%s' "$$num" | sed 's/\./\\./g'); \
	if grep -qE "^## \[?v?$$re\]?" CHANGELOG.md 2>/dev/null; then \
		echo "  ok    CHANGELOG.md has a section for $$num"; \
	else \
		echo "  FAIL  CHANGELOG.md has no '## [$$num]' section"; fail=1; \
	fi; \
	if grep -q '^replace ' "$$mod/go.mod"; then \
		echo "  FAIL  $$mod/go.mod still has a replace directive"; fail=1; \
	else echo "  ok    $$mod/go.mod has no replace directive"; fi; \
	if grep -qE '^[[:space:]]+[^[:space:]]+ v0\.0\.0([[:space:]]|$$)' "$$mod/go.mod"; then \
		echo "  FAIL  $$mod/go.mod pins a v0.0.0 placeholder"; fail=1; \
	else echo "  ok    $$mod/go.mod pins no v0.0.0 placeholder"; fi; \
	others=; \
	for m in $(MODULES); do \
		m="$${m#./}"; \
		[ "$$m" = "$$mod" ] && continue; \
		if grep -q '^replace ' "$$m/go.mod"; then others="$$others $$m"; fi; \
	done; \
	if [ -n "$$others" ]; then \
		echo "  note  still carrying a replace, not released here:$$others"; \
		echo "        expected until each is tagged in its turn — RELEASING.md"; \
	fi; \
	echo; \
	if [ $$fail -ne 0 ]; then \
		echo "not releasable. nothing was tagged and nothing was pushed."; exit 1; \
	fi; \
	echo "releasable. tag and push by hand:"; \
	echo "    git tag -s $$tag -m \"$$tag\" && git push origin main $$tag"

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
