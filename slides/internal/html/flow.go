package html

import (
	"fmt"
	"html"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// The flowing form of the blocks of a slide: the text of the longer
// explanation of a scene, the reading view, and every slide on a narrow
// screen, where a slide scaled to the width would be too small to read.

func flowBlocks(b *strings.Builder, blocks []deck.Block, repository string) {
	for _, blk := range blocks {
		switch blk := blk.(type) {
		case deck.Paragraph:
			b.WriteString("<p>")
			flowLines(b, blk.Lines, repository)
			b.WriteString("</p>")
		case deck.Quote:
			b.WriteString("<blockquote><p>")
			flowLines(b, blk.Lines, repository)
			b.WriteString("</p></blockquote>")
		case deck.List:
			flowList(b, blk, repository)
		case deck.Table:
			b.WriteString(`<div class="table"><table><thead><tr>`)
			for i, c := range blk.Header {
				fmt.Fprintf(b, `<th%s>`, alignAttr(blk.Align[i]))
				flowSpans(b, c, repository)
				b.WriteString("</th>")
			}
			b.WriteString("</tr></thead><tbody>")
			for _, row := range blk.Rows {
				b.WriteString("<tr>")
				for i, c := range row {
					fmt.Fprintf(b, `<td%s>`, alignAttr(blk.Align[i]))
					flowSpans(b, c, repository)
					b.WriteString("</td>")
				}
				b.WriteString("</tr>")
			}
			b.WriteString("</tbody></table></div>")
		case deck.Code:
			b.WriteString(`<pre class="mono"><code>`)
			b.WriteString(html.EscapeString(strings.Join(blk.Lines, "\n")))
			b.WriteString("</code></pre>")
		case deck.Ladder:
			elements, h, err := layout.LadderShapes(blk, layout.SlideWidth-2*layout.MarginX)
			if err != nil {
				// The layout of the slide already failed the build on it.
				continue
			}
			b.WriteString(`<figure class="ladder">`)
			elementsSVG(b, elements, layout.SlideWidth-2*layout.MarginX, h)
			b.WriteString("</figure>")
		}
	}
}

func alignAttr(a deck.Align) string {
	switch a {
	case deck.AlignCenter:
		return ` style="text-align:center"`
	case deck.AlignRight:
		return ` style="text-align:right"`
	}
	return ""
}

func flowList(b *strings.Builder, l deck.List, repository string) {
	if l.Ordered {
		start := ""
		if l.Start != 1 {
			start = fmt.Sprintf(` start="%d"`, l.Start)
		}
		fmt.Fprintf(b, "<ol%s>", start)
	} else {
		b.WriteString("<ul>")
	}
	for _, item := range l.Items {
		b.WriteString("<li>")
		flowLines(b, item.Lines, repository)
		if item.Children != nil {
			flowList(b, *item.Children, repository)
		}
		b.WriteString("</li>")
	}
	if l.Ordered {
		b.WriteString("</ol>")
	} else {
		b.WriteString("</ul>")
	}
}

func flowLines(b *strings.Builder, lines [][]deck.Span, repository string) {
	for i, l := range lines {
		if i > 0 {
			b.WriteString("<br>")
		}
		flowSpans(b, l, repository)
	}
}

func flowSpans(b *strings.Builder, spans []deck.Span, repository string) {
	for _, s := range spans {
		text := html.EscapeString(s.Text)
		if s.Code {
			text = "<code>" + text + "</code>"
		}
		if s.Bold {
			text = "<b>" + text + "</b>"
		}
		if s.Href != "" {
			text = `<a href="` + html.EscapeString(Address(s.Href, repository)) + `">` + text + "</a>"
		}
		b.WriteString(text)
	}
}

// RepositoryRoot is how a deck at decks/<language>/<name>.md reaches the
// root of the repository. A built file sits as deep, at
// dist/<language>/<name>.html, so the relative links work inside a
// checkout; a copy elsewhere, such as a CI artifact, needs them absolute.
const RepositoryRoot = "../../../"

// Address is the address that a link of a deck gets in a built file:
// the same, or with the address of the repository tree in place of the
// way up to its root.
func Address(href, repository string) string {
	if repository != "" && strings.HasPrefix(href, RepositoryRoot) {
		return strings.TrimRight(repository, "/") + "/" + strings.TrimPrefix(href, RepositoryRoot)
	}
	return href
}
