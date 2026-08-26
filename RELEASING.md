# Releasing

Mostly *why*, because the *how* is four commands and the reasoning behind them
is what prevents a bad release.

Ovrin is a multi-module repository ([ADR-0024](docs/adr/0024-versioning-and-stability.md)).
Each module versions independently and is tagged with its path prefix:

```text
v0.2.0                    the core
model/skyl/v0.1.0
ocr/tesseract/v0.1.0
render/pdfium/v0.1.0
otel/v0.1.0
```

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
git commit -s -m "chore: release v0.2.0"

# 3. Check what is about to be released
./scripts/release.sh v0.2.0

# 4. Tag and push, deliberately, by hand
git tag -s v0.2.0 -m "v0.2.0"
git push origin main v0.2.0
```

`scripts/release.sh` **never creates a tag and never pushes.** It checks and
reports: the tree is clean, the module builds at its declared floor, no
`replace` directive is present, no dependency is at a `v0.0.0` placeholder, the
changelog has a section for this version, and the tag does not already exist.
It refuses to be the thing that publishes something.

That separation is on purpose. A script that both verifies and publishes will
eventually publish on the strength of a check that was silently skipped.

## Order, when releasing several modules

Core first, then adapters. An adapter release should require a core version
that already exists in the proxy, or `go get` will fail for the first person to
try it.

After tagging the core, wait for `proxy.golang.org` to have it:

```bash
curl -s https://proxy.golang.org/github.com/!b!a!g!o!m!b!e!k!a-!j!o!b-!d!e!v/ovrin/@v/list
```

Then update the adapter's `go.mod`, commit, and tag the adapter.

## After a release

- Confirm `pkg.go.dev` has rendered the new version and the documentation looks
  right. Bad godoc is the most visible kind of bad release.
- Open a fresh `[Unreleased]` section in the changelog.
- If the release contains a security fix, publish the advisory.

## If a release is wrong

You cannot unpublish. The options, in order of preference:

1. **Fix forward.** Release the next patch immediately.
2. **Retract**, for a version that is actively harmful:

   ```go
   retract v0.2.0 // Extracted values could reach trace attributes.
   ```

   Commit the `retract` directive, tag the next patch. Retraction warns people
   on upgrade; it does not remove anything.

Never delete a tag that has been pushed. The proxy already has it, so deleting
only breaks people who fetched it before you noticed.
