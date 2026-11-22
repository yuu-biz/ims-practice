#!/usr/bin/env python3
"""Tests of the rule that reads the display filters out of a guide.

    python3 -m unittest discover -s scripts -p "*_test.py"
"""

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
            [filter_ for _, filter_ in found],
            [
                "sip.Privacy",
                "sip.Security-Client || sip.Security-Server",
                "rtcp",
                "rtpevent",
                "sip.P-Served-User",
            ],
        )

    def test_the_line_of_each_filter(self):
        found = dict((filter_, number) for number, filter_ in guide_filters.filters(self.guide(GUIDE)))
        self.assertEqual(found["sip.Privacy"], 7)
        self.assertEqual(found["rtpevent"], 9)

    def test_a_table_of_other_things_is_left_out(self):
        found = [filter_ for _, filter_ in guide_filters.filters(self.guide(GUIDE))]
        self.assertNotIn("internal/sip", found)

    def test_a_guide_without_a_filter_table(self):
        self.assertEqual(guide_filters.filters(self.guide("# A guide\n\nNo table here.\n")), [])

    def test_a_row_without_a_filter(self):
        rows = GUIDE.replace("| `sip.Privacy` |", "| sip.Privacy |")
        with self.assertRaises(ValueError):
            guide_filters.filters(self.guide(rows))


if __name__ == "__main__":
    unittest.main()
