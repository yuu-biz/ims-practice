package deck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckExcerpts(t *testing.T) {
	dir := t.TempDir()
	code := "package peer\n\n" +
		"// Timeout bounds the wait.\n" +
		"func (p *Peer) Request(ctx context.Context) error {\n" +
		"\tctx, cancel := context.WithTimeout(ctx, p.timeout)\n" +
		"\tdefer cancel()\n" +
		"\treturn nil\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "peer.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	source := "---\ntitle: T\nfooter: F\nlang: en\n---\n\n# T\n\n---\n\n## Excerpts\n\n" +
		"[peer.go](peer.go)\n\n" +
		"```go\nfunc (p *Peer) Request(ctx context.Context) error {\n\tctx, cancel := context.WithTimeout(ctx, p.timeout)\n```\n\n" +
		"```go\n\treturn nil\n}\n```\n\n" +
		"```sh\ngo test ./peer\n```\n\n" +
		"```go\nnot = an.excerpt()\n```\n\n" +
		"- A list ends the excerpts of peer.go.\n\n" +
		"```go\nnot = an.excerpt()\n```\n\n" +
		"---\n\n## Drift\n\n" +
		"[peer.go](peer.go)\n\n" +
		"```go\n\tctx, cancel := context.WithTimeout(ctx, p.node.timers.Answer)\n```\n\n" +
		"```go\n\tctx, cancel := context.WithTimeout(ctx, p.timeout)\n\treturn nil\n```\n\n" +
		"[missing.go](missing.go)\n\n" +
		"```go\nfunc gone() {}\n```\n"
	d, err := Parse(filepath.Join(dir, "deck.md"), source)
	if err != nil {
		t.Fatal(err)
	}
	problems := CheckExcerpts(d)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want two", problems)
	}
	for i, first := range []string{"p.node.timers.Answer", "p.timeout"} {
		if got := problems[i].Error(); !strings.Contains(got, "slide 3") || !strings.Contains(got, first) || !strings.Contains(got, "not in peer.go") {
			t.Errorf("problem %d = %q, want the excerpt of slide 3 with %q, not in peer.go", i, got, first)
		}
	}
}
