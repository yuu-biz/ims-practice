#!/usr/bin/env python3
"""Check the messages of the commits that a pull request adds.

The history is public. A conversation with an AI assistant is not, so a
link to one leads nowhere for a reader and records where a private text
lives. Each subject has the form of Conventional Commits, with one of
the types that this repository uses.

Only the commits from the merge base of BASE and HEAD to HEAD are read:
the commits that BASE already holds were checked when they were added.

Usage: check_commit_messages.py BASE [HEAD]   (default HEAD: HEAD)
"""
import re
import subprocess
import sys

TYPES = ["feat", "fix", "refactor", "test", "docs", "build", "ci", "chore"]
# type(scope)!: subject, where the scope and the ! are optional. Git's
# own "Revert ..." subject does not have this form, so a revert is
# written as a commit of its own type.
SUBJECT = re.compile(r"^(" + "|".join(TYPES) + r")(\([a-z0-9-]+\))?!?: \S")

# The trailer that names a session, whatever it links to, and the forms
# of the URLs of a conversation or of a shared copy of one.
PATTERNS = [
    ("session trailer", re.compile(r"^Claude-Session:", re.I)),
    ("link to a conversation", re.compile(
        r"claude\.ai/(code/session_|chat/|share/)"
        r"|chatgpt\.com/(c|share)/|chat\.openai\.com/(c|share)/"
        r"|gemini\.google\.com/(app|share)/|g\.co/gemini/share/"
        r"|copilot\.microsoft\.com/(chats|shares)/", re.I)),
]


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


def commits(base, head):
    """Yield (sha, message) for each commit from the merge base to head, oldest first."""
    merge_base = git("merge-base", base, head).strip()
    out = git("log", "--reverse", "--format=%H%n%B%x00", f"{merge_base}..{head}")
    for record in out.split("\0"):
        record = record.lstrip("\n")
        if record:
            sha, _, message = record.partition("\n")
            yield sha, message


def problems(message):
    subject = message.split("\n", 1)[0]
    if not SUBJECT.match(subject):
        yield "subject is not type(scope): subject", subject
    elif subject.endswith("."):
        yield "subject ends with a period", subject
    for line in message.splitlines():
        for what, pattern in PATTERNS:
            match = pattern.search(line)
            if match:
                yield what, match.group(0)


def main():
    if not 2 <= len(sys.argv) <= 3:
        print(__doc__.rsplit("\n\n", 1)[-1].strip(), file=sys.stderr)
        return 2
    base, head = sys.argv[1], sys.argv[2] if len(sys.argv) == 3 else "HEAD"
    found = 0
    for sha, message in commits(base, head):
        for what, text in problems(message):
            found += 1
            print(f"{sha[:12]}: {what}: {text}")
    print(f"{found} problems in the commit messages")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
