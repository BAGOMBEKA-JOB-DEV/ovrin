# Releasing

Mostly *why*, because the *how* is four commands and the reasoning behind them
is what prevents a bad release.

Ovrin is a multi-module repository ([ADR-0024](docs/adr/0024-versioning-and-stability.md)).
Each module versions independently and is tagged with its path prefix. All nine
modules, and the tag each one takes:

```text
v0.3.0                        the core
model/skyl/v0.1.0
ocr/tesseract/v0.1.0
ocr/google/v0.1.0
ocr/azure/v0.1.0
ocr/textract/v0.1.0
render/pdfium/v0.1.0
otel/v0.1.0
examples/receipt/v0.1.0
```

`examples/receipt` is on that list because it is a module with its own `go.mod`,
and Go does not offer a way to have a directory be a module and not be
importable. It gets a tag like everything else. What makes it awkward is that
it depends on `model/skyl`, which is why the ordering below has three tiers
rather than two.

## Why the first release was v0.3.0

There had never been a tag. The roadmap milestones through v0.3 were all
complete, so the first tag was **v0.3.0** rather than v0.1.0: version numbers
name what is in the release, and pretending otherwise would have meant shipping
three releases in an afternoon to versions nobody ever ran.

The seven adapters and `examples/receipt` started at `<path>/v0.1.0` instead,
because they version independently and it was their first release. A module's
number describes that module.

That release is done — `v0.3.0`, 2026-09-05. What follows is the procedure for
the next one, which unlike the first can be verified against a predecessor.

## Before tagging anything

**`main` must be green**, across every module, at every Go version in the
matrix. A tag is permanent in the Go module proxy: once
`proxy.golang.org` has fetched a version it is cached forever, and a retracted
version is still downloadable. There is no unpublishing. This is the reason
every step below is paranoid.

**The changelog must be written**, not generated. Entries are prose paragraphs
under a bold lead, saying what changed and what a user must do about it. A list
of commit subjects is not a changelog.

**Breaking changes must have a migration note.** Before v1 they may land in a
minor release, but never silently.

## Cutting a release

```bash
# 1. Confirm the tree is clean and main is green
git checkout main && git pull && git status

# 2. Move [Unreleased] into a dated section
$EDITOR CHANGELOG.md
git commit -s -m "chore: release v0.3.0"

# 3. Check what is about to be released
make release-check VERSION=v0.3.0

# 4. Tag and push, deliberately, by hand
git tag -s v0.3.0 -m "v0.3.0"
git push origin main v0.3.0
```

`VERSION` is the tag exactly as it will be pushed, prefix and all. For the core
that is `v0.3.0`; for a module it is the full tag:

```bash
make release-check VERSION=model/skyl/v0.1.0
```

The module being released is derived from that prefix, so there is nothing else
to pass. `MODULE=` overrides the derivation in the unlikely case that a tag and
a directory disagree.

`make release-check` **never creates a tag and never pushes.** It checks and
reports: the tree is clean, the tag does not already exist, the changelog has a
section for this version, the module being released carries no `replace`
directive, and it pins no dependency at a bare `v0.0.0` placeholder. A
pseudo-version such as `v0.0.0-20231006140011-7918f672742d` is not a
placeholder — it pins a commit exactly — and is not reported. On success it
prints the `git tag` and `git push` you would run; it does not run them.

That separation is on purpose. A script that both verifies and publishes will
eventually publish on the strength of a check that was silently skipped.

### Why the replace check is scoped to one module

Every non-root module carries

```text
replace github.com/BAGOMBEKA-JOB-DEV/ovrin => ../..
```

and must keep it until the core is tagged *and* `proxy.golang.org` has fetched
that tag, because until then the version its `go.mod` requires does not exist
and the module will not build for anyone.

An earlier version of this check refused any tree in which *any* module had a
`replace`. That made the first release impossible in both directions: the
replaces cannot come out before the core tag, and the core tag could not be cut
while they were there. The check therefore reads the module under release and
lists the others as context. Seeing seven modules reported as still carrying a
replace during the core release is the expected output, not a warning to act
on.

### The changelog heading it looks for

`CHANGELOG.md` follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
which writes the heading without a `v`:

```text
## [0.3.0] - 2026-08-26
```

`release-check` strips any module path prefix from `VERSION` and treats the
leading `v` as optional, so `v0.3.0` and `model/skyl/v0.1.0` both find the
heading they should. One changelog serves the whole repository, so a module
release looks for its own version number in that same file, and its entries say
which module they affect.

## Order, when releasing several modules

Three tiers, not two, and each tier waits for the proxy to have the tier above
it. An adapter release must require a core version that already exists in the
proxy, or `go get` fails for the first person to try it.

1. **The core.** `v0.3.0`. Nothing in the repository can be released before it.
2. **The seven adapters.** `model/skyl`, `ocr/tesseract`, `ocr/google`,
   `ocr/azure`, `ocr/textract`, `render/pdfium`, `otel`. They depend on the
   core and on nothing else in this repository, so once the core is on the
   proxy they can go in any order, or all at once.
3. **`examples/receipt`.** It requires `model/skyl` as well as the core, so it
   needs a *second* wait: `model/skyl` must be tagged and on the proxy before
   its `replace` lines can come out.

After tagging anything, wait for `proxy.golang.org` to have it before releasing
whatever depends on it:

```bash
curl -s https://proxy.golang.org/github.com/!b!a!g!o!m!b!e!k!a-!j!o!b-!d!e!v/ovrin/@v/list
curl -s https://proxy.golang.org/github.com/!b!a!g!o!m!b!e!k!a-!j!o!b-!d!e!v/ovrin/model/skyl/@v/list
```

Then, for each module in the tier below: delete its `replace` lines, set the
`require` lines to the versions that now exist, run `make check`, commit, and
run `make release-check VERSION=<its tag>` before tagging it.

Two waits means the whole sequence cannot be done in one sitting without
pausing twice. That is the cost of `examples/receipt` being a module, and it is
paid once per release.

## After a release

- Confirm `pkg.go.dev` has rendered the new version and the documentation looks
  right. Bad godoc is the most visible kind of bad release.
- Open a fresh `[Unreleased]` section in the changelog.
- If the release contains a security fix, publish the advisory.

## If a release is wrong

You cannot unpublish. The options, in order of preference:

1. **Fix forward.** Release the next patch immediately.
2. **Retract**, for a version that is actively harmful:

   ```text
   retract v0.3.0 // Extracted values could reach trace attributes.
   ```

   Commit the `retract` directive, tag the next patch. Retraction warns people
   on upgrade; it does not remove anything.

Never delete a tag that has been pushed. The proxy already has it, so deleting
only breaks people who fetched it before you noticed.
