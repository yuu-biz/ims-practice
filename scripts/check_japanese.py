#!/usr/bin/env python3
"""Check two rules of the Japanese text of the repository.

- A heading names its subject in one phrase. A heading that cuts a
  subject and a detail apart with a full stop, such as
  「CAT。着信側が選ぶ呼び出し音」, reads as two sentences pressed together;
  the detail goes in parentheses: 「CAT（着信側が選ぶ呼び出し音）」.
- A sequence diagram is called シーケンス図, the name Japanese readers
  know, not ラダー図. The English text keeps "ladder diagram".

It reads every tracked Markdown file, outside its fenced code blocks;
inline code in a heading is not read. The decks under slides/decks/ hold
the headings of steps in YAML, which `go run ./cmd/slides check` reads.

Usage: check_japanese.py [path ...]   (default: every tracked .md file)
"""
import re
import subprocess
import sys

HEADING = re.compile(r"^#{1,6} (.*)$")
CODE = re.compile(r"`[^`]*`")
# A text that names the old word, in brackets as a word, may say it.
OLD_TERM = re.compile(r"(?<!「)ラダー図(?!」)|ラダー(?=を|の|に|で|が|は)")
# This script and its test spell out what they look for.
SKIPPED = re.compile(r"^scripts/check_japanese(?:_test)?\.py$")


def tracked(paths):
    if paths:
        return paths
    out = subprocess.run(["git", "ls-files", "*.md"], capture_output=True, text=True, check=True).stdout
    return [p for p in out.splitlines() if not SKIPPED.search(p)]


def findings(text):
    """The line numbers and the problems of one file."""
    found = []
    fenced = False
    for number, line in enumerate(text.splitlines(), 1):
        if line.lstrip().startswith(("```", "~~~")):
            fenced = not fenced
            continue
        if fenced:
            continue
        if OLD_TERM.search(CODE.sub("", line)):
            found.append((number, "ラダー図: say シーケンス図 in Japanese"))
        m = HEADING.match(line)
        if m and "。" in CODE.sub("", m.group(1)).rstrip("。"):
            found.append((number, f"a heading with a full stop inside: {m.group(1)}"))
    return found


def main():
    problems = 0
    for path in tracked(sys.argv[1:]):
        with open(path, encoding="utf-8") as f:
            text = f.read()
        for number, problem in findings(text):
            print(f"{path}:{number}: {problem}")
            problems += 1
    print(f"{problems} problems in the Japanese text")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
