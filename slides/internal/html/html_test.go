package html

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

const sample = "---\ntitle: A <sample>\nfooter: study session 9. Sample\nlang: ja\n---\n\n" +
	"# 表紙\n\n副題\n\n---\n\n" +
	"## 本文\n\n- `a < b` と [リンク](../guide.md#step)\n\n| 列 | 値 |\n| --- | ---: |\n| a | 1 |\n\n```\nline\n\nend\n```\n"

func build(t *testing.T) string {
	t.Helper()
	d, err := deck.Parse("sample.md", sample)
	if err != nil {
		t.Fatal(err)
	}
	pages, problems := layout.Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	var out bytes.Buffer
	if err := Write(&out, d, pages); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestOneSectionPerSlide(t *testing.T) {
	page := build(t)
	if n := strings.Count(page, `<section class="slide `); n != 2 {
		t.Errorf("%d sections, want 2", n)
	}
	for _, want := range []string{
		`<html lang="ja">`, `<title>A &lt;sample&gt;</title>`, `<section class="slide page content" id="2"`,
		`<code>a &lt; b</code>`, `<a href="../guide.md#step">`, `text-align:right`, `<th `,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %s", want)
		}
	}
}

// TestSelfContained makes sure that the page loads nothing: it has to
// open from a CI artifact on a machine without a network.
func TestSelfContained(t *testing.T) {
	page := build(t)
	if regexp.MustCompile(`(?i)<(script|link|img|iframe)[^>]+(src|href)=`).MatchString(page) {
		t.Error("the page loads a file")
	}
	if strings.Contains(page, "<!--") {
		t.Error("the page has a comment")
	}
}

func TestEmptyCodeLineKeepsItsHeight(t *testing.T) {
	if !regexp.MustCompile(`<p class="mono"[^>]*><br></p>`).MatchString(build(t)) {
		t.Error("the empty line of the code block has no <br>")
	}
}
