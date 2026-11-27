#!/usr/bin/env python3
"""Check that published text does not use the developers' private names.

Guides, comments, configuration and scripts explain the system on their
own. Roadmap phases, decision and requirement numbers, and links to the
development records are names the developers use among themselves. Nor
do they announce what is not built: they describe what exists. They
name only the clauses that were read; a note that a document was not
read or a clause is unverified belongs in docs/unverified-clauses.md.
A phrase that a comment wraps onto the next line counts as one.

The development records are published too. They may link to each other
and to the code, but they follow the same rules otherwise, and they
say why something was chosen, not who chose it.

Usage: check_learner_text.py [path ...]   (default: every tracked file)
"""
import re
import subprocess
import sys

# The development records that are published. The project skills under
# .claude/ are instructions for the agents that develop the repository.
RECORDS = re.compile(
    r"^(AGENTS\.md|CLAUDE\.md|CONTRIBUTING\.md|\.github/|\.claude/"
    r"|docs/(requirements|roadmap|unverified-clauses)\.md"
    r"|docs/design/|docs/background/)"
)
# This script and its test are skipped: they spell out the words it
# looks for.
SKIPPED = re.compile(r"^scripts/check_learner_text(?:_test)?\.py$")

# What learners read names only the clauses that were read. A note that
# a document or clause was not read, or is unverified, belongs in
# docs/unverified-clauses.md, which records such notes. A spec, a clause
# or "this project" comes first, so that "the TU has not read the
# response" passes.
UNREAD = [
    ("note that a clause is unverified", re.compile(r"\bunverified\b|未確認", re.I)),
    ("note that a document was not read", re.compile(
        r"(?:\b(?:clauses?|rules|document|specification|RCC|IR|NG|TS|TR|RFC)\b|\bH\.\d)"
        r"(?:[^.;]|\.(?=\S)){0,80}?\b(?:has|have|was|were) not (?:been )?read\b"
        r"|\bthis project (?:has|have) not read\b|読んでいない|読んでいません|読めていない", re.I)),
]
# A development record named in what learners read.
RECORD_NAME = ("name of a development record", re.compile(r"\b[Rr]oadmap\b|ロードマップ|\bunverified-clauses\b"))
# A link from what learners read to a development record.
LINK = ("link to a development record", re.compile(
    r"(requirements|roadmap|decisions|profiles|unverified-clauses)\.md|[a-z0-9]+-research\.md"
    r"|docs/(design|background)/|\.\./(design|background)/|AGENTS\.md"
    r"|\binterconnect\.md [0-9]"))

PATTERNS = [
    ("roadmap phase", re.compile(r"\bphase[ -]?[0-9]+\b|フェーズ ?[0-9]+", re.I)),
    ("decision, requirement or work unit number", re.compile(r"\b(D|FR|NFR|Q|P[0-9]+)-[0-9]{2}\b")),
    ("development process word", re.compile(r"\blane-[ab]\b|\b[Ss]ession [AB]\b|セッション [AB]|本線|暫定(?!応答)|\(draft\)|草稿")),
    # "to be confirmed" after a noun: "clause to be confirmed", not the
    # "has to be confirmed" of a SIP dialog.
    ("note that a clause is unverified", re.compile(r"要確認|(?<!has )(?<!have )(?<!had )to be confirmed", re.I)),
    # Words that announce what is not built. "yet", "later", "for now",
    # "まだ" and "あとで" are left out: in this repository they mostly
    # describe the state of a protocol ("no answer yet", "the callee
    # later sends"), and only the reader can tell the two apart.
    ("announcement of what is not built", re.compile(
        r"\b(is|are) planned\b|\bplanned (architecture|configuration|features?|support|work)\b"
        r"|\bwill be (added|built|implemented|supported)\b|\blater phases?\b|\bupcoming\b|(?-i:\bTODO\b)"
        r"|\bnot been (tried|built|implemented)\b"
        r"|予定|今のところ|将来|(いずれ|あとで|後で|まだ)(作|実装|対応|加え|足)|まだ(して|試して|確かめて)い(ない|ません)", re.I)),
]
# A date in prose records when something was decided; in code and data it is a value.
DATE = ("date of a decision", re.compile(r"\b20[0-9]{2}-[0-9]{2}-[0-9]{2}\b"))
# The records say why, not who decided or asked.
WHO = ("who decided or asked", re.compile(r"利用者の(指示|判断)|依頼者|調整役|統括"))


def tracked(paths):
    out = subprocess.run(["git", "ls-files", "--", *paths], check=True, capture_output=True, text=True)
    return [p for p in out.stdout.splitlines() if not SKIPPED.match(p)]


def patterns_for(path):
    if RECORDS.match(path):
        patterns = PATTERNS + [WHO]
    else:
        patterns = PATTERNS + UNREAD + [LINK, RECORD_NAME]
    # The survey notes cite the dates of the documents and events they
    # survey, such as the version date of a GSMA profile.
    if path.endswith(".md") and not path.startswith("docs/background/"):
        patterns.append(DATE)
    return patterns


# The markers that start a comment line, which a sentence that goes on
# from the line before carries in front of it.
CONTINUATION = re.compile(r"^\s*(?://+|#+|\*|--|;+)?\s*")


def findings(text, patterns):
    """(line number, what, match) of each pattern in text. A phrase may
    be wrapped onto the next line, as in a comment; it counts once, on
    the line where it starts."""
    lines = text.splitlines()
    spilled = {}
    for number, line in enumerate(lines, 1):
        following = CONTINUATION.sub("", lines[number]) if number < len(lines) else ""
        joined = line + " " + following
        for what, pattern in patterns:
            match = pattern.search(joined, spilled.get(what, 0))
            spilled[what] = 0
            if match and match.start() < len(line):
                spilled[what] = max(0, match.end() - len(line) - 1)
                yield number, what, match.group(0)


def main():
    found = 0
    for path in tracked(sys.argv[1:]):
        try:
            text = open(path, encoding="utf-8").read()
        except (UnicodeDecodeError, FileNotFoundError, IsADirectoryError):
            continue
        for number, what, match in findings(text, patterns_for(path)):
            found += 1
            print(f"{path}:{number}: {what}: {match}")
    print(f"{found} private names in published text")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
