// Package layout places everything on a slide: it turns the blocks of a
// deck into boxes, lines and tables at fixed positions. The pptx writer
// and the HTML writer draw the same boxes, so the two outputs look the
// same and the text that fits in one fits in the other.
//
// The unit is the point. A slide is 960 by 540 points, 16:9, the size of
// a widescreen PowerPoint slide (13.333 by 7.5 inches). In the HTML a
// point is one CSS pixel of the slide.
package layout

import "github.com/yuu-biz/ims-practice/slides/internal/deck"

// The size of a slide.
const (
	SlideWidth  = 960.0
	SlideHeight = 540.0
)

// Colours, as RGB in hexadecimal, the form that both DrawingML and CSS
// take.
const (
	ColorText       = "1F2328"
	ColorMuted      = "57606A"
	ColorAccent     = "1F4E79"
	ColorRule       = "D0D7DE"
	ColorCodeFill   = "F3F5F8"
	ColorCodeLine   = "D8DEE4"
	ColorHeaderFill = "E6EDF5"
	ColorPanelFill  = "F2F6FA"
	ColorExercise   = "9A6700"
	ColorAnswers    = "1A7F37"
	ColorWhite      = "FFFFFF"
	ColorOnAccent   = "D6E2F0"
	ColorLifeline   = "8C959F"
	ColorNoteFill   = "FFF8E1"
	ColorNoteLine   = "D4A72C"
	ColorLink       = "0B5CAD"
)

// Colours of the diagrams of the scenes. Each family of protocols has a
// colour and a dash of its own; the label of a line also names its
// protocol, so that the colour is never the only sign.
const (
	ColorStage       = "F6F5F1"
	ColorNodeFill    = "FFFFFF"
	ColorNodeLine    = "AEB7BF"
	ColorFocusFill   = "F6E7C1"
	ColorFocusLine   = "1D2B36"
	ColorDimLine     = "D9DEE2"
	ColorDimText     = "8A949D"
	ColorInk         = "1D2B36"
	ColorGroupLine   = "BAC3CA"
	ColorProcessFill = "FFF4D6"
	ColorProcessLine = "B08A1E"
)

// FamilyColors are the colours of the lines of each family, see Family.
var FamilyColors = map[string]string{
	"sip":      "1F5F8B",
	"diameter": "9A5B00",
	"dns":      "5C6670",
	"http":     "1A7F64",
	"media":    "6E4BA8",
	"other":    "3D4A57",
	"relation": "6B7580",
	"process":  "8A6A12",
}

// Fonts. A run of text names a Latin and an East Asian font, and the
// application picks the one that has the glyph. The pptx names one font
// of each kind; the HTML lists more, for the machines that lack them.
// The text is measured with the widths of Arial, which are wider than
// those of Calibri and of the Latin letters of the Japanese fonts, so
// that a line that fits in the measure fits on the screen.
const (
	FontLatin     = "Calibri"
	FontEastAsian = "Yu Gothic"
	FontMono      = "Consolas"
)

// Font stacks of the HTML. The Japanese fonts are named because the
// generic sans-serif of a machine without a Japanese locale may draw
// kanji with Chinese glyphs.
const (
	CSSSans = `Calibri, Carlito, "Segoe UI", Arial, "Noto Sans CJK JP", "Noto Sans JP", "Yu Gothic UI", "Yu Gothic", YuGothic, "Hiragino Sans", Meiryo, sans-serif`
	CSSMono = `Consolas, "DejaVu Sans Mono", Menlo, "Liberation Mono", "Noto Sans Mono CJK JP", "MS Gothic", monospace`
)

// Sizes of text, in points, before a slide is scaled down to fit.
const (
	SizeTitleSlide = 40.0
	SizeSection    = 36.0
	SizeSubtitle   = 20.0
	SizeHeading    = 28.0
	SizeBody       = 20.0
	sizeTable      = 16.0
	sizeCode       = 14.0
	sizeQuote      = 14.0
	SizeFooter     = 11.0
	sizeTag        = 13.0
	sizeLadder     = 13.0

	minSizeCode = 8.0

	// CodeScale is the size of inline code in a paragraph of text, as a
	// share of the size of the text: a monospace font looks larger than
	// a proportional one of the same size.
	CodeScale = 0.9

	lineHeightTitle = 1.2
	lineHeightText  = 1.3
	lineHeightCode  = 1.25
)

// The frame of the content slides.
const (
	MarginX      = 48.0
	TitleTop     = 22.0
	BodyBottom   = 494.0
	FooterTop    = 508.0
	FooterHeight = 20.0
	blockGap     = 12.0
	columnGap    = 28.0
)

// scales are the factors that a slide is shrunk by, one after the other,
// until its content fits. Below the last one the text gets too small to
// read from the back of a room, and the build fails instead.
var scales = []float64{1, 0.95, 0.9, 0.85, 0.8}

// Words that the layouts put on a slide, by the language of the deck.
var tagWords = map[string]map[string]string{
	"en": {"exercise": "Exercise", "answers": "Answers", "details": "Details"},
	"ja": {"exercise": "演習", "answers": "解答", "details": "詳しく"},
}

// BasisWords say, under each diagram, what the diagram is drawn from.
var BasisWords = map[string]map[deck.Basis]string{
	"en": {
		deck.BasisRepository:   "A schematic of what this implementation does, drawn from the guide. It is not a capture.",
		deck.BasisStandard:     "A schematic of the procedure of the specification. It is not a capture.",
		deck.BasisIllustration: "An example made up to explain the idea, not values or a capture of this implementation.",
	},
	"ja": {
		deck.BasisRepository:   "この構成の動きを手順書から描いた模式図です。キャプチャそのものではありません。",
		deck.BasisStandard:     "仕様の手順を描いた模式図です。キャプチャそのものではありません。",
		deck.BasisIllustration: "考え方を説明するための例です。この構成の値やキャプチャではありません。",
	},
}
