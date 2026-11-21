package pptx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// schemaElement is what the schemas of ECMA-376 ask of one element that
// the writer makes. children ranks each child that the element's
// complex type allows by its place in the type's sequence; the elements
// of one choice share a rank. A child that the type does not allow, or
// that comes before one of a higher rank, makes PowerPoint refuse the
// file or offer to repair it.
type schemaElement struct {
	children   map[string]int
	required   []string
	attributes []string
}

func ranks(groups ...[]string) map[string]int {
	m := map[string]int{}
	for rank, group := range groups {
		for _, name := range group {
			m[name] = rank
		}
	}
	return m
}

var (
	fills       = []string{"a:noFill", "a:solidFill", "a:gradFill", "a:blipFill", "a:pattFill", "a:grpFill"}
	effects     = []string{"a:effectLst", "a:effectDag"}
	groupShapes = []string{"p:sp", "p:grpSp", "p:graphicFrame", "p:cxnSp", "p:pic", "p:contentPart"}
	// CT_GroupShape (pml.xsd), the type of p:spTree and p:grpSp.
	groupShape = schemaElement{
		children: ranks([]string{"p:nvGrpSpPr"}, []string{"p:grpSpPr"}, groupShapes, []string{"p:extLst"}),
		required: []string{"p:nvGrpSpPr", "p:grpSpPr"},
	}
	// CT_TextBody (dml-main.xsd), the type of p:txBody and a:txBody.
	textBody = schemaElement{
		children: ranks([]string{"a:bodyPr"}, []string{"a:lstStyle"}, []string{"a:p"}),
		required: []string{"a:bodyPr", "a:p"},
	}
	// CT_TextCharacterProperties (dml-main.xsd), a:rPr and a:endParaRPr.
	characterProperties = schemaElement{children: ranks(
		[]string{"a:ln"}, fills, effects, []string{"a:highlight"}, []string{"a:uLnTx", "a:uLn"}, []string{"a:uFillTx", "a:uFill"},
		[]string{"a:latin"}, []string{"a:ea"}, []string{"a:cs"}, []string{"a:sym"}, []string{"a:hlinkClick"}, []string{"a:hlinkMouseOver"},
		[]string{"a:rtl"}, []string{"a:extLst"})}
	// CT_LineProperties (dml-main.xsd), a:ln and the borders of a cell.
	lineProperties = schemaElement{children: ranks(
		[]string{"a:noFill", "a:solidFill", "a:gradFill", "a:pattFill"}, []string{"a:prstDash", "a:custDash"},
		[]string{"a:round", "a:bevel", "a:miter"}, []string{"a:headEnd"}, []string{"a:tailEnd"}, []string{"a:extLst"})}
	nonVisual = func(properties string) schemaElement {
		return schemaElement{
			children: ranks([]string{"p:cNvPr"}, []string{properties}, []string{"p:nvPr"}),
			required: []string{"p:cNvPr", properties, "p:nvPr"},
		}
	}
	point = schemaElement{attributes: []string{"x", "y"}}
	size  = schemaElement{attributes: []string{"cx", "cy"}}
	font  = schemaElement{attributes: []string{"typeface"}}
)

// schema holds, for each element the writer makes and the package
// needs, its complex type as the transitional schemas of ECMA-376 give
// it: pml.xsd and dml-main.xsd of Part 1 (types named in the comments),
// and opc-relationships.xsd and opc-contentTypes.xsd of Part 2.
var schema = map[string]schemaElement{
	// CT_Presentation, CT_SlideMaster, CT_SlideLayout, CT_Slide.
	"p:presentation": {children: ranks([]string{"p:sldMasterIdLst"}, []string{"p:notesMasterIdLst"}, []string{"p:handoutMasterIdLst"},
		[]string{"p:sldIdLst"}, []string{"p:sldSz"}, []string{"p:notesSz"}, []string{"p:smartTags"}, []string{"p:embeddedFontLst"},
		[]string{"p:custShowLst"}, []string{"p:photoAlbum"}, []string{"p:custDataLst"}, []string{"p:kinsoku"}, []string{"p:defaultTextStyle"},
		[]string{"p:modifyVerifier"}, []string{"p:extLst"}), required: []string{"p:notesSz"}},
	"p:sldMaster": {children: ranks([]string{"p:cSld"}, []string{"p:clrMap"}, []string{"p:sldLayoutIdLst"}, []string{"p:transition"},
		[]string{"p:timing"}, []string{"p:hf"}, []string{"p:txStyles"}, []string{"p:extLst"}), required: []string{"p:cSld", "p:clrMap"}},
	"p:sldLayout": {children: ranks([]string{"p:cSld"}, []string{"p:clrMapOvr"}, []string{"p:transition"}, []string{"p:timing"},
		[]string{"p:hf"}, []string{"p:extLst"}), required: []string{"p:cSld"}},
	"p:sld": {children: ranks([]string{"p:cSld"}, []string{"p:clrMapOvr"}, []string{"p:transition"}, []string{"p:timing"}, []string{"p:extLst"}),
		required: []string{"p:cSld"}},
	// CT_CommonSlideData, CT_Background.
	"p:cSld": {children: ranks([]string{"p:bg"}, []string{"p:spTree"}, []string{"p:custDataLst"}, []string{"p:controls"}, []string{"p:extLst"}),
		required: []string{"p:spTree"}},
	"p:bg":     {children: ranks([]string{"p:bgPr", "p:bgRef"})},
	"p:spTree": groupShape,
	"p:grpSp":  groupShape,
	// CT_Shape, CT_Connector, CT_GraphicalObjectFrame and their
	// non-visual properties.
	"p:sp":               {children: ranks([]string{"p:nvSpPr"}, []string{"p:spPr"}, []string{"p:style"}, []string{"p:txBody"}, []string{"p:extLst"}), required: []string{"p:nvSpPr", "p:spPr"}},
	"p:cxnSp":            {children: ranks([]string{"p:nvCxnSpPr"}, []string{"p:spPr"}, []string{"p:style"}, []string{"p:extLst"}), required: []string{"p:nvCxnSpPr", "p:spPr"}},
	"p:graphicFrame":     {children: ranks([]string{"p:nvGraphicFramePr"}, []string{"p:xfrm"}, []string{"a:graphic"}, []string{"p:extLst"}), required: []string{"p:nvGraphicFramePr", "p:xfrm", "a:graphic"}},
	"p:nvSpPr":           nonVisual("p:cNvSpPr"),
	"p:nvCxnSpPr":        nonVisual("p:cNvCxnSpPr"),
	"p:nvGraphicFramePr": nonVisual("p:cNvGraphicFramePr"),
	"p:nvGrpSpPr":        nonVisual("p:cNvGrpSpPr"),
	"p:cNvPr":            {children: ranks([]string{"a:hlinkClick"}, []string{"a:hlinkHover"}, []string{"a:extLst"}), attributes: []string{"id", "name"}},
	// CT_ShapeProperties, CT_GroupShapeProperties, CT_Transform2D,
	// CT_GroupTransform2D.
	"p:spPr": {children: ranks([]string{"a:xfrm"}, []string{"a:custGeom", "a:prstGeom"}, fills, []string{"a:ln"}, effects,
		[]string{"a:scene3d"}, []string{"a:sp3d"}, []string{"a:extLst"})},
	"p:grpSpPr":     {children: ranks([]string{"a:xfrm"}, fills, effects, []string{"a:scene3d"}, []string{"a:extLst"})},
	"a:xfrm":        {children: ranks([]string{"a:off"}, []string{"a:ext"}, []string{"a:chOff"}, []string{"a:chExt"})},
	"p:xfrm":        {children: ranks([]string{"a:off"}, []string{"a:ext"})},
	"a:off":         point,
	"a:chOff":       point,
	"a:ext":         size,
	"a:chExt":       size,
	"a:prstGeom":    {children: ranks([]string{"a:avLst"}), attributes: []string{"prst"}},
	"a:srgbClr":     {attributes: []string{"val"}},
	"a:ln":          lineProperties,
	"a:lnL":         lineProperties,
	"a:lnR":         lineProperties,
	"a:lnT":         lineProperties,
	"a:lnB":         lineProperties,
	"a:graphic":     {children: ranks([]string{"a:graphicData"}), required: []string{"a:graphicData"}},
	"a:graphicData": {attributes: []string{"uri"}},
	// CT_TextBody, CT_TextBodyProperties, CT_TextParagraph,
	// CT_TextParagraphProperties, the runs and CT_TextFont.
	"p:txBody": textBody,
	"a:txBody": textBody,
	"a:bodyPr": {children: ranks([]string{"a:prstTxWarp"}, []string{"a:noAutofit", "a:normAutofit", "a:spAutoFit"}, []string{"a:scene3d"},
		[]string{"a:sp3d", "a:flatTx"}, []string{"a:extLst"})},
	"a:p": {children: ranks([]string{"a:pPr"}, []string{"a:r", "a:br", "a:fld"}, []string{"a:endParaRPr"})},
	"a:pPr": {children: ranks([]string{"a:lnSpc"}, []string{"a:spcBef"}, []string{"a:spcAft"}, []string{"a:buClrTx", "a:buClr"},
		[]string{"a:buSzTx", "a:buSzPct", "a:buSzPts"}, []string{"a:buFontTx", "a:buFont"}, []string{"a:buNone", "a:buAutoNum", "a:buChar", "a:buBlip"},
		[]string{"a:tabLst"}, []string{"a:defRPr"}, []string{"a:extLst"})},
	"a:spcPts":     {attributes: []string{"val"}},
	"a:buAutoNum":  {attributes: []string{"type"}},
	"a:buChar":     {attributes: []string{"char"}},
	"a:r":          {children: ranks([]string{"a:rPr"}, []string{"a:t"}), required: []string{"a:t"}},
	"a:fld":        {children: ranks([]string{"a:rPr"}, []string{"a:pPr"}, []string{"a:t"}), attributes: []string{"id"}},
	"a:br":         {children: ranks([]string{"a:rPr"})},
	"a:rPr":        characterProperties,
	"a:endParaRPr": characterProperties,
	"a:latin":      font,
	"a:ea":         font,
	"a:cs":         font,
	"a:buFont":     font,
	// CT_Table, CT_TableGrid, CT_TableCol, CT_TableRow, CT_TableCell,
	// CT_TableCellProperties.
	"a:tbl":     {children: ranks([]string{"a:tblPr"}, []string{"a:tblGrid"}, []string{"a:tr"}), required: []string{"a:tblGrid"}},
	"a:tblGrid": {children: ranks([]string{"a:gridCol"})},
	"a:gridCol": {children: ranks([]string{"a:extLst"}), attributes: []string{"w"}},
	"a:tr":      {children: ranks([]string{"a:tc"}, []string{"a:extLst"}), attributes: []string{"h"}},
	"a:tc":      {children: ranks([]string{"a:txBody"}, []string{"a:tcPr"}, []string{"a:extLst"})},
	"a:tcPr": {children: ranks([]string{"a:lnL"}, []string{"a:lnR"}, []string{"a:lnT"}, []string{"a:lnB"}, []string{"a:lnTlToBr"},
		[]string{"a:lnBlToTr"}, []string{"a:cell3D"}, fills, []string{"a:headers"}, []string{"a:extLst"})},
	// CT_SlideIdListEntry, CT_SlideMasterIdListEntry, CT_SlideSize.
	"p:sldId":       {attributes: []string{"id", "r:id"}},
	"p:sldMasterId": {attributes: []string{"r:id"}},
	"p:sldSz":       size,
	"p:notesSz":     size,
	// Part 2: CT_Relationship, CT_Default, CT_Override.
	"Relationship": {attributes: []string{"Id", "Type", "Target"}},
	"Default":      {attributes: []string{"Extension", "ContentType"}},
	"Override":     {attributes: []string{"PartName", "ContentType"}},
}

var prefixes = map[string]string{nsA: "a:", nsP: "p:", nsR: "r:"}

func qualified(n xml.Name) string { return prefixes[n.Space] + n.Local }

// checkSchemaOrder reports each element of a part that breaks schema.
func checkSchemaOrder(name string, body []byte) []string {
	type open struct {
		name     string
		lastRank int
		seen     []string
	}
	var problems []string
	var stack []*open
	decoder := xml.NewDecoder(bytes.NewReader(body))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return append(problems, fmt.Sprintf("%s: %v", name, err))
		}
		switch t := token.(type) {
		case xml.StartElement:
			element := qualified(t.Name)
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				if rule, ok := schema[parent.name]; ok && rule.children != nil {
					rank, allowed := rule.children[element]
					switch {
					case !allowed:
						problems = append(problems, fmt.Sprintf("%s: %s is not allowed in %s", name, element, parent.name))
					case rank < parent.lastRank:
						problems = append(problems, fmt.Sprintf("%s: %s comes after %s in %s", name, element, parent.seen[len(parent.seen)-1], parent.name))
					default:
						parent.lastRank = rank
					}
				}
				parent.seen = append(parent.seen, element)
			}
			if rule, ok := schema[element]; ok {
				for _, a := range rule.attributes {
					if !slices.ContainsFunc(t.Attr, func(attr xml.Attr) bool { return qualified(attr.Name) == a }) {
						problems = append(problems, fmt.Sprintf("%s: %s has no attribute %s", name, element, a))
					}
				}
			}
			stack = append(stack, &open{name: element})
		case xml.EndElement:
			closed := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, child := range schema[closed.name].required {
				if !slices.Contains(closed.seen, child) {
					problems = append(problems, fmt.Sprintf("%s: %s has no %s", name, closed.name, child))
				}
			}
		}
	}
	return problems
}

// decks builds the pptx of the sample and of every deck of the
// repository, by the name of each.
func decks(t *testing.T) map[string]map[string][]byte {
	t.Helper()
	built := map[string]map[string][]byte{"sample": build(t)}
	paths, err := filepath.Glob("../../decks/*/*.md")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no decks: %v", err)
	}
	for _, path := range paths {
		d, err := deck.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		pages, _ := layout.Deck(d)
		var out bytes.Buffer
		if err := Write(&out, d, pages); err != nil {
			t.Fatal(err)
		}
		z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
		if err != nil {
			t.Fatal(err)
		}
		parts := map[string][]byte{}
		for _, f := range z.File {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			if parts[f.Name], err = io.ReadAll(r); err != nil {
				t.Fatal(err)
			}
		}
		built[path] = parts
	}
	return built
}

// TestPartsFollowTheSchemas checks every part of every deck against
// the order of the children, the required children and the required
// attributes of schema.
func TestPartsFollowTheSchemas(t *testing.T) {
	for path, parts := range decks(t) {
		for name, body := range parts {
			for _, p := range checkSchemaOrder(name, body) {
				t.Errorf("%s: %s", path, p)
			}
		}
	}
}

func TestSchemaOrderFindsMistakes(t *testing.T) {
	bad := `<p:sp xmlns:a="` + nsA + `" xmlns:p="` + nsP + `"><p:spPr><a:prstGeom/><a:xfrm><a:off x="0"/></a:xfrm></p:spPr>` +
		`<p:txBody><a:lstStyle/><a:p><a:r><a:t>x</a:t><a:rPr/></a:r><a:pic/></a:p></p:txBody></p:sp>`
	got := strings.Join(checkSchemaOrder("bad", []byte(bad)), "\n")
	for _, want := range []string{
		"a:xfrm comes after a:prstGeom in p:spPr",
		"a:prstGeom has no attribute prst",
		"a:off has no attribute y",
		"a:rPr comes after a:t in a:r",
		"a:pic is not allowed in a:p",
		"p:txBody has no a:bodyPr",
		"p:sp has no p:nvSpPr",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}
