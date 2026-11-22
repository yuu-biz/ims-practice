#!/usr/bin/env python3
"""Print the Wireshark display filters that a guide shows its readers.

Each guide that follows a scenario ends with a table of display filters.
The scenario's end-to-end script runs every filter of that table against
the capture it takes, so that a filter a reader is told to type always
finds something. This script holds the rule that picks the filters out
of the table, so that the scripts and the checks read them the same way:

  * a table whose first header cell is "Filter" or "フィルタ",
  * every code span in the first cell of each row of that table,
  * with each "\\|" read as the "|" of a display filter, as a cell of a
    Markdown table carries it escaped.

The code spans of the second cell are left out. Many of them are a part
of a filter, such as `contains "regstate=unreg"`, which tshark cannot
read on its own.

    python3 scripts/guide_filters.py docs/guide/en/mmtel.md

prints one filter per line, after the place it comes from:

    docs/guide/en/mmtel.md:361 sip.P-Served-User

    python3 scripts/guide_filters.py --check

compares the filters of the English guides with those of the Japanese
ones, which are the same filters in the same order.
"""

import re
import sys
from pathlib import Path

HEADERS = ("Filter", "フィルタ")
GUIDES = Path("docs/guide")
# A row of dashes and colons separates the header from the body.
RULE = set("-: ")


def cells(line):
    """The cells of a Markdown table row, or None for any other line."""
    line = line.strip()
    if not line.startswith("|"):
        return None
    line = line[1:]
    if line.endswith("|"):
        line = line[:-1]
    # A "|" inside a cell is escaped, including the one of a filter.
    return [cell.strip() for cell in re.split(r"(?<!\\)\|", line)]


def filters(path):
    """The filters of the file's filter table, as (line number, filter)."""
    found = []
    in_table = False
    for number, line in enumerate(path.read_text(encoding="utf-8").split("\n"), 1):
        row = cells(line)
        if row is None:
            in_table = False
            continue
        if row[0] in HEADERS:
            in_table = True
            continue
        if not in_table or set(row[0]) <= RULE:
            continue
        spans = re.findall(r"`([^`]+)`", row[0])
        if not spans:
            raise ValueError(f"{path}:{number}: a row of the filter table holds no filter")
        for span in spans:
            found.append((number, span.replace("\\|", "|")))
    return found


def print_filters(paths):
    status = 0
    for name in paths:
        path = Path(name)
        found = filters(path)
        if not found:
            print(f"{path}: no table of display filters", file=sys.stderr)
            status = 2
            continue
        for number, filter_ in found:
            print(f"{path}:{number} {filter_}")
    return status


def check_translations():
    """The English and the Japanese guide hold the same filters."""
    status = 0
    tables = 0
    for english in sorted((GUIDES / "en").glob("*.md")):
        japanese = GUIDES / "ja" / english.name
        if not japanese.exists():
            continue
        mine = [f for _, f in filters(english)]
        theirs = [f for _, f in filters(japanese)]
        if not mine and not theirs:
            continue
        tables += 1
        if mine == theirs:
            print(f"ok   {english.name}: {len(mine)} filters in both languages")
            continue
        print(f"FAIL {english.name}: the two languages show different filters")
        for filter_ in [f for f in mine if f not in theirs]:
            print(f"     only in {english}: {filter_}")
        for filter_ in [f for f in theirs if f not in mine]:
            print(f"     only in {japanese}: {filter_}")
        if sorted(mine) == sorted(theirs):
            print("     the same filters, in another order")
        status = 1
    if not tables:
        # The guides have such tables, so finding none means that this
        # script no longer reads them and checks nothing.
        print(f"FAIL no guide under {GUIDES} holds a table of display filters")
        return 2
    return status


def main(argv):
    if argv == ["--check"]:
        return check_translations()
    if not argv or any(arg.startswith("-") for arg in argv):
        print(__doc__, file=sys.stderr)
        return 2
    return print_filters(argv)


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except ValueError as err:
        print(err, file=sys.stderr)
        sys.exit(2)
