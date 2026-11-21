package deck

import (
	"reflect"
	"strings"
	"testing"
)

const sample = "---\n" +
	"title: A sample\n" +
	"footer: study session 9. Sample\n" +
	"lang: en\n" +
	"---\n" +
	"\n" +
	"# A sample\n" +
	"\n" +
	"The subtitle\n" +
	"\n" +
	"---\n" +
	"\n" +
	"## Blocks\n" +
	"\n" +
	"A paragraph with **bold**, `code` and [a link](../guide.md#a-step).\n" +
	"Its second line.\n" +
	"\n" +
	"- one\n" +
	"  - nested\n" +
	"  continued\n" +
	"- two\n" +
	"\n" +
	"| Name | Value |\n" +
	"| --- | ---: |\n" +
	"| `a \\| b` | 1 |\n" +
	"\n" +
	"```sh\n" +
	"go test ./...\n" +
	"---\n" +
	"```\n" +
	"\n" +
	"> Guide: <https://example.org/guide>\n" +
	"\n" +
	"---\n" +
	"\n" +
	"# A part\n" +
	"\n" +
	"---\n" +
	"\n" +
	"[layout: two-columns]\n" +
	"\n" +
	"## Two columns\n" +
	"\n" +
	"1. left\n" +
	"2. more\n" +
	"\n" +
	"[column]\n" +
	"\n" +
	"right\n"

func TestParseSample(t *testing.T) {
	d, err := Parse("sample.md", sample)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "A sample" || d.Footer != "study session 9. Sample" || d.Lang != "en" {
		t.Errorf("front matter = %q, %q, %q", d.Title, d.Footer, d.Lang)
	}
	var layouts []Layout
	for _, s := range d.Slides {
		layouts = append(layouts, s.Layout)
	}
	if want := []Layout{LayoutTitle, LayoutContent, LayoutSection, LayoutTwoColumns}; !reflect.DeepEqual(layouts, want) {
		t.Fatalf("layouts = %v, want %v", layouts, want)
	}

	blocks := d.Slides[1].Columns[0]
	if len(blocks) != 5 {
		t.Fatalf("slide 2 has %d blocks, want 5: %#v", len(blocks), blocks)
	}
	para := blocks[0].(Paragraph)
	wantFirst := []Span{
		{Text: "A paragraph with "}, {Text: "bold", Bold: true}, {Text: ", "},
		{Text: "code", Code: true}, {Text: " and "}, {Text: "a link", Href: "../guide.md#a-step"}, {Text: "."},
	}
	if !reflect.DeepEqual(para.Lines[0], wantFirst) || len(para.Lines) != 2 {
		t.Errorf("paragraph = %#v", para.Lines)
	}
	list := blocks[1].(List)
	if len(list.Items) != 2 || list.Items[0].Children == nil ||
		PlainText(list.Items[0].Children.Items[0].Lines[1]) != "continued" {
		t.Errorf("list = %#v", list)
	}
	table := blocks[2].(Table)
	if got := table.Rows[0][0]; !reflect.DeepEqual(got, Cell{{Text: "a | b", Code: true}}) || table.Align[1] != AlignRight {
		t.Errorf("table = %#v", table)
	}
	code := blocks[3].(Code)
	if code.Info != "sh" || !reflect.DeepEqual(code.Lines, []string{"go test ./...", "---"}) {
		t.Errorf("code = %#v", code)
	}
	quote := blocks[4].(Quote)
	if quote.Lines[0][1].Href != "https://example.org/guide" {
		t.Errorf("quote = %#v", quote)
	}

	columns := d.Slides[3].Columns
	if len(columns) != 2 || columns[0][0].(List).Start != 1 || !columns[0][0].(List).Ordered {
		t.Errorf("columns = %#v", columns)
	}
}

func TestParseRejects(t *testing.T) {
	front := "---\ntitle: T\nfooter: F\nlang: en\n---\n\n# T\n\n---\n\n"
	tests := []struct {
		name, source, message string
	}{
		{"speaker note", front + "## S\n\n<!--\nSay this.\n-->\n", "speaker notes"},
		{"directive comment", front + "<!-- _class: lead -->\n## S\n", "speaker notes"},
		{"unknown key", "---\ntitle: T\nfooter: F\nlang: en\ntheme: x\n---\n\n# T\n", "unknown front matter key"},
		{"missing key", "---\ntitle: T\nlang: en\n---\n\n# T\n", `no "footer"`},
		{"unknown language", "---\ntitle: T\nfooter: F\nlang: fr\n---\n\n# T\n", "lang is one of"},
		{"unknown layout", front + "[layout: grid]\n\n## S\n", "unknown layout"},
		{"no heading", front + "text\n", "starts with its heading"},
		{"column outside two columns", front + "## S\n\na\n\n[column]\n\nb\n", "only for the two-columns"},
		{"one column", front + "[layout: two-columns]\n\n## S\n\na\n", "two columns"},
		{"first slide", "---\ntitle: T\nfooter: F\nlang: en\n---\n\n## T\n", "title slide"},
		{"open fence", front + "## S\n\n```\ncode\n", "not closed"},
		{"bad row", front + "## S\n\n| a | b |\n| --- | --- |\n| 1 |\n", "row has 1 cells"},
		{"second heading", front + "## S\n\n## Again\n", "one heading"},
		{"table on a section slide", front + "## S\n\n---\n\n# Part\n\n| a |\n| --- |\n| 1 |\n", "only its heading and paragraphs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("x.md", tt.source)
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Errorf("error = %v, want one with %q", err, tt.message)
			}
		})
	}
}

func TestCommentInsideCodeIsText(t *testing.T) {
	source := "---\ntitle: T\nfooter: F\nlang: en\n---\n\n# T\n\n---\n\n## S\n\n```xml\n<!-- a comment of the XML -->\n```\n\nThe text `<!--` is fine in code.\n"
	if _, err := Parse("x.md", source); err != nil {
		t.Fatal(err)
	}
}
