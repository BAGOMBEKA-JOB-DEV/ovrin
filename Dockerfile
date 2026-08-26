# syntax=docker/dockerfile:1

# The toolchain this project is built and tested with, pinned.
#
# The point is not deployment — ovrin is a library and there is no server to
# run. The point is that "it passes on my machine" stops depending on the
# machine: the Go version, the linter version, and the one system package that
# decides whether six of the tests run or quietly skip.
#
#   docker build --target ci -t ovrin:ci .
#   docker run --rm ovrin:ci
#
# or through the Makefile, which is where every command in this repository is
# defined:  make docker-ci

# CI pins 1.27 (.github/workflows/ci.yml). The go.mod files declare a floor of
# 1.22, which is a *language* floor and not a claim that 1.22.0 is safe to run
# this on — see SECURITY.md, "Which Go toolchain you need".
ARG GO_VERSION=1.27

# ---------------------------------------------------------------------------
FROM golang:${GO_VERSION}-bookworm AS toolchain

# Pinned to the versions .github/workflows/ci.yml installs. A different
# golangci-lint reports different findings, which would make a green run here
# mean nothing about a run there.
ARG GOLANGCI_LINT_VERSION=v2.12.2
ARG GOVULNCHECK_VERSION=v1.1.4

# go.work is local to each contributor, gitignored, and reaches a checkout
# outside this repository. Off, so modules resolve exactly as they will once
# published — the same reason CI sets it.
ENV GOWORK=off

# A go.mod `toolchain` directive can make the go command download a different
# toolchain mid-build. ocr/tesseract/go.mod has one (go1.22.2). `local` turns
# that into a loud failure instead of a silent fetch of something other than
# the version this image claims to pin.
ENV GOTOOLCHAIN=local

# make        — every command lives in the Makefile
# git         — the tidy check runs `git diff`, and Go reads VCS info
# python3     — scripts/check-docs.py (stdlib only; needs >= 3.10)
# tesseract-ocr-eng
#             — this is the interesting one. Without a language pack,
#               ocr/tesseract skips six engine-backed tests with a message
#               nobody reads, and the suite is green without having run them.
#               The package installs eng.traineddata to
#               /usr/share/tesseract-ocr/5/tessdata, which is already in
#               DefaultTessdataDirs (ocr/tesseract/tesseract.go), so the tests
#               find it with no configuration at all. Note this is the
#               *language data* only — no libtesseract, no cgo: the engine
#               itself is WebAssembly inside a Go module.
RUN apt-get update && apt-get install -y --no-install-recommends \
        make \
        git \
        python3 \
        tesseract-ocr-eng \
    && rm -rf /var/lib/apt/lists/*

RUN go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION} \
 && go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}

WORKDIR /src

# The whole tree, in one COPY.
#
# The usual `COPY go.mod go.sum ./` dependency layer is not available here,
# for two independent reasons:
#
#   1. Every non-root go.mod carries `replace github.com/…/ovrin => ../..`
#      (otel uses `..`). Copying one module's go.mod alone cannot resolve
#      that — the replace target has to be present.
#   2. Four of the nine modules have no go.sum at all, because they have no
#      external dependencies. `COPY go.sum` would fail on them.
#
# Caching is done with BuildKit cache mounts below instead, which is better
# anyway: it survives source edits rather than being invalidated by them.
COPY . .

# Warm the module and build caches. Failures are tolerated: four modules have
# nothing to download, and a network hiccup here should not fail the image
# when the commands that matter will report it properly.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    for m in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \;); do \
        (cd "$m" && go mod download) || true; \
    done

# ---------------------------------------------------------------------------
# An interactive shell with the full toolchain. Mount the repo over /src:
#     docker run --rm -it -v "$PWD:/src" ovrin:dev
FROM toolchain AS dev
CMD ["bash"]

# ---------------------------------------------------------------------------
# The whole gate. This is what `make docker-ci` runs.
#
# Not distroless or scratch, deliberately: otel/names_test.go shells out to
# `go list`, and internal/detect's tests need /dev/null and /dev/zero as
# character devices. A *release* binary from this repository could be scratch
# — there is no cgo anywhere — but a test image cannot be.
FROM toolchain AS ci
CMD ["make", "ci"]

# ---------------------------------------------------------------------------
# The example, against a real provider.
#     docker run --rm -e OPENAI_API_KEY ovrin:example
FROM toolchain AS example
CMD ["make", "run-example"]

# ---------------------------------------------------------------------------
# The evaluation harness. Needs credentials and costs money; mount
# eval/report out if you want to keep the result.
FROM toolchain AS eval
CMD ["make", "eval"]
