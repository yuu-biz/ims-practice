import unittest

import check_deck_content as c

OLD = """---
title: t
footer: f
lang: ja
---

# Title

---

## Run it

```sh
docker compose up -d
```

---

## Review

| Question | Answer |
| --- | --- |
| Why twice | `nonce` |
"""

RECORD = """基準のコミット: `0123456789abcdef0123456789abcdef01234567`

## ja/04-registration.md

| 旧の見出し | 内容 | 扱い | 行き先 | 確かめる語 |
| --- | --- | --- | --- | --- |
{rows}
"""

DECK = "slides/decks/ja/04-registration.md"


def problems(new_deck, rows, files):
    _, by_deck, record_problems = c.read_record(RECORD.format(rows=rows))
    new = {} if new_deck is None else {DECK: new_deck}
    return record_problems + c.check({DECK: OLD}, new, by_deck, files.get)


CHAPTER = "# Two\n\n## Why twice\n\nThe 401 carries a `nonce`.\n\n## Other\n\nx\n"
GUIDE = "# Guide\n\n## Run\n\n```sh\ndocker compose up -d\n```\n"
MOVED = ("| Review | the answer as an explanation | 課題から解説へ | `docs/book/ja/two.md#why-twice` | `nonce` |\n"
         "| Run it | the commands | 移動 | `docs/guide/ja/registration.md#run` | `docker compose up -d` |")
FILES = {"docs/book/ja/two.md": CHAPTER, "docs/guide/ja/registration.md": GUIDE}


class TheBaselineIsKept(unittest.TestCase):
    # A deck that is still the same has nothing to report.
    def test_an_unchanged_deck(self):
        self.assertEqual(problems(OLD, "", FILES), [])

    # Slides whose content moved, with rows that say where, pass.
    def test_slides_moved_where_the_record_says(self):
        self.assertEqual(problems("---\ntitle: t\nfooter: f\nlang: ja\n---\n\n# Title\n", MOVED, FILES), [])


class TheRecordIsChecked(unittest.TestCase):
    # A deck that is gone has to have its slides in the record.
    def test_a_removed_deck_without_rows(self):
        found = problems(None, "", FILES)
        self.assertTrue(any("'Run it' is gone" in p for p in found), found)
        self.assertTrue(any("'Review' is gone" in p for p in found), found)

    # A row whose place does not exist fails, not only its file name.
    def test_a_place_that_does_not_exist(self):
        rows = MOVED.replace("#why-twice", "#no-such-heading")
        found = problems("---\ntitle: t\nfooter: f\nlang: ja\n---\n\n# Title\n", rows, FILES)
        self.assertTrue(any("no heading or scene #no-such-heading" in p for p in found), found)
        found = problems("---\ntitle: t\nfooter: f\nlang: ja\n---\n\n# Title\n", MOVED, {"docs/guide/ja/registration.md": GUIDE})
        self.assertTrue(any("docs/book/ja/two.md does not exist" in p for p in found), found)

    # The words of a row have to be in the part of the file that it names.
    def test_a_word_that_is_not_in_its_section(self):
        files = dict(FILES, **{"docs/book/ja/two.md": "# Two\n\n## Why twice\n\nx\n\n## Other\n\n`nonce`\n"})
        found = problems("---\ntitle: t\nfooter: f\nlang: ja\n---\n\n# Title\n", MOVED, files)
        self.assertTrue(any("'nonce' is not where the row says" in p for p in found), found)

    # Content that is gone from the deck and from every place of its rows fails.
    def test_content_that_went_nowhere(self):
        files = dict(FILES, **{"docs/guide/ja/registration.md": "# Guide\n\n## Run\n\n`docker compose up -d`\n"})
        rows = MOVED.replace("| `docker compose up -d` |", "| |")
        found = problems("---\ntitle: t\nfooter: f\nlang: ja\n---\n\n# Title\n", rows, dict(files, **{"docs/guide/ja/registration.md": "# Guide\n\n## Run\n\nnothing\n"}))
        self.assertTrue(any("line of code is gone: docker compose up -d" in p for p in found), found)

    # Content that is only somewhere else in the file of a place, not in
    # the part that the row names, was not moved there.
    def test_content_elsewhere_in_the_file(self):
        files = dict(FILES, **{"docs/guide/ja/registration.md": "# Guide\n\n## Run\n\nnothing\n\n## Other\n\n```sh\ndocker compose up -d\n```\n"})
        rows = MOVED.replace("| `docker compose up -d` |", "| |")
        found = problems("---\ntitle: t\nfooter: f\nlang: ja\n---\n\n# Title\n", rows, files)
        self.assertTrue(any("line of code is gone: docker compose up -d" in p for p in found), found)

    # A row that drops content says why, and its words are what may go.
    def test_a_row_that_drops_content(self):
        rows = MOVED.split("\n")[0] + "\n| Run it | | 削除 | | `docker compose up -d` |"
        found = problems("---\ntitle: t\nfooter: f\nlang: ja\n---\n\n# Title\n", rows, FILES)
        self.assertTrue(any("says why" in p for p in found), found)
        rows = MOVED.split("\n")[0] + "\n| Run it | the guide has the commands | 削除 | | `docker compose up -d` |"
        self.assertEqual(problems("---\ntitle: t\nfooter: f\nlang: ja\n---\n\n# Title\n", rows, FILES), [])

    # A row for a slide that the baseline did not have is stale.
    def test_a_row_for_a_slide_that_was_not_there(self):
        rows = MOVED + "\n| Nothing | x | 移動 | `docs/book/ja/two.md` | |"
        found = problems("---\ntitle: t\nfooter: f\nlang: ja\n---\n\n# Title\n", rows, FILES)
        self.assertTrue(any("had no slide 'Nothing'" in p for p in found), found)


class TheYAMLOfAScene(unittest.TestCase):
    # A block fenced with tildes is code too.
    def test_a_tilde_fence(self):
        self.assertEqual(c.atoms("~~~sh\nmake it\n~~~\n"), {("line of code", "make it")})

    # A scene reworded with its code kept is not a loss; its code gone is.
    def test_reworded_yaml_keeps_its_code(self):
        old = "```ims-scene\nid: s\nlead: The REGISTER goes to `sip:ims` first.\n```\n"
        self.assertEqual(c.atoms(old), {("inline code", "sip:ims")})


class TheBaselineIsRead(unittest.TestCase):
    # A baseline that git cannot read stops the check instead of passing it.
    def test_an_unreadable_baseline(self):
        with self.assertRaises(c.Failure):
            c.git("show", "0000000000000000000000000000000000000000:slides/decks/ja/x.md")


if __name__ == "__main__":
    unittest.main()
