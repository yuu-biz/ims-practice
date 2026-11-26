#!/usr/bin/env python3
"""Tests of the check that every abbreviation is in the glossary.

    python3 -m unittest discover -s scripts -p "*_test.py"
"""

import contextlib
import io
import tempfile
import unittest
from pathlib import Path

import check_glossary

GLOSSARY = """# Glossary

| Term | Full name | English | 日本語 |
| --- | --- | --- | --- |
| UE | User Equipment | The phone. | 端末。 |
| DNS | Domain Name System | Names. | 名前。 |
| IP | Internet Protocol | Packets. | パケット。 |
| MSISDN | MSISDN | The number. | 番号。 |
| PCMU | G.711 mu-law | A codec. | コーデック。 |
| VoLTE | Voice over LTE | Calls. | 通話。 |
"""


class ReadsEveryLearnerDocument(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        for name, value in [("ROOT", self.root), ("GLOSSARY", self.root / "docs" / "glossary.md")]:
            self.addCleanup(setattr, check_glossary, name, getattr(check_glossary, name))
            setattr(check_glossary, name, value)
        self.write("docs/glossary.md", GLOSSARY)

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def missing(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            check_glossary.main()
        return [line.rsplit(": ", 1)[1] for line in out.getvalue().splitlines() if "not in the glossary" in line]

    def test_a_slide_deck(self):
        self.write("slides/decks/en/09-volte.md", "# VoLTE\n\nThe UE gets 5QI 1.\n")
        self.write("slides/decks/ja/09-volte.md", "# VoLTE\n\nUE は 5QI 1 を得ます。\n")
        self.assertEqual(self.missing(), ["5QI", "5QI"])

    def test_the_readmes_beside_the_code(self):
        self.write("internal/sgd/README.md", "The UserIdentifier has no LMSI.\n")
        self.write("deploy/cloud/README.md", "The cluster runs on EKS.\n")
        self.write("slides/README.md", "The decks are built as HTML.\n")
        self.assertEqual(sorted(self.missing()), ["EKS", "HTML", "LMSI"])

    def test_names_made_of_a_word_and_a_term(self):
        self.write("deploy/k8s/README.md", "CoreDNS answers, a ClusterIP Service, a PCMU-only UE, the C-MSISDN, non-UE.\n")
        self.assertEqual(self.missing(), [])

    def test_a_hyphenated_word_with_an_unknown_part(self):
        self.write("internal/sgd/README.md", "The SMSMI-Correlation-ID and the PDU-ID.\n")
        self.assertEqual(self.missing(), ["SMSMI-Correlation-ID", "PDU-ID"])


if __name__ == "__main__":
    unittest.main()
