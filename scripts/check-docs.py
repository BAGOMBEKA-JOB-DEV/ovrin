#!/usr/bin/env python3
"""Documentation integrity checks for ovrin.

Everything here reads Markdown (and api/ovrin.txt) only, so it all works before
a line of Go exists. The checks that need to read Go source live in _test.go
files in the core module instead, and run under `go test`.

Run:  python3 scripts/check-docs.py [--quiet]

Each check is independent and reports every failure it finds rather than
stopping at the first, because fixing documentation in one pass is much cheaper
than fixing it in nine.
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
failures: list[str] = []
notes: list[str] = []


def fail(path: str, msg: str, line: int | None = None) -> None:
    where = f"{path}:{line}" if line else path
    failures.append(f"{where}: {msg}")


def markdown_files() -> list[str]:
    out = []
    for base, dirs, files in os.walk(ROOT):
        dirs[:] = [d for d in dirs if d not in {".git", "node_modules"}]
        for f in files:
            if f.endswith(".md"):
                out.append(os.path.join(base, f))
    return sorted(out)


def rel(p: str) -> str:
    return os.path.relpath(p, ROOT)


def strip_code(text: str) -> str:
    """Remove fenced and inline code. A snippet like `c.Extract[T](...)` is not
    a link, and treating it as one produces noise nobody reads."""
    text = re.sub(r"```.*?```", "", text, flags=re.S)
    return re.sub(r"`[^`\n]*`", "", text)


def slug(heading: str) -> str:
    """GitHub's heading-anchor algorithm, near enough."""
    s = heading.strip().lower()
    s = re.sub(r"<[^>]+>", "", s)                 # <sup>v0.2</sup>
    s = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", s)  # links keep their text
    s = s.replace("`", "")
    s = re.sub(r"[^\w\s-]", "", s, flags=re.UNICODE)
    return re.sub(r"\s+", "-", s.strip())


# --------------------------------------------------------------------------
# 1. Fenced code blocks balance
# --------------------------------------------------------------------------
def check_fences(docs):
    for p in docs:
        n = open(p).read().count("```")
        if n % 2:
            fail(rel(p), f"unbalanced code fences ({n})")


# --------------------------------------------------------------------------
# 2. Relative links and their anchors resolve
#
# The anchor half is the part the old inline check threw away, and every
# "Contents:" line in every document is an anchor.
# --------------------------------------------------------------------------
LINK = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")


def check_links(docs):
    anchors: dict[str, set[str]] = {}
    for p in docs:
        found, seen = set(), {}
        for h in re.findall(r"^#{1,6}\s+(.*)$", open(p).read(), re.M):
            s = slug(h)
            seen[s] = seen.get(s, 0) + 1
            found.add(s if seen[s] == 1 else f"{s}-{seen[s]-1}")
        anchors[os.path.abspath(p)] = found

    for p in docs:
        body = strip_code(open(p).read())
        for m in LINK.finditer(body):
            target = m.group(1)
            if target.startswith(("http://", "https://", "mailto:", "tel:")):
                continue
            line = body[: m.start()].count("\n") + 1
            path_part, _, frag = target.partition("#")
            if path_part:
                dest = os.path.normpath(os.path.join(os.path.dirname(p), path_part))
                if not os.path.exists(dest):
                    fail(rel(p), f"broken link: {target}", line)
                    continue
            else:
                dest = os.path.abspath(p)
            if frag and os.path.isfile(dest) and dest.endswith(".md"):
                if frag not in anchors.get(dest, set()):
                    fail(rel(p), f"broken anchor: {target}", line)


# --------------------------------------------------------------------------
# 3. Citations resolve
#
# docs/rules.md warns that renumbering a rule breaks every citation of it, and
# nothing enforced that until now. Same for ADR references.
# --------------------------------------------------------------------------
def check_citations(docs):
    rules_path = os.path.join(ROOT, "docs", "rules.md")
    rules = set(re.findall(r"^\*\*(\d+\.\d+)\s", open(rules_path).read(), re.M))
    adrs = {
        f[:4]
        for f in os.listdir(os.path.join(ROOT, "docs", "adr"))
        if re.match(r"^\d{4}-", f)
    }
    if not rules or not adrs:
        fail("scripts/check-docs.py", "found no rules or no ADRs to check against")
        return

    sources = list(docs) + [
        os.path.join(b, f)
        for b, d, fs in os.walk(ROOT)
        for f in fs
        if f.endswith(".go") and ".git" not in b
    ]
    for p in sources:
        text = open(p).read()
        for m in re.finditer(r"§(\d+\.\d+)", text):
            if m.group(1) not in rules:
                fail(rel(p), f"cites rules.md §{m.group(1)}, which does not exist",
                     text[: m.start()].count("\n") + 1)
        for m in re.finditer(r"ADR-(\d{4})", text):
            if m.group(1) not in adrs:
                fail(rel(p), f"cites ADR-{m.group(1)}, which does not exist",
                     text[: m.start()].count("\n") + 1)


# --------------------------------------------------------------------------
# 4. ADR hygiene: indexed, four sections, names a cost
# --------------------------------------------------------------------------
REQUIRED_SECTIONS = ("## Context", "## Decision", "## Consequences",
                     "## Alternatives considered")


def check_adrs():
    adr_dir = os.path.join(ROOT, "docs", "adr")
    files = sorted(f for f in os.listdir(adr_dir) if re.match(r"^\d{4}-", f))
    index = open(os.path.join(adr_dir, "README.md")).read()
    listed = set(re.findall(r"^\|\s*\[(\d{4})\]", index, re.M))

    for f in files:
        num, p = f[:4], os.path.join(adr_dir, f)
        text = open(p).read()
        if num not in listed:
            fail(f"docs/adr/{f}", "not listed in docs/adr/README.md")
        for s in REQUIRED_SECTIONS:
            if not re.search(rf"^{re.escape(s)}\s*$", text, re.M):
                fail(f"docs/adr/{f}", f"missing section {s}")
        # rules.md §9.4 — an ADR that lists no downsides has not finished thinking
        if "**Bad.**" not in text:
            fail(f"docs/adr/{f}", "lists no downsides")
        if not re.search(r"^\*\*Status:\*\* \w+ · \*\*Date:\*\* \d{4}-\d{2}-\d{2}", text, re.M):
            fail(f"docs/adr/{f}", "malformed status line")

    for num in sorted(listed - {f[:4] for f in files}):
        fail("docs/adr/README.md", f"indexes ADR-{num}, which has no file")


# --------------------------------------------------------------------------
# 5. Every ovrin.X named in the docs exists in api/ovrin.txt
#
# Content under "## Alternatives considered" is skipped: an ADR's rejected
# options are supposed to name APIs that do not exist.
# --------------------------------------------------------------------------
def check_api_references(docs):
    api_path = os.path.join(ROOT, "api", "ovrin.txt")
    if not os.path.exists(api_path):
        fail("api/ovrin.txt", "missing — it is the contract the docs are checked against")
        return
    known = set()
    for line in open(api_path):
        if not line.startswith("pkg ovrin, "):
            continue
        m = re.search(r"\b(?:const|var|func|type)\s+(\w+)", line)
        if m:
            known.add(m.group(1))

    referenced = 0
    for p in docs:
        text = open(p).read()
        # An ADR's rejected options are supposed to name APIs that do not
        # exist, so its Alternatives section is not checked.
        text = re.sub(r"^## Alternatives considered\s*$.*?(?=^## |\Z)", "",
                      text, flags=re.M | re.S)
        # Explicit escape hatch, for a migration note or a quotation of a
        # superseded API. Suppresses the rest of its own line and the fenced
        # block immediately following, if any.
        text = re.sub(
            r"<!-- api-check: ignore -->[^\n]*\n"
            r"(?:\s*```.*?```|(?:[^\n]+\n)*)",
            "", text, flags=re.S)
        for m in re.finditer(r"\bovrin\.([A-Z]\w*)", text):
            name = m.group(1)
            referenced += 1
            if name not in known:
                fail(rel(p), f"references ovrin.{name}, which is not in api/ovrin.txt",
                     text[: m.start()].count("\n") + 1)
    notes.append(f"checked {referenced} ovrin.X references against {len(known)} API symbols")


# --------------------------------------------------------------------------
# 6. The two repository-layout trees describe the repository that exists
#
# docs/architecture.md and AGENTS.md each carry one, hand-maintained. This
# check used to compare them against each other, which caught nothing worth
# catching: both drifted the same way at the same time, agreed perfectly, and
# stayed green while naming three packages that had never existed. The
# filesystem is the only authority a layout tree can usefully be checked
# against, so that is what it is checked against now.
#
# Test files are not required to appear in a tree — a tree is a map of the
# implementation, and listing every _test.go would bury it.
# --------------------------------------------------------------------------
TREE_FILE = re.compile(r"^[A-Za-z0-9_.-]+\.go$")
TREE_DIR = re.compile(r"^[a-z0-9_]+(?:/[a-z0-9_]+)*/$")
TREE_LINE = re.compile(r"^((?:[\u2502 ]{4})*)(?:[\u251c\u2514]\u2500\u2500\s*)?(.*)$")


def parse_tree(path: str, hint: str):
    """Return the repo-relative paths one fenced tree names.

    Entries and their annotations share a line, so each line is read left to
    right and stops at the first token that is not an entry: a line reading
    `model.go   ocr.go   render.go      THE SEAMS` names three files and then
    says something about them. Indentation gives the parent, so `pdf/` nested
    under `internal/` resolves to `internal/pdf/` rather than to a top-level
    directory that does not exist.
    """
    text = open(os.path.join(ROOT, path)).read()
    for m in re.finditer(r"```text\n(.*?)```", text, re.S):
        if hint not in m.group(1):
            continue
        first_line = text[: m.start(1)].count("\n") + 1
        entries, stack = [], []
        for i, line in enumerate(m.group(1).splitlines()):
            if i == 0:
                continue  # the module root itself, which is ROOT
            lm = TREE_LINE.match(line)
            depth = len(lm.group(1)) // 4
            for tok in lm.group(2).split():
                parent = stack[depth - 1] if 0 < depth <= len(stack) else ""
                if TREE_DIR.match(tok):
                    p = parent + tok
                    entries.append((p, True, first_line + i))
                    del stack[depth:]
                    stack.append(p)
                elif TREE_FILE.match(tok):
                    entries.append((parent + tok, False, first_line + i))
                else:
                    break  # the annotation starts here
        return entries
    return None


def check_layout_trees():
    trees = {}
    for path in ("docs/architecture.md", "AGENTS.md"):
        entries = parse_tree(path, "ovrin.go")
        if not entries:
            fail(path, "could not find a repository-layout tree")
            continue
        trees[path] = entries
    if len(trees) != 2:
        return

    # What every tree must account for: the root package is the public API and
    # internal/ is where the implementation lives, so a tree that omits either
    # is a map with a missing country.
    want = {f for f in os.listdir(ROOT)
            if f.endswith(".go") and not f.endswith("_test.go")}
    internal = os.path.join(ROOT, "internal")
    want |= {f"internal/{d}/" for d in os.listdir(internal)
             if os.path.isdir(os.path.join(internal, d))}

    checked = 0
    for path, entries in trees.items():
        named = set()
        for entry, is_dir, line in entries:
            named.add(entry)
            checked += 1
            dest = os.path.join(ROOT, entry)
            if not (os.path.isdir(dest) if is_dir else os.path.isfile(dest)):
                fail(rel(os.path.join(ROOT, path)),
                     f"layout tree names {entry}, which does not exist", line)
        for missing in sorted(want - named):
            fail(path, f"layout tree omits {missing}, which does exist")

    notes.append(f"checked {checked} layout-tree entries against the filesystem")


# --------------------------------------------------------------------------
# 7. Fence markers are ones we understand
# --------------------------------------------------------------------------
VALID_MARKERS = {"", "mirror", "sketch"}


def check_fence_markers(docs):
    sketches = 0
    for p in docs:
        for i, line in enumerate(open(p), 1):
            m = re.match(r"^```go(.*)$", line.rstrip("\n"))
            if not m:
                continue
            marker = m.group(1).strip()
            if marker not in VALID_MARKERS:
                fail(rel(p), f"unknown go fence marker {marker!r}; "
                             f"expected one of {sorted(VALID_MARKERS - {''})} or none", i)
            if marker == "sketch":
                sketches += 1
    notes.append(f"{sketches} go fences are marked `sketch` (exempt from checking)")


def check_make_targets():
    """Every `make X` a document names must be a real target, and every target
    must be documented in the README.

    The Makefile exists so that a command is written down once. That only holds
    while the README describing the targets and the Makefile defining them
    agree, and nothing else would notice them drifting apart: a `make deploy`
    in a document is not a broken link and not a failing build, it is simply an
    instruction that does nothing.
    """
    makefile = os.path.join(ROOT, "Makefile")
    if not os.path.exists(makefile):
        return

    declared = set()
    for line in open(makefile):
        m = re.match(r"^([a-zA-Z0-9][a-zA-Z0-9_-]*):", line)
        if m:
            declared.add(m.group(1))

    # Targets that exist as plumbing and need no entry of their own.
    internal = {"tools-lint", "tools-vuln"}

    documented = set()
    for p in markdown_files():
        text = open(p, encoding="utf-8").read()
        for target in re.findall(r"`make ([a-z][a-z0-9-]*)", text):
            documented.add(target)
            if target not in declared:
                fail(rel(p), f"`make {target}` is documented but is not a target "
                             f"in the Makefile")

    readme = os.path.join(ROOT, "README.md")
    in_readme = set(re.findall(r"`make ([a-z][a-z0-9-]*)",
                               open(readme, encoding="utf-8").read()))
    for target in sorted(declared - in_readme - internal - {"help"}):
        fail("README.md", f"`make {target}` is a target but is not documented "
                          f"in the README")

    notes.append(f"checked {len(declared)} make targets against the documentation")


def main() -> int:
    docs = markdown_files()
    check_fences(docs)
    check_links(docs)
    check_citations(docs)
    check_adrs()
    check_api_references(docs)
    check_layout_trees()
    check_fence_markers(docs)
    check_make_targets()

    quiet = "--quiet" in sys.argv
    if not quiet:
        print(f"checked {len(docs)} markdown files")
        for n in notes:
            print(f"  {n}")
    if failures:
        for f in failures:
            print(f"::error::{f}" if os.getenv("GITHUB_ACTIONS") else f"FAIL {f}")
        print(f"\n{len(failures)} documentation problem(s)")
        return 1
    if not quiet:
        print("documentation checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
