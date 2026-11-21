package pptx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

const sample = "---\ntitle: A sample & more\nfooter: study session 9. Sample\nlang: ja\n---\n\n" +
	"# 表紙\n\n副題\n\n---\n\n" +
	"## 本文\n\n- 箇条書きと `code` と [リンク](https://example.org/a?b=1&c=2)\n  - 入れ子\n\n" +
	"| 列 | 値 |\n| --- | --- |\n| a | 1 |\n\n```go\nfunc main() {}\n\n// <end>\n```\n\n---\n\n# 区切り\n\n---\n\n## 図\n\n```ladder\nUE, P-CSCF\nUE -> P-CSCF: REGISTER\nUE <-- P-CSCF: 401\n```\n\n---\n\n" +
	"[layout: answers]\n\n## 解答\n\n1. 一\n2. 二\n"

// build writes the pptx of the sample and returns its parts by name.
func build(t *testing.T) map[string][]byte {
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
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if z.File[0].Name != "[Content_Types].xml" {
		t.Errorf("the first entry is %s", z.File[0].Name)
	}
	parts := map[string][]byte{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		parts[f.Name] = body
	}
	return parts
}

// TestPackageIsComplete checks what PowerPoint checks before it offers
// to repair a file, in the sample and in every deck: every part is
// well-formed XML and has a content type, and every relationship inside
// the package leads to a part.
func TestPackageIsComplete(t *testing.T) {
	for deckPath, parts := range decks(t) {
		checkPackage(t, deckPath, parts)
	}
}

func checkPackage(t *testing.T, deckPath string, parts map[string][]byte) {
	t.Helper()
	var types struct {
		Defaults []struct {
			Extension string `xml:"Extension,attr"`
		} `xml:"Default"`
		Overrides []struct {
			PartName string `xml:"PartName,attr"`
		} `xml:"Override"`
	}
	if err := xml.Unmarshal(parts["[Content_Types].xml"], &types); err != nil {
		t.Fatal(err)
	}
	typed := map[string]bool{}
	for _, o := range types.Overrides {
		typed[strings.TrimPrefix(o.PartName, "/")] = true
		if parts[strings.TrimPrefix(o.PartName, "/")] == nil {
			t.Errorf("%s: content type for %s, which does not exist", deckPath, o.PartName)
		}
	}
	for name, body := range parts {
		decoder := xml.NewDecoder(bytes.NewReader(body))
		for {
			if _, err := decoder.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s: %s: %v", deckPath, name, err)
			}
		}
		if !typed[name] && !strings.HasSuffix(name, ".rels") && name != "[Content_Types].xml" {
			t.Errorf("%s: %s has no content type", deckPath, name)
		}
		if !strings.HasSuffix(name, ".rels") {
			continue
		}
		var rels struct {
			Relationship []struct {
				Target     string `xml:"Target,attr"`
				TargetMode string `xml:"TargetMode,attr"`
			}
		}
		if err := xml.Unmarshal(body, &rels); err != nil {
			t.Fatal(err)
		}
		// The relationships of a part are in _rels/ next to it, and
		// their targets are relative to the part.
		base := path.Dir(path.Dir(name))
		for _, r := range rels.Relationship {
			if r.TargetMode == "External" {
				continue
			}
			if target := path.Join(base, r.Target); parts[target] == nil {
				t.Errorf("%s: %s: target %s does not exist", deckPath, name, target)
			}
		}
	}
}

func TestSlidesAreNativeObjects(t *testing.T) {
	parts := build(t)
	for name := range parts {
		if strings.Contains(name, "notesSlide") || strings.Contains(name, "notesMaster") || strings.Contains(name, "media/") {
			t.Errorf("the package has %s", name)
		}
	}
	all := ""
	for i := 1; i <= 5; i++ {
		slide := string(parts["ppt/slides/slide"+string(rune('0'+i))+".xml"])
		if slide == "" {
			t.Fatalf("no slide %d", i)
		}
		if strings.Contains(slide, "<p:pic") {
			t.Errorf("slide %d has a picture", i)
		}
		all += slide
	}
	for _, want := range []string{
		`<p:ph type="ctrTitle"/>`, `<p:ph type="title"/>`, `<p:ph type="sldNum"`, `<a:tbl>`,
		`<a:t>箇条書きと </a:t>`, `typeface="Consolas"`, `<a:t>// &lt;end&gt;</a:t>`,
		`<a:buAutoNum type="arabicPeriod" startAt="1"/>`, `lang="ja-JP"`, `<a:hlinkClick r:id="rId2"/>`,
		`<p:grpSp>`, `<p:cxnSp>`, `<a:tailEnd type="triangle"`, `<a:prstDash val="dash"/>`, `flipH="1"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the slides have no %s", want)
		}
	}
	rels := string(parts["ppt/slides/_rels/slide2.xml.rels"])
	if !strings.Contains(rels, `Target="https://example.org/a?b=1&amp;c=2" TargetMode="External"`) {
		t.Errorf("slide 2 relationships = %s", rels)
	}
	if !strings.Contains(string(parts["docProps/core.xml"]), "A sample &amp; more") {
		t.Error("the title is not in the core properties")
	}
}

func TestBuildIsReproducible(t *testing.T) {
	a, b := build(t), build(t)
	for name := range a {
		if !bytes.Equal(a[name], b[name]) {
			t.Errorf("%s differs between two builds", name)
		}
	}
}

func TestURIReference(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"../../../docs/guide/ja/call.md#3-s-cscf-が番号を", "../../../docs/guide/ja/call.md#3-s-cscf-%E3%81%8C%E7%95%AA%E5%8F%B7%E3%82%92"},
		{"call.md#%E3%81%8C-and-が", "call.md#%E3%81%8C-and-%E3%81%8C"},
		{"https://example.org/a?b=1&c=2", "https://example.org/a?b=1&c=2"},
		{"https://[2001:db8::1]:8080/a b", "https://[2001:db8::1]:8080/a%20b"},
		{"guide [draft].md#50%-of-#2", "guide%20%5Bdraft%5D.md#50%25-of-%232"},
		{"mailto:someone@example.org", "mailto:someone@example.org"},
	} {
		if got := uriReference(c.in); got != c.want {
			t.Errorf("uriReference(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
