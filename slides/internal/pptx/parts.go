package pptx

// The parts of the package that are the same in every deck: the
// relationship types, the content types, the theme, the slide master and
// the slide layouts. The names of the parts and the order of the elements
// are those of ECMA-376 Part 1 (PresentationML, DrawingML) and Part 2
// (Open Packaging Conventions); PowerPoint refuses a file whose elements
// come in another order.

const (
	nsA  = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsR  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsP  = "http://schemas.openxmlformats.org/presentationml/2006/main"
	nsCT = "http://schemas.openxmlformats.org/package/2006/content-types"
	nsPR = "http://schemas.openxmlformats.org/package/2006/relationships"

	relOfficeDocument = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	relCore           = "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"
	relExtended       = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties"
	relSlideMaster    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster"
	relSlideLayout    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout"
	relSlide          = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide"
	relTheme          = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme"
	relPresProps      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/presProps"
	relViewProps      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/viewProps"
	relTableStyles    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/tableStyles"
	relHyperlink      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"

	typeRelationships = "application/vnd.openxmlformats-package.relationships+xml"
	typePresentation  = "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"
	typeSlideMaster   = "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"
	typeSlideLayout   = "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"
	typeSlide         = "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"
	typeTheme         = "application/vnd.openxmlformats-officedocument.theme+xml"
	typePresProps     = "application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"
	typeViewProps     = "application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"
	typeTableStyles   = "application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"
	typeCore          = "application/vnd.openxmlformats-package.core-properties+xml"
	typeExtended      = "application/vnd.openxmlformats-officedocument.extended-properties+xml"
)

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// The slide layouts of the master. A slide of the deck uses the first
// for the title slide, the second for a section and the third for every
// other layout, whose boxes are placed on the slide itself.
var slideLayouts = []struct {
	name, kind string
}{
	{"Title Slide", "title"},
	{"Section Header", "secHead"},
	{"Title Only", "titleOnly"},
}

const (
	layoutTitle   = 1
	layoutSection = 2
	layoutOther   = 3
)

// The placeholders of the master and the layouts. The slides refer to
// them by type and index, so that PowerPoint takes the title of a slide
// for its outline and the footer for its header and footer settings.
const (
	indexBody   = 1
	indexFooter = 11
	indexNumber = 12
)

// slideNumberField identifies the field that shows the number of the
// slide. Every slide uses the same one, as the slides that PowerPoint
// makes from a layout do.
const slideNumberField = "{6D3D1F5B-6F2A-4A4E-9C55-1B2E3F4A5B6C}"

const presProps = xmlHeader + `<p:presentationPr xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `"/>`

const viewProps = xmlHeader + `<p:viewPr xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `">` +
	`<p:normalViewPr><p:restoredLeft sz="15620"/><p:restoredTop sz="94660"/></p:normalViewPr>` +
	`<p:gridSpacing cx="76200" cy="76200"/></p:viewPr>`

// The default table style is the identifier of "Medium Style 2 - Accent
// 1", the one PowerPoint itself names here. The tables of the decks set
// their own fills and lines and use no style.
const tableStyles = xmlHeader + `<a:tblStyleLst xmlns:a="` + nsA + `" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`
