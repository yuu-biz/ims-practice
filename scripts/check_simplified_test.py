#!/usr/bin/env python3
"""Tests of the check that matches SIMPLIFIED: comments with table rows.

    python3 -m unittest discover -s scripts -p "*_test.py"
"""

import contextlib
import io
import tempfile
import unittest
from pathlib import Path

import check_simplified

TABLE = """# {title}

| Where | What is left out | Why |
| --- | --- | --- |
{rows}
"""
TABLE_JA = """# {title}

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
{rows}
"""


class ReadsTheClauses(unittest.TestCase):
    def test_the_number_of_an_item_is_not_a_clause(self):
        self.assertEqual(
            check_simplified.references("TS 23.040 6.2 item 3, 9.2.3.6 and 9.2.3.25"),
            {("TS 23.040", "6.2"), ("TS 23.040", "9.2.3.6"), ("TS 23.040", "9.2.3.25")},
        )

    def test_an_item_does_not_match_a_clause_of_its_number(self):
        comment = check_simplified.references("TS 23.040 6.2 item 3")
        row = check_simplified.references("SC, TS 23.040 3.2.7")
        self.assertFalse(check_simplified.any_covers(comment, row))

    def test_operations_steps_and_notes(self):
        for text, clause in [
            ("TS 23.040 10.1 operation 7", "10.1"),
            ("TS 23.204 6.5a steps 17 and 18", "6.5a"),
            ("TS 23.204 6.4 steps 3a to 3c", "6.4"),
            ("TS 24.229 5.4.3.3 step 10 a", "5.4.3.3"),
            ("TS 29.213 Table 6.2.2 NOTE 3", "6.2.2"),
            ("TS 23.204 6.4 の手順 2 から 4", "6.4"),
            ("TS 24.229 5.2.2.1 の 6 番目の項目", "5.2.2.1"),
            ("TS 23.040 3.2.6 の項目 1a と 1b", "3.2.6"),
        ]:
            with self.subTest(text=text):
                self.assertEqual(check_simplified.references(text), {(text.split()[0] + " " + text.split()[1], clause)})

    def test_a_step_before_the_next_clause(self):
        self.assertEqual(
            check_simplified.references("TS 23.204 6.5b step 2d and 6.6 step 3"),
            {("TS 23.204", "6.5b"), ("TS 23.204", "6.6")},
        )

    def test_a_step_before_a_clause_of_several_digits(self):
        self.assertEqual(
            check_simplified.references("RFC 3261 16.6 step 11, 16.8"),
            {("RFC 3261", "16.6"), ("RFC 3261", "16.8")},
        )

    def test_the_number_of_a_table_keeps_its_part(self):
        self.assertEqual(check_simplified.references("TS 29.513 Table 7.3.3-1"), {("TS 29.513", "7.3.3-1")})
        self.assertEqual(check_simplified.references("TS 29.338 Table 5.3.3.8/1"), {("TS 29.338", "5.3.3.8/1")})


class ChecksTheTree(unittest.TestCase):
    """main() over a small tree of code and tables."""

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        for name, value in [
            ("ROOT", self.root),
            ("GUIDES", self.root / "docs" / "guide"),
            ("BESIDE_THE_CODE", []),
        ]:
            self.addCleanup(setattr, check_simplified, name, getattr(check_simplified, name))
            setattr(check_simplified, name, value)
        self.addCleanup(setattr, check_simplified, "code_files", check_simplified.code_files)
        check_simplified.code_files = lambda: sorted(
            p for p in self.root.rglob("*") if p.is_file() and not check_simplified.NOT_CODE.search(p.relative_to(self.root).as_posix())
        )
        self.guide("sms.md", "| SC, TS 23.040 3.2.7 | The alert. | One number. |", "| SC、TS 23.040 3.2.7 | 知らせ。 | 番号は 1 つ。 |")

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def guide(self, name, en, ja):
        self.write(f"docs/guide/en/{name}", TABLE.format(title="Guide", rows=en))
        self.write(f"docs/guide/ja/{name}", TABLE_JA.format(title="手順書", rows=ja))

    def run_main(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            status = check_simplified.main()
        return status, out.getvalue()

    def test_a_tree_that_matches(self):
        self.write("internal/smsc/alert.go", "// SIMPLIFIED: TS 23.040 3.2.7. The SC finds the queue by MSISDN.\nfunc f() {}\n")
        self.assertEqual(self.run_main()[0], 0)

    def test_a_package_that_no_guide_names(self):
        self.write("internal/smsc/alert.go", "// SIMPLIFIED: TS 23.040 3.2.7. The SC finds the queue by MSISDN.\n")
        self.write("internal/fivegc/qos.go", "// SIMPLIFIED: TS 24.501 6.3.2.2. No command.\n")
        status, out = self.run_main()
        self.assertEqual(status, 1)
        self.assertIn("internal/fivegc/qos.go:1: no table row names 'TS 24.501 6.3.2.2'", out)

    def test_an_item_number_that_a_row_of_another_clause_matched(self):
        self.write("internal/smsc/alert.go", "// SIMPLIFIED: TS 23.040 3.2.7. The SC finds the queue by MSISDN.\n")
        self.write("internal/smsc/submit.go", "// SIMPLIFIED: TS 23.040 6.2 item 3. No TP-RD.\n")
        status, out = self.run_main()
        self.assertEqual(status, 1)
        self.assertIn("internal/smsc/submit.go:1: no table row names 'TS 23.040 6.2 item 3'", out)

    def test_a_comment_outside_the_go_code(self):
        self.write("internal/smsc/alert.go", "// SIMPLIFIED: TS 23.040 3.2.7. The SC finds the queue by MSISDN.\n")
        self.write(
            "deploy/cloud/aws/load-balancer.tf",
            '# SIMPLIFIED: AWS, "Health checks for Network Load Balancer target\n# groups". The check shows the P-CSCF.\nresource "x" "y" {}\n',
        )
        status, out = self.run_main()
        self.assertEqual(status, 1)
        self.assertIn("deploy/cloud/aws/load-balancer.tf:1: no table row names", out)
        self.assertIn("target groups", out)

    def test_markdown_and_tests_are_not_code(self):
        self.write("internal/smsc/alert.go", "// SIMPLIFIED: TS 23.040 3.2.7. The SC finds the queue by MSISDN.\n")
        self.write("docs/notes.md", "# SIMPLIFIED: TS 24.501 6.3.2.2. A heading.\n")
        self.write("internal/smsc/alert_test.go", "// SIMPLIFIED: TS 24.501 6.3.2.2. A test.\n")
        self.assertEqual(self.run_main()[0], 0)

    def test_a_japanese_row_of_other_clauses(self):
        self.write("internal/smsc/alert.go", "// SIMPLIFIED: TS 23.040 3.2.7. The SC finds the queue by MSISDN.\n")
        self.guide("sms.md", "| SC, TS 23.040 3.2.7 | The alert. | One number. |", "| SC、TS 23.040 3.2.6 | 知らせ。 | 番号は 1 つ。 |")
        status, out = self.run_main()
        self.assertEqual(status, 1)
        self.assertIn("names other clauses than 'SC, TS 23.040 3.2.7'", out)


if __name__ == "__main__":
    unittest.main()
