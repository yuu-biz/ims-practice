package layout

import "strings"

// arialWidths are the advance widths of the printable ASCII characters
// in Arial, from " " (0x20) to "~" (0x7E), in thousandths of an em.
var arialWidths = [95]float64{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278, // ' ' to '/'
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556, // '0' to '?'
	1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778, // '@' to 'O'
	667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556, // 'P' to '_'
	333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556, // '`' to 'o'
	556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584, // 'p' to '~'
}

const (
	// A monospace character is 0.6 em wide in the fonts of FontMono and
	// CSSMono, a little more in some; the measure keeps a margin.
	monoWidth = 0.62
	// Bold letters are wider than regular ones.
	boldFactor = 1.08
	// A line is filled to this share of its box at most, for the fonts
	// whose letters are wider than the measure.
	fillLimit = 0.97
)

// wide reports a character that takes a full em: the kanji, kana,
// Hangul and full-width forms, and every other character outside the
// Latin, Greek and Cyrillic scripts, whose width the measure does not
// know. Punctuation such as "…" and the arrows is narrow in a Latin
// font and full width in a Japanese one; it counts as wide.
func wide(r rune) bool {
	return r >= 0x0250 && !(r >= 0x0370 && r < 0x0530)
}

// runeWidth is the width of a character in ems.
func runeWidth(r rune, mono, bold bool) float64 {
	if wide(r) {
		return 1
	}
	if mono {
		return monoWidth
	}
	w := 0.6
	if r >= 0x20 && r <= 0x7E {
		w = arialWidths[r-0x20] / 1000
	}
	if bold {
		w *= boldFactor
	}
	return w
}

// runSize is the size of the text of a run in a paragraph of a size.
func runSize(run Run, size float64) float64 {
	if run.Code {
		return size * CodeScale
	}
	return size
}

// textWidth is the width of text in points.
func textWidth(text string, size float64, mono, bold bool) float64 {
	w := 0.0
	for _, r := range text {
		w += runeWidth(r, mono, bold)
	}
	return w * size
}

// piece is a part of a line that the wrapping keeps together: a word, a
// run of spaces, or one wide character with the punctuation that may not
// begin or end a line next to it.
type piece struct {
	width float64
	space bool
	text  string
}

// noLineStart are the characters that may not begin a line, and
// noLineEnd those that may not end one, in Japanese text.
const (
	noLineStart = "、。，．・：；？！ー）」』】〕〉》｝ぁぃぅぇぉっゃゅょァィゥェォッャュョ%)]},.:;!?"
	noLineEnd   = "（「『【〔〈《｛([{"
)

// pieces cuts runs of text into the pieces that a line break may come
// between. Latin words break at spaces; wide characters break between
// any two of them, except before a character of noLineStart and after
// one of noLineEnd.
//
// Where Japanese punctuation meets a Latin word without a space, as in
// "、SubscribeSignallingPath" or "AAR（", the applications differ: a
// browser breaks there, LibreOffice does not. The measure keeps the two
// together, so that the text fits in either.
func pieces(runs []Run, size float64) []piece {
	var out []piece
	var current piece
	open := false       // current holds characters
	glue := false       // the last character may not end a line
	afterPunct := false // the last character is wide punctuation that closes
	lastNarrow := false // the last character is a Latin one
	closeCurrent := func() {
		if open {
			out = append(out, current)
		}
		current = piece{}
		open = false
	}
	// reopen takes the piece before back, for a character that may not
	// begin a line.
	reopen := func() {
		if !open && !glue && len(out) > 0 && !out[len(out)-1].space {
			current = out[len(out)-1]
			out = out[:len(out)-1]
			open = true
		}
	}
	for _, run := range runs {
		for _, r := range run.Text {
			w := runeWidth(r, run.Code, run.Bold) * runSize(run, size)
			switch {
			case r == ' ' || r == '\u3000':
				closeCurrent()
				out = append(out, piece{width: w, space: true, text: string(r)})
				glue, afterPunct, lastNarrow = false, false, false
				continue
			case strings.ContainsRune(noLineStart, r):
				reopen()
				afterPunct = wide(r)
			case wide(r):
				opening := strings.ContainsRune(noLineEnd, r)
				if !glue && !(opening && open && lastNarrow) {
					closeCurrent()
				}
				afterPunct = false
			default:
				if !afterPunct && !lastNarrow && !glue {
					closeCurrent()
				}
				afterPunct = false
			}
			current.width += w
			current.text += string(r)
			open = true
			glue = strings.ContainsRune(noLineEnd, r)
			lastNarrow = !wide(r)
			if wide(r) && !glue && !afterPunct {
				closeCurrent()
			}
		}
	}
	closeCurrent()
	return out
}

// countLines is the number of lines that the runs take in a box of the
// given width, with explicit breaks (Run.Break) counted.
func countLines(runs []Run, size, width float64) int {
	limit := width * fillLimit
	lines := 0
	var segment []Run
	count := func() {
		lines += wrap(pieces(segment, size), limit)
		segment = nil
	}
	for _, run := range runs {
		if run.Break {
			count()
			continue
		}
		segment = append(segment, run)
	}
	count()
	return lines
}

// wrap fills lines greedily and returns how many it takes. Spaces at the
// end of a line take no room; a piece wider than a whole line is cut
// where it reaches the edge, as the applications do with a long address.
func wrap(ps []piece, limit float64) int {
	lines := 1
	used := 0.0
	pendingSpace := 0.0
	for _, p := range ps {
		if p.space {
			if used > 0 {
				pendingSpace += p.width
			}
			continue
		}
		switch {
		case used == 0:
			used = p.width
		case used+pendingSpace+p.width <= limit:
			used += pendingSpace + p.width
		default:
			lines++
			used = p.width
		}
		pendingSpace = 0
		for used > limit && limit > 0 {
			lines++
			used -= limit
		}
	}
	return lines
}

// WrapText cuts the runs into the lines that a box of the given width
// shows them in, as countLines counts them: the writers that draw each
// line themselves, such as an SVG, break the text where the layout
// measured it. The styles of the runs are not kept.
func WrapText(runs []Run, size, width float64) []string {
	limit := width * fillLimit
	var lines []string
	var segment []Run
	flush := func() {
		// A text that fits the box stays on one line: the measure
		// counts with some room to spare, which the drawing need not.
		if runsWidth(segment, size) <= width {
			text := ""
			for _, r := range segment {
				text += r.Text
			}
			lines = append(lines, text)
		} else {
			lines = append(lines, wrapLines(pieces(segment, size), limit)...)
		}
		segment = nil
	}
	for _, run := range runs {
		if run.Break {
			flush()
			continue
		}
		segment = append(segment, run)
	}
	flush()
	return lines
}

// wrapLines fills lines as wrap does and returns their text.
func wrapLines(ps []piece, limit float64) []string {
	var lines []string
	var line strings.Builder
	used, pendingSpace, pending := 0.0, 0.0, ""
	for _, p := range ps {
		if p.space {
			if used > 0 {
				pendingSpace += p.width
				pending += p.text
			}
			continue
		}
		switch {
		case used == 0:
			line.WriteString(p.text)
			used = p.width
		case used+pendingSpace+p.width <= limit:
			line.WriteString(pending + p.text)
			used += pendingSpace + p.width
		default:
			lines = append(lines, line.String())
			line.Reset()
			line.WriteString(p.text)
			used = p.width
		}
		pendingSpace, pending = 0, ""
		// A piece wider than a whole line is cut where it reaches the
		// edge, by the share of its width that fits.
		for used > limit && limit > 0 {
			text := []rune(line.String())
			keep := max(1, int(float64(len(text))*(1-(used-limit)/used)))
			if keep >= len(text) {
				break
			}
			lines = append(lines, string(text[:keep]))
			line.Reset()
			line.WriteString(string(text[keep:]))
			used -= limit
		}
	}
	return append(lines, line.String())
}

// longestPiece is the width of the widest piece: a box narrower than it
// cuts a word.
func longestPiece(runs []Run, size float64) float64 {
	longest := 0.0
	for _, p := range pieces(runs, size) {
		if !p.space && p.width > longest {
			longest = p.width
		}
	}
	return longest
}

// runsWidth is the width of the runs on one line, without wrapping; for
// runs with explicit breaks, the widest of their lines.
func runsWidth(runs []Run, size float64) float64 {
	widest, w := 0.0, 0.0
	for _, run := range runs {
		if run.Break {
			widest = max(widest, w)
			w = 0
			continue
		}
		w += textWidth(run.Text, runSize(run, size), run.Code, run.Bold)
	}
	return max(widest, w)
}

// columns counts the monospace columns of a line of code: a wide
// character takes the room of more than one.
func columns(text string) float64 {
	n := 0.0
	for _, r := range text {
		if wide(r) {
			n += 1 / monoWidth
		} else {
			n++
		}
	}
	return n
}
