#!/usr/bin/env python3
"""Tests of the check of the commit messages of a pull request.

    python3 -m unittest discover -s scripts -p "*_test.py"

Each test builds a repository whose main holds a commit that the check
would reject, then a branch from main, and runs the script on it as CI
does.
"""

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().with_name("check_commit_messages.py")
TRAILER = "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
GOOD = "docs(sip): name the header that carries the route\n\nThe body says why.\n\n" + TRAILER


class ChecksTheCommitsOfABranch(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.repo = self.dir.name
        self.git("init", "-q", "-b", "main")
        self.commit("feat: start the repository")
        # Already on main, so never checked again.
        self.commit("Start the repository.\n\nClaude-Session: https://claude.ai/code/session_0123")
        self.git("checkout", "-q", "-b", "topic")

    def git(self, *args):
        env = dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")
        return subprocess.run(
            ["git", "-c", "user.name=test", "-c", "user.email=test@example.invalid", *args],
            cwd=self.repo, env=env, check=True, capture_output=True, text=True,
        ).stdout

    def commit(self, message):
        self.git("commit", "-q", "--allow-empty", "-m", message)

    def check(self, base="main", head="HEAD"):
        result = subprocess.run(
            [sys.executable, str(SCRIPT), base, head],
            cwd=self.repo, capture_output=True, text=True,
        )
        return result.returncode, result.stdout

    def assertRejected(self, message):
        self.commit(GOOD)
        self.commit(message)
        code, out = self.check()
        self.assertEqual(code, 1, out)
        self.assertIn("1 problems in the commit messages", out)

    def test_good_messages_pass(self):
        self.commit(GOOD)
        self.commit("fix(scscf)!: answer 403 to a barred identity\n\n" + TRAILER)
        self.commit("ci: run the check of the commit messages")
        code, out = self.check()
        self.assertEqual(code, 0, out)

    def test_an_empty_range_passes(self):
        code, out = self.check()
        self.assertEqual(code, 0, out)

    def test_a_session_trailer(self):
        self.assertRejected(GOOD + "\nClaude-Session: https://example.invalid/somewhere")

    def test_a_link_to_a_conversation_in_the_body(self):
        for url in [
            "https://claude.ai/code/session_01ABCdef",
            "https://claude.ai/chat/3f2a",
            "https://claude.ai/share/3f2a",
            "https://chatgpt.com/c/68a1",
            "https://chatgpt.com/share/68a1",
            "https://chat.openai.com/c/68a1",
            "https://gemini.google.com/app/1a2b",
            "https://g.co/gemini/share/1a2b",
        ]:
            with self.subTest(url=url):
                self.git("reset", "-q", "--hard", "main")
                self.assertRejected("docs: say why\n\nAs discussed in " + url + ".\n\n" + TRAILER)

    def test_a_subject_without_the_conventional_form(self):
        for subject in [
            "Add the check of the commit messages",
            "feature(ci): add the check",
            "ci(CI): add the check",
            "ci:add the check",
            "ci: ",
            "ci: add the check.",
            'Revert "ci: add the check"',
        ]:
            with self.subTest(subject=subject):
                self.git("reset", "-q", "--hard", "main")
                self.assertRejected(subject + "\n\n" + TRAILER)

    def test_commits_on_main_after_the_branch_point_are_not_checked(self):
        self.commit(GOOD)
        self.git("checkout", "-q", "main")
        self.commit("Change main after the branch point.\n\nClaude-Session: https://claude.ai/code/session_4567")
        self.git("checkout", "-q", "topic")
        code, out = self.check()
        self.assertEqual(code, 0, out)

    def test_the_head_need_not_be_checked_out(self):
        self.commit("Bad subject")
        self.git("checkout", "-q", "main")
        code, out = self.check(head="topic")
        self.assertEqual(code, 1, out)


if __name__ == "__main__":
    unittest.main()
