package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

func writeDeck(t *testing.T, path, lang, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	source := "---\ntitle: T\nfooter: F\nlang: " + lang + "\n---\n\n# T\n\n" + body
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTranslationsHaveTheSameSlides(t *testing.T) {
	dir := t.TempDir()
	writeDeck(t, filepath.Join(dir, "en", "01-a.md"), "en", "---\n\n## One\n\ntext\n")
	writeDeck(t, filepath.Join(dir, "ja", "01-a.md"), "ja", "---\n\n[layout: summary]\n\n## 一\n\n本文\n")
	writeDeck(t, filepath.Join(dir, "en", "02-b.md"), "en", "")
	_, problems := load(dir)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want two", problems)
	}
	if !strings.Contains(problems[0].Error(), "differ in their slides") || !strings.Contains(problems[1].Error(), "02-b.md exists in 1 of the 2 languages") {
		t.Errorf("problems = %v", problems)
	}
}

func TestLanguageMatchesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	writeDeck(t, filepath.Join(dir, "en", "01-a.md"), "ja", "")
	if _, problems := load(dir); len(problems) != 1 || !strings.Contains(problems[0].Error(), "lang is ja") {
		t.Errorf("problems = %v", problems)
	}
}

func TestCheckRefusesAnExcerptThatIsNotInItsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lock.go"), []byte("package lock\n\nfunc (u *UserLocks) Lock(ctx context.Context, user string) (unlock func(), err error) {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	slide := "---\n\n## Code\n\n[lock.go](../lock.go)\n\n```go\nfunc (u *UserLocks) Lock(user string) func() {\n```\n"
	writeDeck(t, filepath.Join(dir, "en", "01-a.md"), "en", slide)
	writeDeck(t, filepath.Join(dir, "ja", "01-a.md"), "ja", slide)
	_, problems := load(dir)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want one for each language", problems)
	}
	for _, p := range problems {
		if !strings.Contains(p.Error(), "Lock(user string)") || !strings.Contains(p.Error(), "is not in ../lock.go") {
			t.Errorf("problem = %v, want the excerpt that lock.go does not have", p)
		}
	}
}

func TestAbsoluteLinks(t *testing.T) {
	dir := t.TempDir()
	writeDeck(t, filepath.Join(dir, "en", "01-a.md"), "en", "---\n\n## One\n\n- [guide](../../../docs/x.md)\n- [web](https://example.org/)\n")
	all, _ := load(dir)
	absoluteLinks(all[0].pages, "https://host/owner/repo/blob/abc/")
	var links []string
	for _, e := range all[0].pages[1].Elements {
		if box, ok := e.(*layout.TextBox); ok {
			for _, p := range box.Paragraphs {
				for _, r := range p.Runs {
					if r.Href != "" {
						links = append(links, r.Href)
					}
				}
			}
		}
	}
	if strings.Join(links, " ") != "https://host/owner/repo/blob/abc/docs/x.md https://example.org/" {
		t.Errorf("links = %v", links)
	}
}
