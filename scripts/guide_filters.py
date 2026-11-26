#!/usr/bin/env python3
"""Print the Wireshark display filters that a guide shows its readers.

A guide that follows a scenario has a table of display filters. The
scenario's end-to-end script runs every filter of that table against
the captures it takes, so that a filter a reader is told to type finds
what the guide says it finds. This script holds the rule that picks the
filters out of the table, so that the scripts and the checks read them
the same way:

  * a table whose first header cell is "Filter" or "フィルタ",
  * every code span in the first cell of each row of that table,
  * with each "\\|" read as the "|" of a display filter, as a cell of a
    Markdown table carries it escaped.

The code spans of the second cell are left out. Many of them are a part
of a filter, such as `contains "regstate=unreg"`, which tshark cannot
read on its own.

Each filter comes with what the script expects of it:

  * some:     it finds at least one frame. This is the rule.
  * none:     it finds no frame. The second cell of its row says so, in
              English "It must find nothing." and in Japanese
              "何も見つからないのが正しい。".
  * template: tshark can read it, and nothing more. The filter holds a
              placeholder in angle brackets, such as "<Call-ID>", that
              the reader fills in.

    python3 scripts/guide_filters.py docs/guide/en/mmtel.md

prints one filter per line, after the place it comes from and what is
expected of it:

    docs/guide/en/mmtel.md:361 some sip.P-Served-User

    python3 scripts/guide_filters.py --check

compares the filters of the English guides with those of the Japanese
ones, which are the same filters in the same order with the same
expectations. It also checks that an end-to-end script runs the table
of each English guide that has one, and that the CI runs the end-to-end
scripts when only that guide changes: the "changes" job of
.github/workflows/ci.yml lets a change of documents alone skip them,
except for the guides that its pattern names.
"""

import re
import sys
from pathlib import Path

HEADERS = ("Filter", "フィルタ")
GUIDES = Path("docs/guide")
SCRIPTS = Path("scripts")
CI = Path(".github/workflows/ci.yml")
# A row of dashes and colons separates the header from the body.
RULE = set("-: ")
# The sentence of the second cell that turns the expectation around.
NOTHING = ("It must find nothing.", "何も見つからないのが正しい。")
PLACEHOLDER = re.compile(r"<[^<>]+>")
# How an end-to-end script runs the table of a guide (scripts/e2e-lib.sh).
RUNS = re.compile(r"\bguide_filters\s+(docs/guide/en/[\w.-]+\.md)\b")
# The pattern with which the CI tells a guide that a test reads from the
# other documents: the one of a grep -E that names docs/guide.
CI_PATTERN = re.compile(r"grep -E '([^']*docs/guide[^']*)'")


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
    """The filters of the file's filter table, as (line number, filter,
    expectation)."""
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
        nothing = any(sentence in " ".join(row[1:]) for sentence in NOTHING)
        for span in spans:
            filter_ = span.replace("\\|", "|")
            if PLACEHOLDER.search(filter_):
                if nothing:
                    raise ValueError(f"{path}:{number}: a filter with a placeholder cannot be expected to find nothing")
                expect = "template"
            else:
                expect = "none" if nothing else "some"
            found.append((number, filter_, expect))
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
        for number, filter_, expect in found:
            print(f"{path}:{number} {expect} {filter_}")
    return status


def run_by_a_script():
    """The English guides whose table an end-to-end script runs."""
    run = set()
    for script in sorted(SCRIPTS.glob("e2e-*.sh")):
        run.update(RUNS.findall(script.read_text(encoding="utf-8")))
    return run


def read_by_the_ci():
    """Whether a change of each guide alone still runs the tests, by the
    patterns of the CI; None when the CI names no guide."""
    patterns = [re.compile(p) for p in CI_PATTERN.findall(CI.read_text(encoding="utf-8"))] if CI.exists() else []
    if not patterns:
        return None
    return lambda guide: any(p.search(guide) for p in patterns)


def check_translations():
    """The English and the Japanese guide hold the same filters, and a
    script runs the table of each English guide."""
    status = 0
    tables = 0
    run = run_by_a_script()
    ci = read_by_the_ci()
    if ci is None:
        print(f"FAIL {CI} has no pattern of the guides that a test reads")
        status = 1
    for english in sorted((GUIDES / "en").glob("*.md")):
        japanese = GUIDES / "ja" / english.name
        mine = [(f, e) for _, f, e in filters(english)]
        theirs = [(f, e) for _, f, e in filters(japanese)] if japanese.exists() else []
        if not mine and not theirs:
            continue
        tables += 1
        # The name by which the scripts and the CI know the guide.
        name = f"docs/guide/en/{english.name}"
        if mine and name not in run:
            print(f"FAIL {english.name}: no script under {SCRIPTS} runs its filters (guide_filters {name})")
            status = 1
        if mine and ci is not None and not ci(name):
            print(f"FAIL {english.name}: the CI skips the end-to-end scripts when only this guide changes ({CI})")
            status = 1
        if mine == theirs:
            print(f"ok   {english.name}: {len(mine)} filters in both languages")
            continue
        print(f"FAIL {english.name}: the two languages show different filters")
        for filter_, expect in [f for f in mine if f not in theirs]:
            print(f"     only in {english}: {expect} {filter_}")
        for filter_, expect in [f for f in theirs if f not in mine]:
            print(f"     only in {japanese}: {expect} {filter_}")
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
