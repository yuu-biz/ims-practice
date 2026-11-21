#!/usr/bin/env python3
"""Compare the SIMPLIFIED: comments of the code with the guides.

Every deliberate simplification of a specification has a comment that
starts with SIMPLIFIED: in the code, and a row in the table "Where |
What is left out | Why" of a guide. The comment names the specification
and clause first (for example "SIMPLIFIED: TS 24.229 5.1.1.7."), and the
first cell of the row names the same (for example "UE, TS 24.229
5.1.1.7"). This script matches the two by the specification and the
clause, and lists:

- each comment that no row of any English guide names, in a package
  that a guide shows (it names `internal/<package>` or a file in it),
  and
- each row whose specification and clause no comment names.

It also checks that the Japanese guide has as many rows as the English
one. It exits with 1 when it lists anything.

Usage: python3 scripts/check_simplified.py
"""

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
GUIDES = ROOT / "docs" / "guide"
HEADERS = {
    "en": "| Where | What is left out | Why |",
    "ja": ("| 場所 | 省いたこと | 理由 |", "| 箇所 | 省略していること | 理由 |"),
}

# A specification: a 3GPP TS or TR, an RFC, a GSMA document, or an ITU-T
# recommendation (H.248.1 with or without "ITU-T").
SPEC = re.compile(
    r"\b(?:3GPP\s+)?(?:(?:TS|TR)\s+\d{2}\.\d{3}|RFC\s*\d{3,4}|(?:GSMA\s+)?(?:IR|NG)\.\d+|(?:ITU-T\s+)?[A-Z]\.\d{3}(?:\.\d+)?)"
)
# A clause: 5.1.1.7, B.2.2, Annex E, Appendix A, or a section of an RFC such as 19.
CLAUSE = re.compile(r"\b((?:Annex|Appendix)\s+[A-Z](?:\.\d+)*|[A-Z]\.\d+(?:\.\d+)*|\d+(?:\.\d+)*[A-Z]?)\b")


def spec_name(token):
    token = re.sub(r"^(?:3GPP|GSMA|ITU-T)\s+", "", token)
    return re.sub(r"\s+", " ", token.replace("RFC", "RFC ")).replace("RFC  ", "RFC ")


def references(text):
    """The (specification, clause) pairs that text names: every clause
    between a specification and the next one. A specification with no
    clause gives (specification, "")."""
    refs = set()
    specs = list(SPEC.finditer(text))
    for k, m in enumerate(specs):
        end = specs[k + 1].start() if k + 1 < len(specs) else len(text)
        clauses = [c for c in CLAUSE.findall(text[m.end():end]) if not re.fullmatch(r"\d{4}", c)]
        name = spec_name(m.group(0))
        refs |= {(name, c) for c in clauses} or {(name, "")}
    return refs


def covers(a, b):
    """Whether two references name the same text: the same
    specification, and one clause the same as or inside the other."""
    (sa, ca), (sb, cb) = a, b
    if sa != sb:
        return False
    if not ca or not cb:
        return not ca and not cb
    return ca == cb or ca.startswith(cb + ".") or cb.startswith(ca + ".")


def any_covers(refs, others):
    return any(covers(a, b) for a in refs for b in others)


def comments():
    """Each SIMPLIFIED: comment of the Go code: its file, line and the
    references of its first sentence."""
    found = []
    for path in sorted(ROOT.rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        lines = path.read_text(encoding="utf-8").splitlines()
        for i, line in enumerate(lines):
            at = line.find("SIMPLIFIED:")
            if at < 0 or "//" not in line[:at]:
                continue
            text = line[at + len("SIMPLIFIED:"):]
            for more in lines[i + 1:i + 4]:
                stripped = more.strip()
                if not stripped.startswith("//"):
                    break
                text += " " + stripped.lstrip("/ ")
            first = re.split(r"\.\s+(?=[A-Z])", text.strip(), maxsplit=1)[0]
            found.append((path.relative_to(ROOT).as_posix(), i + 1, first, references(first)))
    return found


def table_rows(path, headers):
    """The first cells of the rows of the simplification tables of a
    guide."""
    if isinstance(headers, str):
        headers = (headers,)
    rows, inside = [], False
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.strip() in headers:
            inside = True
            continue
        if inside:
            if not line.startswith("|"):
                inside = False
                continue
            cells = [c.strip() for c in line.strip().strip("|").split("|")]
            if set(cells[0]) <= {"-", " "}:
                continue
            rows.append(cells[0])
    return rows


# Packages that no guide covers keep their table in a README beside them.
BESIDE_THE_CODE = [
    ("deploy/observability/README.md", "deploy/observability/README.ja.md"),
    ("internal/h248/README.md", "internal/h248/README.ja.md"),
    ("internal/nas/README.md", "internal/nas/README.ja.md"),
    ("internal/sgd/README.md", "internal/sgd/README.ja.md"),
]


def documents():
    """The pairs of documents, English and Japanese, that hold a table of
    simplifications."""
    pairs = [(en, GUIDES / "ja" / en.name) for en in sorted((GUIDES / "en").glob("*.md"))]
    pairs += [(ROOT / en, ROOT / ja) for en, ja in BESIDE_THE_CODE]
    return pairs


def main():
    problems = 0
    rows = {}
    for en_path, ja_path in documents():
        name = en_path.relative_to(ROOT).as_posix()
        en = table_rows(en_path, HEADERS["en"])
        ja = table_rows(ja_path, HEADERS["ja"])
        if len(en) != len(ja):
            print(f"{name}: {len(en)} rows in English, {len(ja)} in Japanese")
            problems += 1
        for cell in en:
            rows[(name, cell)] = references(cell)

    named = set()
    for refs in rows.values():
        named |= refs
    # The packages that a guide shows its reader.
    shown = set()
    for en_path, _ in documents():
        shown |= set(re.findall(r"\b((?:internal|cmd)/[a-z0-9]+)", en_path.read_text(encoding="utf-8")))
    code = comments()
    in_code = set()
    for file, line, first, refs in code:
        in_code |= refs
        if "/".join(file.split("/")[:2]) not in shown:
            continue
        if refs:
            covered = any_covers(refs, named)
        else:
            # A comment whose source is not a specification: a row
            # without one names it, by the last part of its first cell.
            covered = any(not r and cell.split(", ")[-1].lower() in first.lower() for (_, cell), r in rows.items())
        if not covered:
            print(f"{file}:{line}: no guide row names {first!r}")
            problems += 1
    texts = [first.lower() for _, _, first, _ in code]
    for (guide, cell), refs in rows.items():
        if refs:
            found = any_covers(refs, in_code)
        else:
            # A source that is not a specification, such as a document of
            # the HEP repository: the last part of the cell is in a
            # comment.
            found = any(cell.split(", ")[-1].lower() in t for t in texts)
        if not found:
            print(f"{guide}: no SIMPLIFIED: comment names {cell!r}")
            problems += 1
    print(f"{problems} mismatches between {len(code)} comments and {len(rows)} guide rows")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
