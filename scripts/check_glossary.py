#!/usr/bin/env python3
"""Check that every abbreviation in the guides is in the glossary.

A learner who meets an abbreviation in a guide or a README looks it up
in docs/glossary.md. This script takes each abbreviation from those
files, outside code spans, code blocks and link targets, and lists each
one that the first column of no glossary row names. A row names each
term between the slashes of its first column: the row "UA / UAC / UAS"
names UA, UAC and UAS, and the row "EPS bearer" names "EPS bearer" but
not EPS on its own.

An abbreviation is a word with two or more capital letters (S-CSCF,
GTP-U, MMTel, iFC), with a capital letter, a digit and a small letter
in that order (S6c), or an ITU-T recommendation (H.248, G.711). These
words are not abbreviations:

- a name with an underscore, which is a value of an enumeration
  (DIAMETER_SUCCESS, TERMINATING_UNREGISTERED);
- a name made of capitalized words, which is a header, an AVP or an XML
  element (Request-URI, Server-Name, SIP-Authorization, ServiceInfo);
- a file name or a document number (HEP3-extended-packet-length.md,
  IR.92, JJ-90.30), which is not a word but a part of one;
- a word next to "=", which is a parameter or a value (b=RS).

These count as the term they are made from: a plural (UEs, INVITEs), a
term with a version number (HEP3, IPv4), a term followed by the letter
of one of several nodes (IBCF-A, UE-B1), and a field of the RP or TP
layer of SMS (RP-MR, TP-DA), which the guide that shows it explains.

It exits with 1 when it lists anything.

Usage: python3 scripts/check_glossary.py
"""

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
GLOSSARY = ROOT / "docs" / "glossary.md"
FILES = ("docs/guide/**/*.md", "README*.md")

FENCE = re.compile(r"^[ \t]*(```|~~~).*?^[ \t]*\1[ \t]*$", re.DOTALL | re.MULTILINE)
CODE = re.compile(r"(`+).+?\1")
LINK_TARGET = re.compile(r"\]\([^)]*\)")
# Python counts Japanese letters as word characters, so the edges of a
# word are written out as ASCII.
EDGE_BEFORE = r"(?<![A-Za-z0-9_.'=-])"
EDGE_AFTER = r"(?![A-Za-z0-9_'=-]|\.[0-9a-z])"
WORD = re.compile(EDGE_BEFORE + r"(?:[A-Z]\.\d{3}|[A-Za-z0-9_]+(?:-[A-Za-z0-9_]+)*(?:'s|')?)" + EDGE_AFTER)
CAPITALIZED = re.compile(r"[A-Z][a-z]{2,}")
NODE_LETTER = re.compile(r"(.+)-[A-Z]\d*")
SUFFIX = re.compile(r"(.+?)(?:'s|s'?|v?\d+)")
FIELD_OF = re.compile(r"(RP|TP)-[A-Z0-9-]+")

# Words that look like abbreviations but are not terms of the system.
NOT_TERMS = {
    # SIP methods, which are English words (RFC 3261 7.1, and the RFCs
    # that add a method). The glossary has the methods that need more
    # than the word: PRACK, MESSAGE, SUBSCRIBE and NOTIFY.
    "ACK", "BYE", "CANCEL", "INFO", "INVITE", "OPTIONS", "PUBLISH",
    "REFER", "REGISTER", "UPDATE",
    # HTTP methods, which are English words too (RFC 9110 9.3).
    "DELETE", "GET", "PATCH", "POST", "PUT",
    # The reason phrase of 200.
    "OK",
    # A note of a clause, as in "5.10.2.2 NOTE 3", and a numbered
    # method of a clause, as in "method II of TS 24.229 L.2.2.1".
    "NOTE", "II",
    # The start of the header WWW-Authenticate (RFC 3261 20.44).
    "WWW",
    # Boolean operators, as in "the groups are ANDed".
    "AND", "OR", "ANDed", "ORed",
    # The licence "CC BY 4.0" and the file NOTICE that the README names.
    "CC", "BY", "NOTICE",
}


def is_abbreviation(word):
    if "_" in word:
        return False
    if re.fullmatch(r"[A-Z]\.\d{3}", word):
        return True
    if "-" not in word and (len(CAPITALIZED.findall(word)) >= 2 or re.search(r"[A-Za-z][A-Z][a-z]{3,}$", word)):
        return False
    return sum(c.isupper() for c in word) >= 2 or bool(re.search(r"[A-Z]\d+[a-z]", word))


def known(word, terms):
    word = re.sub(r"^re-", "", word)
    if word in terms or word in NOT_TERMS:
        return True
    first = word.split("-")[0]
    if "-" in word and CAPITALIZED.search(word) and (not is_abbreviation(first) or known(first, terms)):
        return True
    m = FIELD_OF.fullmatch(word)
    if m and m.group(1) in terms:
        return True
    for pattern in (NODE_LETTER, SUFFIX):
        m = pattern.fullmatch(word)
        if m and known(m.group(1), terms):
            return True
    return False


def prose_lines(path):
    """The lines of a Markdown file with code and link targets taken
    out, numbered as in the file."""
    text = FENCE.sub(lambda m: "\n" * m.group(0).count("\n"), path.read_text(encoding="utf-8"))
    for line in text.splitlines():
        yield LINK_TARGET.sub("]", CODE.sub("", line))


def glossary_terms():
    """The terms that the first column of the glossary's tables names."""
    terms = set()
    for line in GLOSSARY.read_text(encoding="utf-8").splitlines():
        if line.startswith("| ") and not line.startswith(("| Term ", "| ---")):
            terms.update(line.split("|")[1].strip().split(" / "))
    return terms


def main():
    terms = glossary_terms()
    # A term of several words, such as "PDN GW", is taken out of a line
    # before its words are looked at one by one.
    phrases = [re.compile(EDGE_BEFORE + re.escape(t) + EDGE_AFTER) for t in sorted(terms, key=len, reverse=True) if " " in t]
    found = 0
    for path in sorted(p for pattern in FILES for p in ROOT.glob(pattern)):
        for number, line in enumerate(prose_lines(path), 1):
            for phrase in phrases:
                line = phrase.sub(" ", line)
            for word in WORD.findall(line):
                if is_abbreviation(word) and not known(word, terms):
                    found += 1
                    print(f"{path.relative_to(ROOT)}:{number}: not in the glossary: {word}")
    print(f"{found} abbreviations missing from the glossary")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
