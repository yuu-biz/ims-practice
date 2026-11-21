package deck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLinks(t *testing.T) {
	dir := t.TempDir()
	guide := "# Guide\n\n## 1. The UE sends REGISTER\n\n```text\n## not a heading\n```\n\n## Run it\n\n## Run it\n"
	if err := os.WriteFile(filepath.Join(dir, "guide.md"), []byte(guide), 0o644); err != nil {
		t.Fatal(err)
	}
	source := "---\ntitle: T\nfooter: F\nlang: en\n---\n\n# T\n\n---\n\n## Links\n\n" +
		"- [ok](guide.md#1-the-ue-sends-register)\n" +
		"- [second of two](guide.md#run-it-1)\n" +
		"- [in a fence](guide.md#not-a-heading)\n" +
		"- [no file](missing.md)\n" +
		"- [web](https://example.org/x#y)\n"
	d, err := Parse(filepath.Join(dir, "deck.md"), source)
	if err != nil {
		t.Fatal(err)
	}
	problems := CheckLinks(d)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want two", problems)
	}
	if !strings.Contains(problems[0].Error(), "#not-a-heading") || !strings.Contains(problems[1].Error(), "missing.md does not exist") {
		t.Errorf("problems = %v", problems)
	}
}
