#!/usr/bin/env python3
"""Check that a rework of a slide deck kept what the deck had.

A deck that is rewritten, such as a slide of text turned into a scene,
moves its text around: into the steps of a scene, into the fields of an
event, into the longer explanation below the scene. This script
compares every deck under slides/decks/ with the same file at a git
revision (origin/main by default) and lists what the old deck had and
the new one no longer has anywhere:

- the target of each link;
- each piece of inline code, such as a filter, a header or a value;
- each line of each code block, such as a command;
- the text of each table cell.

A cell or a line that was reworded on purpose is listed too; a person
reads the list and decides. Headings are listed apart and do not fail
the check, since a slide split into two scenes gets new headings. A
deck that the revision does not have is skipped.

Run it from the root of the repository:

    python3 scripts/check_deck_content.py [revision]
"""

import pathlib
import re
import subprocess
import sys

DECKS = pathlib.Path("slides/decks")


def atoms(text):
    """Returns the things of a deck that a rework has to keep."""
    found = set()
    headings = set()
    fence = False
    for line in text.split("\n"):
        if line.startswith("```"):
            fence = not fence
            continue
        if fence:
            if line.strip():
                found.add(("line of code", line.strip()))
            continue
        for m in re.finditer(r"\]\(([^)\s]+)\)", line):
            found.add(("link", m.group(1)))
        for m in re.finditer(r"`([^`]+)`", line):
            found.add(("inline code", m.group(1)))
        if line.startswith("|") and not re.fullmatch(r"\|[\s:|-]+\|", line):
            for cell in re.split(r"(?<!\\)\|", line.strip().strip("|")):
                if cell.strip():
                    found.add(("table cell", cell.strip()))
        if line.startswith("#"):
            headings.add(line.lstrip("#").strip())
    return found, headings


def main():
    revision = sys.argv[1] if len(sys.argv) > 1 else "origin/main"
    missing_total = 0
    for path in sorted(DECKS.glob("*/*.md")):
        old = subprocess.run(["git", "show", f"{revision}:{path.as_posix()}"],
                             capture_output=True, text=True)
        if old.returncode != 0:
            continue
        new = path.read_text(encoding="utf-8")
        found, headings = atoms(old.stdout)
        missing = sorted(a for a in found if a[1] not in new)
        renamed = sorted(h for h in headings if h not in new)
        for kind, text in missing:
            print(f"{path}: {kind} is gone: {text}")
        for h in renamed:
            print(f"{path}: heading is gone (not a failure): {h}")
        missing_total += len(missing)
    print(f"{missing_total} things that the decks had at {revision} are gone")
    return 1 if missing_total else 0


if __name__ == "__main__":
    sys.exit(main())
