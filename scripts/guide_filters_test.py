#!/usr/bin/env python3
"""Tests of the rule that reads the display filters out of a guide.

    python3 -m unittest discover -s scripts -p "*_test.py"
"""

import contextlib
import io
import tempfile
import unittest
from pathlib import Path

import guide_filters

GUIDE = """# A guide

## Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `sip.Privacy` | Requests that withhold an identity. |
| `sip.Security-Client \\|\\| sip.Security-Server` | The security agreement. |
| `rtcp`, `rtpevent` | RTCP and the telephone-events. |
| `sip.P-Served-User` | The INVITEs to the AS. `sip.P-Served-User contains "orig-cdiv"` finds the diversions. |
| `ip.addr == 172.28.0.0/24` | In the capture of network B: anything of network A. It must find nothing. |
| `sip.Call-ID == "<Call-ID>"` | One dialog. |

The scenario's script checks these filters.

## Where the code is

| Package or file | What it holds |
| --- | --- |
| `internal/sip` | The messages. |
"""


class ReadsTheFilters(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)

    def guide(self, text, name="guide.md"):
        path = Path(self.dir.name) / name
        path.write_text(text, encoding="utf-8")
        return path

    def test_every_code_span_of_the_first_cell(self):
        found = guide_filters.filters(self.guide(GUIDE))
        self.assertEqual(
            [filter_ for _, filter_, _ in found],
            [
                "sip.Privacy",
                "sip.Security-Client || sip.Security-Server",
                "rtcp",
                "rtpevent",
                "sip.P-Served-User",
                "ip.addr == 172.28.0.0/24",
                'sip.Call-ID == "<Call-ID>"',
            ],
        )

    def test_what_is_expected_of_each_filter(self):
        found = {filter_: expect for _, filter_, expect in guide_filters.filters(self.guide(GUIDE))}
        self.assertEqual(found["sip.Privacy"], "some")
        self.assertEqual(found["ip.addr == 172.28.0.0/24"], "none")
        self.assertEqual(found['sip.Call-ID == "<Call-ID>"'], "template")

    def test_nothing_in_japanese(self):
        ja = "| フィルタ | 見つかるもの |\n| --- | --- |\n| `ip.addr == 172.28.0.0/24` | 網 A の中のもの。何も見つからないのが正しい。 |\n"
        self.assertEqual(guide_filters.filters(self.guide(ja)), [(3, "ip.addr == 172.28.0.0/24", "none")])

    def test_a_placeholder_that_must_find_nothing(self):
        rows = GUIDE.replace("| One dialog. |", "| One dialog. It must find nothing. |")
        with self.assertRaises(ValueError):
            guide_filters.filters(self.guide(rows))

    def test_the_printed_line(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            guide_filters.print_filters([str(self.guide(GUIDE))])
        self.assertIn(":7 some sip.Privacy\n", out.getvalue())
        self.assertIn(":11 none ip.addr == 172.28.0.0/24\n", out.getvalue())

    def test_the_line_of_each_filter(self):
        found = dict((filter_, number) for number, filter_, _ in guide_filters.filters(self.guide(GUIDE)))
        self.assertEqual(found["sip.Privacy"], 7)
        self.assertEqual(found["rtpevent"], 9)

    def test_a_table_of_other_things_is_left_out(self):
        found = [filter_ for _, filter_, _ in guide_filters.filters(self.guide(GUIDE))]
        self.assertNotIn("internal/sip", found)

    def test_a_guide_without_a_filter_table(self):
        self.assertEqual(guide_filters.filters(self.guide("# A guide\n\nNo table here.\n")), [])

    def test_a_row_without_a_filter(self):
        rows = GUIDE.replace("| `sip.Privacy` |", "| sip.Privacy |")
        with self.assertRaises(ValueError):
            guide_filters.filters(self.guide(rows))


# A CI that names the guides that changed, and runs the job of a scenario
# when its guide is among them.
CI = """jobs:
  changes:
    steps:
      - run: |
          guides=$(echo "$files" | grep -E '^docs/guide/en/(sms|mmtel)\\.md$')
  e2e-sms:
    needs: changes
    if: needs.changes.outputs.code == 'true' || contains(needs.changes.outputs.guides, ' sms ')
    steps:
      - run: scripts/e2e-sms.sh
"""


class ChecksTheGuides(unittest.TestCase):
    """--check over a small tree of guides, scripts and a CI."""

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        for name in ("GUIDES", "SCRIPTS", "CI"):
            self.addCleanup(setattr, guide_filters, name, getattr(guide_filters, name))
        guide_filters.GUIDES = self.root / "docs/guide"
        guide_filters.SCRIPTS = self.root / "scripts"
        guide_filters.CI = self.root / "ci.yml"
        table = "| `sip` | SIP. |\n"
        self.write("docs/guide/en/sms.md", "| Filter | What |\n| --- | --- |\n" + table)
        self.write("docs/guide/ja/sms.md", "| フィルタ | 見つかるもの |\n| --- | --- |\n" + table)
        self.write("scripts/e2e-sms.sh", "guide_filters docs/guide/en/sms.md\n")
        self.write("ci.yml", CI)

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def check(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            status = guide_filters.check_translations()
        return status, out.getvalue()

    def test_a_guide_that_a_script_runs(self):
        self.assertEqual(self.check()[0], 0)

    def test_a_guide_that_no_script_runs(self):
        self.write("scripts/e2e-sms.sh", "guide_filters docs/guide/en/mmtel.md\n")
        status, out = self.check()
        self.assertEqual(status, 1)
        self.assertIn("no script under", out)

    def test_a_guide_whose_change_the_ci_skips(self):
        self.write("ci.yml", CI.replace("(sms|mmtel)", "(mmtel)"))
        status, out = self.check()
        self.assertEqual(status, 1)
        self.assertIn("the CI skips the end-to-end scripts when only this guide changes", out)

    def test_a_job_that_does_not_run_when_its_guide_changes(self):
        self.write("ci.yml", CI.replace(" || contains(needs.changes.outputs.guides, ' sms ')", ""))
        status, out = self.check()
        self.assertEqual(status, 1)
        self.assertIn("the CI job e2e-sms runs a script that reads this guide", out)

    def test_an_expectation_that_differs_between_the_languages(self):
        self.write("docs/guide/ja/sms.md", "| フィルタ | 見つかるもの |\n| --- | --- |\n| `sip` | SIP。何も見つからないのが正しい。 |\n")
        status, out = self.check()
        self.assertEqual(status, 1)
        self.assertIn("only in", out)


if __name__ == "__main__":
    unittest.main()
