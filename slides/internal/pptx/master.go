package pptx

import (
	"fmt"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// emu turns points into English Metric Units, the unit of DrawingML:
// 12700 to the point.
func emu(points float64) int64 {
	return int64(points*12700 + 0.5)
}

// hundredths turns points into the hundredths of a point that DrawingML
// uses for the size of text and for spacing.
func hundredths(points float64) int {
	return int(points*100 + 0.5)
}

func srgb(color string) string {
	return `<a:srgbClr val="` + color + `"/>`
}

func solidFill(color string) string {
	return `<a:solidFill>` + srgb(color) + `</a:solidFill>`
}

// theme is the theme part. Its fonts are the fonts of the decks; its
// colours are those of package layout, so that a shape that someone adds
// in PowerPoint takes them as well.
func theme() string {
	font := func(kind string) string {
		return fmt.Sprintf(`<a:%s><a:latin typeface="%s"/><a:ea typeface="%s"/><a:cs typeface=""/><a:font script="Jpan" typeface="%s"/></a:%s>`,
			kind, layout.FontLatin, layout.FontEastAsian, layout.FontEastAsian, kind)
	}
	three := func(s string) string { return s + s + s }
	placeholderFill := `<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>`
	return xmlHeader + `<a:theme xmlns:a="` + nsA + `" name="ims-practice">` +
		`<a:themeElements>` +
		`<a:clrScheme name="ims-practice">` +
		`<a:dk1>` + srgb(layout.ColorText) + `</a:dk1>` +
		`<a:lt1>` + srgb(layout.ColorWhite) + `</a:lt1>` +
		`<a:dk2>` + srgb(layout.ColorAccent) + `</a:dk2>` +
		`<a:lt2>` + srgb(layout.ColorPanelFill) + `</a:lt2>` +
		`<a:accent1>` + srgb(layout.ColorAccent) + `</a:accent1>` +
		`<a:accent2>` + srgb(layout.ColorOchre) + `</a:accent2>` +
		`<a:accent3>` + srgb(layout.ColorGreen) + `</a:accent3>` +
		`<a:accent4>` + srgb(layout.ColorMuted) + `</a:accent4>` +
		`<a:accent5>` + srgb(layout.ColorLifeline) + `</a:accent5>` +
		`<a:accent6>` + srgb(layout.ColorNoteLine) + `</a:accent6>` +
		`<a:hlink>` + srgb(layout.ColorLink) + `</a:hlink>` +
		`<a:folHlink>` + srgb(layout.ColorLink) + `</a:folHlink>` +
		`</a:clrScheme>` +
		`<a:fontScheme name="ims-practice">` + font("majorFont") + font("minorFont") + `</a:fontScheme>` +
		`<a:fmtScheme name="ims-practice">` +
		`<a:fillStyleLst>` + three(placeholderFill) + `</a:fillStyleLst>` +
		`<a:lnStyleLst>` + three(`<a:ln w="9525">`+placeholderFill+`</a:ln>`) + `</a:lnStyleLst>` +
		`<a:effectStyleLst>` + three(`<a:effectStyle><a:effectLst/></a:effectStyle>`) + `</a:effectStyleLst>` +
		`<a:bgFillStyleLst>` + three(placeholderFill) + `</a:bgFillStyleLst>` +
		`</a:fmtScheme>` +
		`</a:themeElements>` +
		`<a:objectDefaults/><a:extraClrSchemeLst/>` +
		`</a:theme>`
}

// placeholder is a placeholder shape of the master or of a layout.
type placeholder struct {
	kind       string // the type of p:ph
	index      int    // 0 for none
	name       string
	x, y, w, h float64
	prompt     string
}

func (p placeholder) xml(id int) string {
	ph := `<p:ph type="` + p.kind + `"`
	if p.index > 0 {
		ph += fmt.Sprintf(` idx="%d"`, p.index)
	}
	ph += `/>`
	body := `<a:p><a:r><a:rPr lang="en-US"/><a:t>` + escape(p.prompt) + `</a:t></a:r></a:p>`
	if p.kind == "sldNum" {
		body = `<a:p><a:pPr algn="r"/><a:fld id="` + slideNumberField + `" type="slidenum"><a:rPr lang="en-US"/><a:t>‹#›</a:t></a:fld></a:p>`
	}
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr>%s</p:nvPr></p:nvSpPr>`+
		`<p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>`+
		`<p:txBody><a:bodyPr lIns="0" tIns="0" rIns="0" bIns="0"/><a:lstStyle/>%s</p:txBody></p:sp>`,
		id, p.name, ph, emu(p.x), emu(p.y), emu(p.w), emu(p.h), body)
}

func spTree(shapes []placeholder) string {
	var b strings.Builder
	b.WriteString(`<p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
		`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`)
	for i, s := range shapes {
		b.WriteString(s.xml(i + 2))
	}
	b.WriteString(`</p:spTree>`)
	return b.String()
}

func background(color string) string {
	return `<p:bg><p:bgPr>` + solidFill(color) + `<a:effectLst/></p:bgPr></p:bg>`
}

var (
	footerPlaceholder = placeholder{kind: "ftr", index: indexFooter, name: "Footer", x: layout.MarginX, y: layout.FooterTop, w: 640, h: layout.FooterHeight, prompt: "Footer"}
	numberPlaceholder = placeholder{kind: "sldNum", index: indexNumber, name: "Slide Number", x: layout.SlideWidth - layout.MarginX - 120, y: layout.FooterTop, w: 120, h: layout.FooterHeight}
	titlePlaceholder  = placeholder{kind: "title", name: "Title", x: layout.MarginX, y: layout.TitleTop, w: layout.SlideWidth - 2*layout.MarginX, h: 34, prompt: "Title"}
)

// level is the paragraph style of one level of the text styles.
func level(n int, size float64, color string, bold bool, bullet string) string {
	attributes := ""
	bulletXML := `<a:buNone/>`
	if bullet != "" {
		indent := emu(size * 1.2)
		attributes = fmt.Sprintf(` marL="%d" indent="%d"`, int64(n)*indent, -indent)
		bulletXML = `<a:buFont typeface="Arial"/><a:buChar char="` + bullet + `"/>`
	}
	b := ""
	if bold {
		b = ` b="1"`
	}
	return fmt.Sprintf(`<a:lvl%dpPr%s><a:lnSpc><a:spcPct val="100000"/></a:lnSpc>%s<a:defRPr sz="%d"%s>%s<a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/></a:defRPr></a:lvl%dpPr>`,
		n, attributes, bulletXML, hundredths(size), b, solidFill(color), n)
}

// slideMaster is the master of every slide: white, with the title at the
// top, the footer and the slide number at the bottom, and the text styles
// of the decks.
func slideMaster() string {
	body := placeholder{kind: "body", index: indexBody, name: "Text", x: layout.MarginX, y: 92, w: layout.SlideWidth - 2*layout.MarginX, h: layout.BodyBottom - 92, prompt: "Text"}
	var ids strings.Builder
	for i := range slideLayouts {
		fmt.Fprintf(&ids, `<p:sldLayoutId id="%d" r:id="rId%d"/>`, 2147483649+i, i+1)
	}
	return xmlHeader + `<p:sldMaster xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `">` +
		`<p:cSld>` + background(layout.ColorWhite) + spTree([]placeholder{titlePlaceholder, body, footerPlaceholder, numberPlaceholder}) + `</p:cSld>` +
		`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
		`<p:sldLayoutIdLst>` + ids.String() + `</p:sldLayoutIdLst>` +
		`<p:txStyles>` +
		`<p:titleStyle>` + level(1, layout.SizeHeading, layout.ColorAccent, true, "") + `</p:titleStyle>` +
		`<p:bodyStyle>` + level(1, layout.SizeBody, layout.ColorText, false, "•") + level(2, layout.SizeBody*0.9, layout.ColorText, false, "–") + `</p:bodyStyle>` +
		`<p:otherStyle>` + level(1, layout.SizeBody, layout.ColorText, false, "") + `</p:otherStyle>` +
		`</p:txStyles>` +
		`</p:sldMaster>`
}

// slideLayout is one of slideLayouts, by its number from 1.
func slideLayout(n int) string {
	l := slideLayouts[n-1]
	const left, width = 80.0, layout.SlideWidth - 160
	shapes := []placeholder{}
	bg := ""
	switch n {
	case layoutTitle:
		shapes = append(shapes,
			placeholder{kind: "ctrTitle", name: "Title", x: left, y: 170, w: width, h: 96, prompt: "Title"},
			placeholder{kind: "subTitle", index: indexBody, name: "Subtitle", x: left, y: 296, w: width, h: 60, prompt: "Subtitle"},
			footerPlaceholder)
	case layoutSection:
		bg = background(layout.ColorAccent)
		shapes = append(shapes,
			placeholder{kind: "title", name: "Title", x: left, y: 200, w: width, h: 48, prompt: "Section"},
			placeholder{kind: "body", index: indexBody, name: "Text", x: left, y: 268, w: width, h: 60, prompt: "Text"},
			footerPlaceholder, numberPlaceholder)
	default:
		shapes = append(shapes, titlePlaceholder, footerPlaceholder, numberPlaceholder)
	}
	return xmlHeader + `<p:sldLayout xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `" type="` + l.kind + `" preserve="1">` +
		`<p:cSld name="` + l.name + `">` + bg + spTree(shapes) + `</p:cSld>` +
		`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>` +
		`</p:sldLayout>`
}

// escape makes text safe in XML content and in attribute values.
func escape(text string) string {
	return xmlEscaper.Replace(text)
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
