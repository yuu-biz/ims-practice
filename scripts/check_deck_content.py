#!/usr/bin/env python3
"""Check that a rework of the decks kept what they had, wherever it went.

A deck that is rewritten moves its text around: into the steps of a
scene, into the longer explanation below it, into a chapter of the book
under docs/book/, or into a guide. A slide of exercises becomes an
explanation in a chapter. This script compares every deck that the
decks had at a fixed commit, the baseline, with what is there now, and
reads the record of where the content went, a Markdown file that the
first argument names.

From each slide of each deck at the baseline it takes what a rework has
to keep:

- the target of each link;
- each piece of inline code, such as a filter, a header or a value,
  also inside the YAML of a model or a scene;
- each line of each other code block, such as a command;
- the text of each cell of a table, but its header.

Each of them has to be in the deck as it is now, or in one of the
places where the record says the slide went. A slide whose heading is
gone needs a row in the record. Each row names its places, file and
heading or scene, and the words that have to be there; the script
checks that the file and the heading exist and that the words are in
that part of the file. A row that drops or corrects something names
what it drops, and says why.

The record says what a person found when they compared the old slide
with its new place. The script checks that the places and the words
are there; it does not prove that they mean the same.

A deck that the baseline has is never skipped: one that is gone now is
compared with nothing, and needs rows for all its slides. A baseline
that git cannot read is an error, not a pass.

Run it from the root of the repository:

    python3 scripts/check_deck_content.py <record> [baseline]

Without a baseline, the baseline is the commit that the record names.
"""

import pathlib
import re
import subprocess
import sys

DECKS = "slides/decks"
RECORD = "the record"
MODEL = "ims-model"
TREATMENTS = {"保持", "再編集", "統合", "移動", "課題から解説へ", "訂正", "削除"}
# The treatments whose words are what may go, not what has to stay.
DROPPING = {"訂正", "削除"}
YAML_BLOCKS = {"ims-model", "ims-scene"}


class Failure(Exception):
    """A problem that stops the check, such as a baseline git cannot read."""


def slides(text):
    """Cuts a deck into (heading, text) for each slide. The model block
    before the first slide is a slide of its own, named ims-model."""
    lines = text.split("\n")
    if lines and lines[0] == "---":
        end = lines.index("---", 1)
        lines = lines[end + 1:]
    out, current, fence = [], [], ""
    for line in lines:
        if not fence and line == "---":
            out.append(current)
            current = []
            continue
        m = re.match(r"^(```+|~~~+)", line)
        if m:
            fence = "" if fence and line.startswith(fence) else (fence or m.group(1))
        current.append(line)
    out.append(current)
    result = []
    for part in out:
        heading = None
        fence = False
        for line in part:
            if line.startswith("```") or line.startswith("~~~"):
                fence = not fence
                if not fence:
                    continue
                if line.strip("`~ ") == MODEL and heading is None:
                    heading = MODEL
                continue
            if not fence and line.startswith("#"):
                heading = heading or line.lstrip("#").strip()
        if heading is None and not "".join(part).strip():
            continue
        result.append((heading or "", "\n".join(part)))
    return result


def atoms(text):
    """Returns the things of a slide that a rework has to keep."""
    found = set()
    fence, info = False, ""

    def inline(line):
        for m in re.finditer(r"\]\(([^)\s]+)\)", line):
            found.add(("link", m.group(1)))
        for m in re.finditer(r"`([^`]+)`", line):
            found.add(("inline code", m.group(1)))

    lines = text.split("\n")
    for i, line in enumerate(lines):
        if line.startswith("```"):
            fence = not fence
            info = line.strip("` ") if fence else ""
            continue
        if fence:
            if info in YAML_BLOCKS:
                # The YAML of a scene is reworded with the scene; what it
                # quotes as code and links to has to stay.
                inline(line)
            elif line.strip():
                found.add(("line of code", line.strip()))
            continue
        inline(line)
        # The header of a table names its columns; a rework that turns
        # the table into text drops it.
        header = i + 1 < len(lines) and re.fullmatch(r"\|[\s:|-]+\|", lines[i + 1])
        if line.startswith("|") and not header and not re.fullmatch(r"\|[\s:|-]+\|", line):
            for cell in re.split(r"(?<!\\)\|", line.strip().strip("|")):
                if cell.strip():
                    found.add(("table cell", cell.strip()))
    return found


def headings(text):
    """The headings of a Markdown file, outside code blocks, as they read."""
    out, fence = [], False
    for line in text.split("\n"):
        if line.lstrip().startswith("```") or line.lstrip().startswith("~~~"):
            fence = not fence
            continue
        if not fence and re.match(r"^#{1,6}\s", line):
            out.append(line.lstrip("#").strip())
    return out


def anchor_of(heading):
    """The anchor that the repository host gives a heading."""
    return re.sub(r"[^\w\- ]", "", heading.lower()).replace(" ", "-")


def section(text, anchor):
    """The part of a Markdown file under the heading with that anchor, up
    to the next heading of the same level or higher; for a deck, the
    slide of the scene with that id. None if there is no such place."""
    if anchor == "":
        return text
    for _, slide in slides(text):
        if re.search(r"(?m)^id: " + re.escape(anchor) + r"$", slide):
            return slide
    lines = text.split("\n")
    seen = {}
    fence = False
    start = level = None
    for i, line in enumerate(lines):
        if line.lstrip().startswith("```") or line.lstrip().startswith("~~~"):
            fence = not fence
            continue
        m = re.match(r"^(#{1,6})\s+(.*)$", line)
        if fence or not m:
            continue
        if start is not None and len(m.group(1)) <= level:
            return "\n".join(lines[start:i])
        slug = anchor_of(m.group(2).strip())
        n = seen.get(slug, 0)
        seen[slug] = n + 1
        if n:
            slug = f"{slug}-{n}"
        if start is None and slug == anchor:
            start, level = i, len(m.group(1))
    return "\n".join(lines[start:]) if start is not None else None


def read_record(text):
    """Reads the record: its baseline, and its rows by deck, each row a
    dict with heading, treatment, places, words and why."""
    m = re.search(r"基準のコミット: `([0-9a-f]{40})`", text)
    baseline = m.group(1) if m else None
    rows, deck, problems = {}, None, []
    for number, line in enumerate(text.split("\n"), 1):
        h = re.match(r"^## ((?:ja|en)/[0-9a-z-]+\.md)$", line)
        if h:
            deck = f"{DECKS}/{h.group(1)}"
            rows.setdefault(deck, [])
            continue
        if not deck or not line.startswith("|") or re.fullmatch(r"\|[\s:|-]+\|", line):
            continue
        cells = [c.strip() for c in re.split(r"(?<!\\)\|", line.strip().strip("|"))]
        if cells[0] == "旧の見出し":
            continue
        if len(cells) != 5:
            problems.append(f"{RECORD}:{number}: a row has five cells: 旧の見出し、内容、扱い、行き先、確かめる語")
            continue
        heading, why, treatment, places, words = cells
        row = {
            "line": number,
            "heading": heading.replace("\\|", "|"),
            "why": why,
            "treatment": treatment,
            "places": re.findall(r"`([^`]+)`", places),
            "words": [w.replace("\\|", "|") for w in re.findall(r"`([^`]+)`", words)],
        }
        if treatment not in TREATMENTS:
            problems.append(f"{RECORD}:{number}: 扱い is one of {'、'.join(sorted(TREATMENTS))}, not {treatment!r}")
        if treatment not in DROPPING and not row["places"]:
            problems.append(f"{RECORD}:{number}: a row that keeps content names where it went")
        if treatment in DROPPING and not why:
            problems.append(f"{RECORD}:{number}: a row that drops or corrects content says why")
        rows[deck].append(row)
    return baseline, rows, problems


def check(old_decks, new_decks, rows, read):
    """Returns the problems. old_decks maps each deck at the baseline to
    its text, new_decks each deck now (absent when it is gone), rows the
    rows of the record by deck, and read(path) the text of a file now, or
    None."""
    problems = []
    for deck in sorted(rows):
        if deck not in old_decks:
            problems.append(f"{RECORD}: the baseline has no {deck}")
    for deck, old in sorted(old_decks.items()):
        new = new_decks.get(deck, "")
        new_headings = set(headings(new))
        deck_rows = rows.get(deck, [])
        old_slides = slides(old)
        old_headings = {h for h, _ in old_slides}
        for row in deck_rows:
            where = f"{RECORD}:{row['line']}"
            if row["heading"] not in old_headings:
                problems.append(f"{where}: {deck} had no slide {row['heading']!r} at the baseline")
            texts = []
            for place in row["places"]:
                path, _, anchor = place.partition("#")
                text = read(path)
                if text is None:
                    problems.append(f"{where}: {path} does not exist")
                    continue
                part = section(text, anchor)
                if part is None:
                    problems.append(f"{where}: {path} has no heading or scene #{anchor}")
                    continue
                texts.append(part)
            if row["treatment"] not in DROPPING:
                for word in row["words"]:
                    if texts and not any(word in t for t in texts):
                        problems.append(f"{where}: {word!r} is not where the row says the slide went")
        for heading, text in old_slides:
            here = [r for r in deck_rows if r["heading"] == heading]
            if heading not in new_headings and heading != MODEL and not here:
                problems.append(f"{deck}: the slide {heading!r} is gone, and the record does not say where it went")
            places = []
            dropped = set()
            for r in here:
                if r["treatment"] in DROPPING:
                    dropped.update(r["words"])
                for place in r["places"]:
                    path, _, _ = place.partition("#")
                    places.append(read(path) or "")
            for kind, atom in sorted(atoms(text)):
                if atom in new or atom in dropped or any(atom in p for p in places):
                    continue
                problems.append(f"{deck}: {heading or 'the slide'}: {kind} is gone: {atom}")
    return problems


def git(*args):
    out = subprocess.run(["git", *args], capture_output=True, text=True)
    if out.returncode != 0:
        raise Failure(f"git {' '.join(args)}: {out.stderr.strip()}")
    return out.stdout


def main():
    global RECORD
    if len(sys.argv) < 2:
        print(__doc__.split("Run it from the root of the repository:")[1].strip())
        return 2
    RECORD = sys.argv[1]
    record = pathlib.Path(RECORD).read_text(encoding="utf-8")
    baseline, rows, problems = read_record(record)
    if len(sys.argv) > 2:
        baseline = sys.argv[2]
    if not baseline:
        print(f"{RECORD} names no baseline: write the line 基準のコミット: `<commit>`")
        return 1
    try:
        names = git("ls-tree", "-r", "--name-only", baseline, "--", DECKS).split()
        old = {n: git("show", f"{baseline}:{n}") for n in names if n.endswith(".md")}
    except Failure as e:
        print(f"cannot read the decks at the baseline {baseline}: {e}")
        return 1
    if not old:
        print(f"the baseline {baseline} has no decks under {DECKS}")
        return 1

    def read(path):
        p = pathlib.Path(path)
        return p.read_text(encoding="utf-8") if p.is_file() else None

    new = {n: read(n) for n in old if read(n) is not None}
    problems += check(old, new, rows, read)
    for p in problems:
        print(p)
    print(f"{len(problems)} problems: {len(old)} decks at {baseline[:12]} compared with the decks now and the places that {RECORD} names")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
