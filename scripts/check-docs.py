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
        text = re.sub(r"<!-- api-check: ignore -->[^\n]*\n(?:\s*```.*?```)?", "",
                      text, flags=re.S)
        for m in re.finditer(r"\bovrin\.([A-Z]\w*)", text):
            name = m.group(1)
            referenced += 1
            if name not in known:
                fail(rel(p), f"references ovrin.{name}, which is not in api/ovrin.txt",
                     text[: m.start()].count("\n") + 1)
    notes.append(f"checked {referenced} ovrin.X references against {len(known)} API symbols")


# --------------------------------------------------------------------------
# 6. The two repository-layout trees agree
#
# docs/architecture.md and AGENTS.md each carry one, hand-maintained.
# --------------------------------------------------------------------------
def check_layout_trees():
    def go_files(path, heading_hint):
        text = open(os.path.join(ROOT, path)).read()
        blocks = re.findall(r"```text\n(.*?)```", text, re.S)
        for b in blocks:
            if heading_hint in b:
                return {m for m in re.findall(r"\b([a-z_]+\.go)\b", b)}
        return set()

    arch = go_files("docs/architecture.md", "ovrin.go")
    agents = go_files("AGENTS.md", "ovrin.go")
    if not arch or not agents:
        fail("docs/architecture.md", "could not find a layout tree to compare")
        return
    for name in sorted(arch - agents):
        fail("AGENTS.md", f"layout tree omits {name}, which docs/architecture.md lists")
    for name in sorted(agents - arch):
        fail("docs/architecture.md", f"layout tree omits {name}, which AGENTS.md lists")


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


def main() -> int:
    docs = markdown_files()
    check_fences(docs)
    check_links(docs)
    check_citations(docs)
    check_adrs()
    check_api_references(docs)
    check_layout_trees()
    check_fence_markers(docs)

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
