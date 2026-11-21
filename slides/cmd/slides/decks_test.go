package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/html"
	"github.com/yuu-biz/ims-practice/slides/internal/pptx"
)

// TestDecks runs the checks of "slides check" on the decks of the
// repository, so that the tests of the repository fail on a deck that
// does not parse, has a speaker note, does not fit, links to a heading
// that is gone, differs between its languages, or names a command that
// the repository does not take.
func TestDecks(t *testing.T) {
	all, problems := load("../../decks")
	for _, p := range problems {
		t.Error(p)
	}
	for _, d := range all {
		var out bytes.Buffer
		if err := pptx.Write(&out, d.deck, d.pages); err != nil {
			t.Fatalf("%s: %v", d.deck.Path, err)
		}
		z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
		if err != nil {
			t.Fatalf("%s: %v", d.deck.Path, err)
		}
		slides := 0
		for _, f := range z.File {
			switch {
			case strings.HasPrefix(f.Name, "ppt/slides/_rels/"):
				checkHyperlinks(t, d.deck, f)
			case strings.HasPrefix(f.Name, "ppt/slides/slide"):
				slides++
			case strings.Contains(f.Name, "notes"), strings.HasPrefix(f.Name, "ppt/media/"):
				t.Errorf("%s: the pptx has %s", d.deck.Path, f.Name)
			}
		}
		if slides != len(d.deck.Slides) {
			t.Errorf("%s: %d slides in the pptx, %d in the deck", d.deck.Path, slides, len(d.deck.Slides))
		}
		out.Reset()
		if err := html.Write(&out, d.deck, d.pages); err != nil {
			t.Fatalf("%s: %v", d.deck.Path, err)
		}
		if n := strings.Count(out.String(), "<section "); n != len(d.deck.Slides) {
			t.Errorf("%s: %d slides in the HTML, %d in the deck", d.deck.Path, n, len(d.deck.Slides))
		}
	}
}

// uriReference matches the characters that RFC 3986 2 allows in a URI
// reference: unreserved, reserved and %XX.
var uriReference = regexp.MustCompile(`^(?:[A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=-]|%[0-9A-Fa-f]{2})*$`)

// checkHyperlinks makes sure that the targets of the hyperlinks of a
// slide are URI references, as the Open Packaging Conventions ask of
// the target of a relationship, and that decoding them gives back the
// links of the slide in the source, Japanese anchors included.
func checkHyperlinks(t *testing.T, d *deck.Deck, f *zip.File) {
	t.Helper()
	r, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	var rels struct {
		Relationship []struct {
			Target     string `xml:"Target,attr"`
			TargetMode string `xml:"TargetMode,attr"`
		}
	}
	if err := xml.Unmarshal(body, &rels); err != nil {
		t.Fatalf("%s: %s: %v", d.Path, f.Name, err)
	}
	var number int
	if _, err := fmt.Sscanf(f.Name, "ppt/slides/_rels/slide%d.xml.rels", &number); err != nil {
		t.Fatalf("%s: %s: %v", d.Path, f.Name, err)
	}
	var decoded []string
	for _, rel := range rels.Relationship {
		if rel.TargetMode != "External" {
			continue
		}
		if !uriReference.MatchString(rel.Target) || strings.Count(rel.Target, "#") > 1 {
			t.Errorf("%s: %s: target %q is not a URI reference", d.Path, f.Name, rel.Target)
		}
		if _, err := url.Parse(rel.Target); err != nil {
			t.Errorf("%s: %s: %v", d.Path, f.Name, err)
		}
		link, err := url.PathUnescape(rel.Target)
		if err != nil {
			t.Errorf("%s: %s: %v", d.Path, f.Name, err)
		}
		decoded = append(decoded, link)
	}
	links := deck.Links(d.Slides[number-1])
	slices.Sort(links)
	slices.Sort(decoded)
	if !slices.Equal(links, decoded) {
		t.Errorf("%s: slide %d: the hyperlinks decode to %q, the source has %q", d.Path, number, decoded, links)
	}
}
