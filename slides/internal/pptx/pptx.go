// Package pptx writes a deck as a PowerPoint file (Office Open XML,
// ECMA-376) from the boxes that package layout placed. Every element is
// a native object that can be edited: the titles, the footers and the
// slide numbers are placeholders of the slide layouts, the text is in
// text boxes, a table is a:tbl, a ladder diagram is lines and text boxes.
// There is no picture and there are no notes slides.
package pptx

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// part is a file of the package.
type part struct {
	name, contentType, body string
}

// Write writes the pptx of a deck. The output depends only on the deck,
// so that building twice gives the same bytes.
func Write(out io.Writer, d *deck.Deck, pages []layout.Page) error {
	var parts []part
	add := func(name, contentType, body string) {
		parts = append(parts, part{name, contentType, body})
	}

	add("_rels/.rels", typeRelationships, relationships([]relationship{
		{relOfficeDocument, "ppt/presentation.xml", false},
		{relCore, "docProps/core.xml", false},
		{relExtended, "docProps/app.xml", false},
	}))
	add("docProps/core.xml", typeCore, coreProperties(d))
	add("docProps/app.xml", typeExtended, xmlHeader+`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties">`+
		fmt.Sprintf(`<Application>ims-practice slides</Application><Slides>%d</Slides><Notes>0</Notes>`, len(pages))+`</Properties>`)

	presentationRels := []relationship{
		{relSlideMaster, "slideMasters/slideMaster1.xml", false},
		{relPresProps, "presProps.xml", false},
		{relViewProps, "viewProps.xml", false},
		{relTheme, "theme/theme1.xml", false},
		{relTableStyles, "tableStyles.xml", false},
	}
	firstSlideRel := len(presentationRels) + 1
	var slideIDs strings.Builder
	for i := range pages {
		presentationRels = append(presentationRels, relationship{relSlide, fmt.Sprintf("slides/slide%d.xml", i+1), false})
		fmt.Fprintf(&slideIDs, `<p:sldId id="%d" r:id="rId%d"/>`, 256+i, firstSlideRel+i)
	}
	add("ppt/presentation.xml", typePresentation, xmlHeader+
		`<p:presentation xmlns:a="`+nsA+`" xmlns:r="`+nsR+`" xmlns:p="`+nsP+`" saveSubsetFonts="1">`+
		`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>`+
		`<p:sldIdLst>`+slideIDs.String()+`</p:sldIdLst>`+
		fmt.Sprintf(`<p:sldSz cx="%d" cy="%d"/>`, emu(layout.SlideWidth), emu(layout.SlideHeight))+
		`<p:notesSz cx="6858000" cy="9144000"/>`+
		`</p:presentation>`)
	add("ppt/_rels/presentation.xml.rels", typeRelationships, relationships(presentationRels))
	add("ppt/presProps.xml", typePresProps, presProps)
	add("ppt/viewProps.xml", typeViewProps, viewProps)
	add("ppt/tableStyles.xml", typeTableStyles, tableStyles)
	add("ppt/theme/theme1.xml", typeTheme, theme())

	masterRels := []relationship{}
	for i := range slideLayouts {
		masterRels = append(masterRels, relationship{relSlideLayout, fmt.Sprintf("../slideLayouts/slideLayout%d.xml", i+1), false})
		add(fmt.Sprintf("ppt/slideLayouts/slideLayout%d.xml", i+1), typeSlideLayout, slideLayout(i+1))
		add(fmt.Sprintf("ppt/slideLayouts/_rels/slideLayout%d.xml.rels", i+1), typeRelationships,
			relationships([]relationship{{relSlideMaster, "../slideMasters/slideMaster1.xml", false}}))
	}
	masterRels = append(masterRels, relationship{relTheme, "../theme/theme1.xml", false})
	add("ppt/slideMasters/slideMaster1.xml", typeSlideMaster, slideMaster())
	add("ppt/slideMasters/_rels/slideMaster1.xml.rels", typeRelationships, relationships(masterRels))

	for i, page := range pages {
		body, links := slide(d.Lang, page)
		rels := []relationship{{relSlideLayout, fmt.Sprintf("../slideLayouts/slideLayout%d.xml", layoutOf(page)), false}}
		for _, link := range links {
			rels = append(rels, relationship{relHyperlink, link, true})
		}
		add(fmt.Sprintf("ppt/slides/slide%d.xml", i+1), typeSlide, body)
		add(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1), typeRelationships, relationships(rels))
	}

	z := zip.NewWriter(out)
	// The content types come first, as most readers expect.
	if err := writeFile(z, "[Content_Types].xml", contentTypes(parts)); err != nil {
		return err
	}
	for _, p := range parts {
		if err := writeFile(z, p.name, p.body); err != nil {
			return err
		}
	}
	return z.Close()
}

// buildTime is the time written into the zip entries, so that the bytes
// of the file do not change from one build to the next.
var buildTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func writeFile(z *zip.Writer, name, body string) error {
	w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: buildTime})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, body)
	return err
}

type relationship struct {
	kind, target string
	external     bool
}

func relationships(rels []relationship) string {
	var b strings.Builder
	b.WriteString(xmlHeader + `<Relationships xmlns="` + nsPR + `">`)
	for i, r := range rels {
		mode := ""
		if r.external {
			mode = ` TargetMode="External"`
		}
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="%s" Target="%s"%s/>`, i+1, r.kind, escape(r.target), mode)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

// contentTypes lists the content type of every part but the
// relationships, which take theirs from the extension.
func contentTypes(parts []part) string {
	var b strings.Builder
	b.WriteString(xmlHeader + `<Types xmlns="` + nsCT + `">` +
		`<Default Extension="rels" ContentType="` + typeRelationships + `"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>`)
	for _, p := range parts {
		if p.contentType != typeRelationships {
			fmt.Fprintf(&b, `<Override PartName="/%s" ContentType="%s"/>`, p.name, p.contentType)
		}
	}
	b.WriteString(`</Types>`)
	return b.String()
}

func coreProperties(d *deck.Deck) string {
	return xmlHeader + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + escape(d.Title) + `</dc:title><dc:language>` + d.Lang + `</dc:language>` +
		`</cp:coreProperties>`
}
