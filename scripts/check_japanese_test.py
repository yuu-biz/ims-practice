import unittest

import check_japanese


class FindsTheRules(unittest.TestCase):
    def test_a_heading_with_a_full_stop_inside(self):
        self.assertEqual(len(check_japanese.findings("## CAT。着信側が選ぶ呼び出し音\n")), 1)

    def test_a_heading_with_a_detail_in_parentheses(self):
        self.assertEqual(check_japanese.findings("## CAT（着信側が選ぶ呼び出し音）\n"), [])

    def test_a_full_stop_at_the_end_or_in_code(self):
        self.assertEqual(check_japanese.findings("## 終わり。\n## `a。b` を読む\n"), [])

    def test_a_comment_in_a_code_block(self):
        self.assertEqual(check_japanese.findings("```sh\n# 通話。発信する\n```\n"), [])

    def test_the_old_name_of_a_sequence_diagram(self):
        self.assertEqual(len(check_japanese.findings("手順書のラダー図を見ます。\n")), 1)
        self.assertEqual(check_japanese.findings("手順書のシーケンス図を見ます。`ladder` の行。\n"), [])
        self.assertEqual(check_japanese.findings("「ラダー図」とは呼びません。\n"), [])


if __name__ == "__main__":
    unittest.main()
