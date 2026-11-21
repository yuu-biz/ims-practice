#!/usr/bin/env python3
"""Check that published text does not use the developers' private names.

Guides, comments, configuration and scripts explain the system on their
own. Roadmap phases, decision and requirement numbers, and links to the
development records are names the developers use among themselves. Nor
do they announce what is not built: they describe what exists.

The development records are published too. They may link to each other
and to the code, but they follow the same rules otherwise, and they
say why something was chosen, not who chose it.

Usage: check_learner_text.py [path ...]   (default: every tracked file)
"""
import re
import subprocess
import sys

# The development records that are published.
RECORDS = re.compile(
    r"^(AGENTS\.md|CLAUDE\.md|CONTRIBUTING\.md|\.github/"
    r"|docs/(requirements|roadmap|unverified-clauses)\.md"
    r"|docs/design/|docs/background/)"
)
# This script is skipped: it has to spell out the words it looks for.
SKIPPED = re.compile(r"^scripts/check_learner_text\.py$")

# A link from what learners read to a development record.
LINK = ("link to a development record", re.compile(
    r"(requirements|roadmap|decisions|profiles|unverified-clauses)\.md|[a-z0-9]+-research\.md"
    r"|docs/(design|background)/|\.\./(design|background)/|AGENTS\.md"
    r"|\binterconnect\.md [0-9]"))

PATTERNS = [
    ("roadmap phase", re.compile(r"\bphase[ -]?[0-9]+\b|フェーズ ?[0-9]+", re.I)),
    ("decision, requirement or work unit number", re.compile(r"\b(D|FR|NFR|Q|P[0-9]+)-[0-9]{2}\b")),
    ("development process word", re.compile(r"\blane-[ab]\b|\b[Ss]ession [AB]\b|セッション [AB]|本線|暫定(?!応答)|\(draft\)|草稿")),
    ("note that a clause is unverified", re.compile(r"要確認|to be confirmed", re.I)),
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
        patterns = PATTERNS + [LINK]
    # The survey notes cite the dates of the documents and events they
    # survey, such as the version date of a GSMA profile.
    if path.endswith(".md") and not path.startswith("docs/background/"):
        patterns.append(DATE)
    return patterns


def main():
    found = 0
    for path in tracked(sys.argv[1:]):
        try:
            text = open(path, encoding="utf-8").read()
        except (UnicodeDecodeError, FileNotFoundError, IsADirectoryError):
            continue
        patterns = patterns_for(path)
        for number, line in enumerate(text.splitlines(), 1):
            for what, pattern in patterns:
                match = pattern.search(line)
                if match:
                    found += 1
                    print(f"{path}:{number}: {what}: {match.group(0)}")
    print(f"{found} private names in published text")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
