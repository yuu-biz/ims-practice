"""Fail when a Markdown file links to a repository path that does not exist."""

import pathlib
import re
import sys

LINK = re.compile(r"\]\(([^)#\s]+)(?:#[^)]*)?\)")
# A fenced code block shows text as it is, so "](" inside one is not a
# link; a regular expression in a YAML sample would look like one.
FENCE = re.compile(r"^(```|~~~).*?^\1[ \t]*$", re.DOTALL | re.MULTILINE)
# dist/ holds what the generators write, such as the book for Zenn,
# whose links point where the book is published.
SKIP_DIRS = {".git", ".claude", "node_modules", "dist"}


def main() -> int:
    root = pathlib.Path(__file__).resolve().parent.parent
    broken = []
    for md in root.rglob("*.md"):
        if SKIP_DIRS.intersection(md.relative_to(root).parts):
            continue
        for target in LINK.findall(FENCE.sub("", md.read_text(encoding="utf-8"))):
            if re.match(r"^[a-z]+:", target):
                continue
            if not (md.parent / target).exists():
                broken.append(f"{md.relative_to(root)}: {target}")
    for line in broken:
        print(f"broken link: {line}")
    print(f"{len(broken)} broken links")
    return 1 if broken else 0


if __name__ == "__main__":
    sys.exit(main())
