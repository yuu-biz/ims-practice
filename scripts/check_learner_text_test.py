#!/usr/bin/env python3
"""Tests of the check for the developers' private names in published text.

    python3 -m unittest discover -s scripts -p "*_test.py"
"""

import unittest

import check_learner_text


def found(text, path="internal/ue/headers.go"):
    return [what for _, what, _ in check_learner_text.findings(text, check_learner_text.patterns_for(path))]


class RefusesTheNotes(unittest.TestCase):
    def test_a_name_of_the_roadmap(self):
        self.assertEqual(
            found("# The roadmap's completion condition: the callee's RTP payload\n", "scripts/e2e-call.sh"),
            ["name of a development record"],
        )

    def test_a_document_that_was_not_read(self):
        for text in [
            "// RCC.07 C.4.1 says, which this project has not read. It is a plain\n",
            "// the rules of GSMA RCC.07 C.4.1 that it refers to were not read.\n",
            "| UE, IR.92 2.6 | The User-Agent follows IR.92. | RCC.07 was not read. |\n",
            "| UE、IR.92 2.6 | User-Agent は IR.92 に従う。 | RCC.07 は読んでいない。 |\n",
        ]:
            with self.subTest(text=text):
                self.assertEqual(found(text), ["note that a document was not read"])

    def test_a_note_wrapped_onto_the_next_line(self):
        self.assertEqual(
            found("// big-endian; clause to\n// be confirmed for UCS2). Handsets send\n", "internal/sms/text.go"),
            ["note that a clause is unverified"],
        )
        self.assertEqual(
            found("// 502 carries no clause: the\n// clause of H.248.8 that defines it has not been read.\n"),
            ["note that a document was not read"],
        )

    def test_an_unverified_value(self):
        self.assertEqual(found("and its values are unverified (see the last section).\n", "docs/guide/en/interconnect.md"),
                         ["note that a clause is unverified"])
        self.assertEqual(found('ref.clause === "" ? "(clause unverified)" : ref.clause\n', "web/lab/src/ladder-svg.ts"),
                         ["note that a clause is unverified"])

    def test_the_records_may_say_what_was_not_read(self):
        self.assertEqual(found("| x | y | RCC.07 | 本文は読んでいない。 |\n", "docs/unverified-clauses.md"), [])
        self.assertEqual(found("The roadmap lists what remains.\n", "docs/roadmap.md"), [])


class PassesTheProtocol(unittest.TestCase):
    def test_what_a_node_has_not_read(self):
        for text in [
            'log.Warn("dropped response the TU has not read", "status", code)\n',
            'log.Warn("the MSISDN of the user was not read: the store failed")\n',
            "// call when this is the dialog the call goes on in. The dialog has to\n// be confirmed. headers go into the BYE.\n",
        ]:
            with self.subTest(text=text):
                self.assertEqual(found(text, "internal/sip/call.go"), [])


if __name__ == "__main__":
    unittest.main()
