package deck

import (
	"errors"
	"strings"
)

// parseInline reads the inline markup of one line: **bold**, `code`,
// [text](address), <https://address> and the escape "\" before a mark.
// Any other character, "<" and "*" included, is text.
func parseInline(text string) ([]Span, error) {
	var spans []Span
	bold := false
	var b strings.Builder
	flush := func(href string) {
		if b.Len() > 0 {
			spans = appendSpan(spans, Span{Text: b.String(), Bold: bold, Href: href})
			b.Reset()
		}
	}
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '\\' && i+1 < len(text) && strings.IndexByte("\\`*[]()<>|#_-", text[i+1]) >= 0:
			b.WriteByte(text[i+1])
			i += 2
		case c == '`':
			ticks := len(text[i:]) - len(strings.TrimLeft(text[i:], "`"))
			end := strings.Index(text[i+ticks:], text[i:i+ticks])
			if end < 0 {
				return nil, errors.New("inline code is not closed with a backtick")
			}
			flush("")
			code := text[i+ticks : i+ticks+end]
			if ticks > 1 {
				code = strings.TrimSpace(code)
			}
			spans = appendSpan(spans, Span{Text: code, Code: true, Bold: bold})
			i += 2*ticks + end
		case strings.HasPrefix(text[i:], "**"):
			flush("")
			bold = !bold
			i += 2
		case c == '[':
			label, href, n, ok := link(text[i:])
			if !ok {
				b.WriteByte(c)
				i++
				continue
			}
			flush("")
			inner, err := parseInline(label)
			if err != nil {
				return nil, err
			}
			for _, span := range inner {
				span.Href = href
				span.Bold = span.Bold || bold
				spans = appendSpan(spans, span)
			}
			i += n
		case c == '<' && (strings.HasPrefix(text[i:], "<http://") || strings.HasPrefix(text[i:], "<https://")):
			end := strings.IndexByte(text[i:], '>')
			if end < 0 {
				return nil, errors.New("an address in <...> is not closed with >")
			}
			flush("")
			address := text[i+1 : i+end]
			spans = appendSpan(spans, Span{Text: address, Href: address, Bold: bold})
			i += end + 1
		default:
			b.WriteByte(c)
			i++
		}
	}
	flush("")
	if bold {
		return nil, errors.New("** is not closed")
	}
	return spans, nil
}

// link reads "[label](href)" at the start of text, with a label that may
// hold inline code, and returns how many bytes it takes.
func link(text string) (label, href string, n int, ok bool) {
	depth := 0
	inCode := false
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '`':
			inCode = !inCode
		case inCode:
		case c == '\\':
			i++
		case c == '[':
			depth++
		case c == ']':
			depth--
			if depth == 0 {
				if i+1 >= len(text) || text[i+1] != '(' {
					return "", "", 0, false
				}
				end := strings.IndexByte(text[i+2:], ')')
				if end < 0 {
					return "", "", 0, false
				}
				return text[1:i], text[i+2 : i+2+end], i + 2 + end + 1, true
			}
		}
	}
	return "", "", 0, false
}

// appendSpan merges a span into the one before it when both have the
// same style, so that a writer makes one run of each.
func appendSpan(spans []Span, span Span) []Span {
	if n := len(spans); n > 0 {
		last := &spans[n-1]
		if last.Bold == span.Bold && last.Code == span.Code && last.Href == span.Href {
			last.Text += span.Text
			return spans
		}
	}
	return append(spans, span)
}
