package html

import (
	"fmt"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// The stage is 1280 by 720 CSS pixels before it is scaled to the window,
// 16:9 as the pptx. A slide of blocks is laid out at 960 by 540 points
// and drawn at 4/3 of that, so that it fills the stage; a point of the
// layout is then 4/3 of a pixel of the stage, as a point is 4/3 of a CSS
// pixel. The diagrams have units of their own, which the SVG scales to
// its box.
const (
	stageWidth  = 1280.0
	stageHeight = 720.0
)

// style is the style sheet of a deck. The sizes and the colours of the
// boxes of a slide of blocks are on the boxes, from the layout; the
// sheet holds what they share, and the look of the scenes.
func style() string {
	families := ""
	for _, f := range layout.Families {
		families += fmt.Sprintf(".event.%[1]s line, .legend .%[1]s line { stroke: #%[2]s; }\n", f, layout.FamilyColors[f])
	}
	r := strings.NewReplacer(
		"$SANS", layout.CSSSans, "$MONO", layout.CSSMono,
		"$TEXT", "#"+layout.ColorText, "$MUTED", "#"+layout.ColorMuted, "$ACCENT", "#"+layout.ColorAccent,
		"$LINK", "#"+layout.ColorLink, "$RULE", "#"+layout.ColorRule, "$HEADFILL", "#"+layout.ColorHeaderFill,
		"$CODEFILL", "#"+layout.ColorCodeFill, "$CODELINE", "#"+layout.ColorCodeLine,
		"$STAGEFILL", "#"+layout.ColorStage, "$INK", "#"+layout.ColorInk,
		"$NODEFILL", "#"+layout.ColorNodeFill, "$NODELINE", "#"+layout.ColorNodeLine,
		"$FOCUSFILL", "#"+layout.ColorFocusFill, "$FOCUSLINE", "#"+layout.ColorFocusLine,
		"$GROUPLINE", "#"+layout.ColorGroupLine, "$PROCFILL", "#"+layout.ColorProcessFill, "$PROCLINE", "#"+layout.ColorProcessLine,
		"$PAGEW", px(layout.SlideWidth), "$PAGEH", px(layout.SlideHeight),
		"$PAGESCALE", fmt.Sprintf("%.6f", stageWidth/layout.SlideWidth),
		"$CODESCALE", fmt.Sprintf("%gem", layout.CodeScale),
		"$FAMILIES", families,
	)
	return r.Replace(css)
}

const css = `:root { color-scheme: light; }
html, body { margin: 0; height: 100%; }
body { font-family: $SANS; color: $TEXT; background: #22272b; }
[hidden] { display: none !important; }
.sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
.skip { position: absolute; left: 8px; top: -40px; background: #fff; color: $INK; padding: 6px 10px; z-index: 20; }
.skip:focus { top: 8px; }
a { color: $LINK; }
code, .mono, kbd { font-family: $MONO; }
:focus-visible { outline: 3px solid #f0b429; outline-offset: 2px; }

/* The app: the stage over the bar of controls. */
#app { position: fixed; inset: 0; display: flex; flex-direction: column; }
#viewport { position: relative; flex: 1; overflow: hidden; }
#stage { position: absolute; left: 50%; top: 50%; width: 1280px; height: 720px;
  transform: translate(-50%, -50%) scale(var(--scale, 1)); background: #fff; }
.slide { position: absolute; inset: 0; overflow: hidden; background: #fff; }

/* A slide of blocks: the boxes of the layout, scaled to the stage. */
.page .fixed { position: absolute; left: 0; top: 0; width: $PAGEW; height: $PAGEH; overflow: hidden;
  transform: scale($PAGESCALE); transform-origin: 0 0; }
.page .flow { display: none; }
.box, .rect, .line, .fixed table { position: absolute; box-sizing: border-box; margin: 0; }
.box { display: flex; flex-direction: column; }
.box.middle { justify-content: center; }
.box.bottom { justify-content: flex-end; }
.box p { position: relative; margin: 0; overflow-wrap: anywhere; }
.box.nowrap p { white-space: pre; overflow-wrap: normal; }
.marker { position: absolute; top: 0; }
.marker.bullet { color: $ACCENT; }
/* Smaller inline code would move the baseline and make the line taller. */
.box p code, .fixed td code, .fixed th code { font-size: $CODESCALE; line-height: 0; }
.line { overflow: visible; }
.fixed table { border-collapse: collapse; table-layout: fixed; }
.fixed th, .fixed td { vertical-align: top; overflow-wrap: anywhere; border-bottom: 1px solid $RULE; }
.fixed th { background: $HEADFILL; border-bottom: 1.5px solid $ACCENT; text-align: left; }

/* A scene: the question, the diagram and the panel of the step. */
.scene { display: grid; grid-template-columns: minmax(0, 1fr) 372px; grid-template-rows: auto minmax(0, 1fr);
  column-gap: 30px; padding: 26px 44px 18px; box-sizing: border-box; }
.scene .head { grid-column: 1 / -1; margin-bottom: 14px; }
.kicker { margin: 0 0 4px; font: 13px/1.2 $MONO; letter-spacing: .08em; color: $MUTED; }
.scene h2 { margin: 0; font-size: 33px; line-height: 1.22; color: $INK; }
.lead { margin: 6px 0 0; font-size: 18px; line-height: 1.45; color: $MUTED; }
.figure { display: flex; flex-direction: column; min-height: 0; position: relative; }
.diagram { flex: 1; width: 100%; min-height: 0; background: $STAGEFILL; border-radius: 14px; display: block; }
.under { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; padding-top: 8px; }
.caption { margin: 0; font-size: 12.5px; line-height: 1.4; color: $MUTED; }
.legend { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 4px 14px; margin: 0; padding: 0; list-style: none;
  font-size: 12.5px; color: $MUTED; white-space: nowrap; }
.legend svg { width: 26px; height: 10px; margin-right: 5px; vertical-align: middle; overflow: visible; }
.legend line { stroke-width: 2.4; }
.legend .diameter line { stroke-dasharray: 6 3; }
.legend .dns line, .legend .relation line { stroke-dasharray: 2 3; }
.legend .process line { stroke-width: 8; stroke: $PROCFILL !important; }

.panel { display: flex; flex-direction: column; min-height: 0; overflow: auto; padding-right: 4px; }
.where { margin: 0 0 10px; font: 13px/1.2 $MONO; letter-spacing: .06em; color: $MUTED; }
.step h3 { margin: 0 0 8px; font-size: 21px; line-height: 1.35; color: $INK; }
.step .mark { color: $ACCENT; }
.step p { margin: 0 0 10px; font-size: 17px; line-height: 1.6; }
.fields { margin-top: 6px; padding: 10px 12px; border-radius: 10px; background: #f7f8fa; border: 1px solid $RULE; }
.fields h4 { margin: 0 0 6px; font-size: 14px; line-height: 1.3; color: $INK; }
.fields dl { display: grid; grid-template-columns: fit-content(46%) minmax(0, 1fr); gap: 4px 12px; margin: 0; }
.fields dt { font: 12.5px/1.45 $MONO; color: $MUTED; overflow-wrap: anywhere; }
.fields dd { margin: 0; font-size: 14px; line-height: 1.45; overflow-wrap: anywhere; }
.fields + .fields { margin-top: 8px; }
.minimap { position: absolute; right: 10px; top: 10px; width: 200px; height: auto; background: #fff; border: 1px solid $RULE;
  border-radius: 8px; box-shadow: 0 2px 8px rgba(0, 0, 0, .08); }
.minimap .cam { fill: rgba(31, 78, 121, .08); stroke: $ACCENT; stroke-width: 6; }
.minimap .label, .minimap .sub, .minimap .pill, .minimap .role, .minimap .group text { display: none; }
/* A wide map: the diagram across the stage, the step below it. */
.scene.wide { grid-template-columns: minmax(0, 1fr); grid-template-rows: auto minmax(0, 1fr) auto; }
.scene.wide .panel { display: grid; grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr); column-gap: 30px; align-content: start;
  height: 216px; margin-top: 12px; overflow: auto; }
.scene.wide .panel > * { grid-column: 1; }
.scene.wide .panel > .detail { grid-column: 2; grid-row: 1 / span 20; }
.scene.wide .detail .fields:first-child { margin-top: 0; }
.scene.wide .step h3 { font-size: 20px; margin-bottom: 4px; }
.scene.wide .step p { font-size: 16.5px; margin-bottom: 6px; }
/* A wide map keeps the whole map in sight around a close-up. */
.scene.wide .minimap { display: none; }
.scene.wide .more { margin-top: 4px; justify-self: start; }
.more { margin-top: auto; align-self: flex-start; font: inherit; font-size: 15px; padding: 8px 14px; border-radius: 8px;
  border: 1px solid $ACCENT; background: #fff; color: $ACCENT; cursor: pointer; }
.more kbd { font-size: 12px; color: $MUTED; }
.panel > .more:first-child { margin-top: 0; }

/* The shapes of the diagrams. */
.group rect { fill: #efeee8; stroke: $GROUPLINE; stroke-width: 1.5; stroke-dasharray: 7 5; }
.group text { font-size: 13px; fill: $MUTED; }
.lifeline { stroke: #c9d0d6; stroke-width: 1.5; }
.node rect { fill: $NODEFILL; stroke: $NODELINE; stroke-width: 1.5; }
.node .name { font-size: 19px; font-weight: 700; text-anchor: middle; fill: $INK; }
.view-ladder .node .name { font-size: 16px; }
.node .role { font-size: 12.5px; text-anchor: middle; fill: $MUTED; }
.node[data-state=focus] rect { fill: $FOCUSFILL; stroke: $FOCUSLINE; stroke-width: 2.5; }
.node[data-state=dim] { opacity: .45; }
.heads .node rect { fill: #fff; }
.heads .band { fill: $STAGEFILL; }
.event.clipped .pill, .event.clipped text { visibility: hidden; }
.heads .node[data-state=focus] rect { fill: $FOCUSFILL; }
.event { transition: opacity .35s ease; }
.event[data-state=off] { opacity: 0; visibility: hidden; }
.event[data-state=done] { opacity: .55; }
.event line { stroke-width: 2.2; }
.event[data-state=active] line { stroke-width: 3.6; }
.event.diameter line { stroke-dasharray: 9 5; }
.event.dns line { stroke-dasharray: 2 5; stroke-linecap: round; }
.event.relation line { stroke-dasharray: 6 6; }
$FAMILIES.event .pill { fill: $STAGEFILL; opacity: .94; }
.event[data-state=active] .pill { fill: #fff; stroke: #d5dadf; }
.event .label { font-size: 15px; text-anchor: middle; fill: $INK; }
.event[data-state=active] .label { font-weight: 700; }
.event .sub { font-size: 11.5px; font-weight: 400; fill: $MUTED; text-anchor: middle; }
.event.process rect { fill: $PROCFILL; stroke: $PROCLINE; stroke-width: 1.3; }
.event.process[data-state=active] rect { stroke-width: 2.6; }
.traveller { fill: $INK; }

/* The bar of controls. */
#bar { display: flex; align-items: center; gap: 8px; padding: 8px 12px; background: #1b1f22; color: #e8eaec; flex: none;
  font-size: 14px; min-height: 40px; flex-wrap: wrap; }
#bar button, #bar select, #bar a { font: inherit; color: #e8eaec; background: #2c3237; border: 1px solid #3d454c; border-radius: 6px;
  padding: 6px 10px; cursor: pointer; text-decoration: none; }
#bar select { max-width: 34ch; }
#bar button[aria-pressed=true] { background: #f0b429; color: #1b1f22; border-color: #f0b429; }
#bar .spacer { flex: 1; }
#steps { display: flex; gap: 4px; margin: 0; padding: 0; list-style: none; flex-wrap: wrap; }
#steps button { min-width: 30px; padding: 5px 8px; }
#steps button[aria-current=step] { background: #e8eaec; color: #1b1f22; }

/* The drawer with the longer explanation of a scene. */
#drawer { position: fixed; top: 0; right: 0; bottom: 0; width: min(620px, 94vw); background: #fff; z-index: 10;
  box-shadow: -10px 0 30px rgba(0, 0, 0, .25); display: flex; flex-direction: column; }
.drawer-head { display: flex; align-items: flex-start; gap: 12px; padding: 18px 22px 10px; border-bottom: 1px solid $RULE; }
.drawer-head h2 { flex: 1; margin: 0; font-size: 20px; line-height: 1.35; color: $INK; }
#drawer-close { font-size: 24px; line-height: 1; border: 0; background: none; cursor: pointer; color: $MUTED; padding: 0 4px; }
#drawer-body { overflow: auto; padding: 8px 22px 28px; }

/* Flowing text: the drawer, the reading view, a narrow screen. */
.flow, #drawer-body, #reading { font-size: 16.5px; line-height: 1.65; }
.flow p, #drawer-body p, #reading p { margin: .5em 0; }
.flow ul, .flow ol, #drawer-body ul, #drawer-body ol, #reading ul, #reading ol { padding-left: 1.5em; margin: .5em 0; }
.flow .table, #drawer-body .table, #reading .table { overflow-x: auto; margin: .8em 0; }
.flow table, #drawer-body table, #reading table { border-collapse: collapse; width: 100%; font-size: 15px; line-height: 1.5; }
.flow th, .flow td, #drawer-body th, #drawer-body td, #reading th, #reading td { border-bottom: 1px solid $RULE; padding: 5px 8px; vertical-align: top; text-align: left; overflow-wrap: anywhere; }
.flow th, #drawer-body th, #reading th { background: $HEADFILL; border-bottom: 1.5px solid $ACCENT; }
.flow pre, #drawer-body pre, #reading pre { background: $CODEFILL; border: 1px solid $CODELINE; border-radius: 6px; padding: 10px 12px;
  overflow-x: auto; font-size: 14px; line-height: 1.5; }
.flow code, #drawer-body code, #reading code { font-size: .92em; }
.flow blockquote, #drawer-body blockquote, #reading blockquote { margin: .8em 0; padding: 2px 0 2px 14px; border-left: 4px solid $ACCENT; color: $MUTED; font-size: 15px; }
.ladder svg, .shapes { width: 100%; height: auto; }

/* The reading view, which is also what is printed. */
#reading { background: #fff; color: $TEXT; max-width: 960px; margin: 0 auto; padding: 24px 28px 60px; box-sizing: border-box; min-height: 100%; }
body.reading { background: #fff; overflow: auto; }
body.reading #app { display: none; }
.reading-head { display: flex; align-items: baseline; gap: 16px; border-bottom: 2px solid $ACCENT; margin-bottom: 18px; }
.reading-head h1 { flex: 1; font-size: 28px; margin: 8px 0; color: $INK; }
.reading-head button { font: inherit; padding: 6px 12px; }
.read { padding: 18px 0 10px; border-bottom: 1px solid $RULE; }
.read h2 { margin: 0 0 6px; font-size: 26px; line-height: 1.3; color: $INK; }
.read h3.details { font-size: 18px; margin: 1.2em 0 .2em; color: $ACCENT; }
.checkpoint { margin: 14px 0 6px; break-inside: avoid; }
.checkpoint svg { width: 100%; height: auto; max-height: 70vh; }
.checkpoint figcaption { font-size: 12.5px; color: $MUTED; }
.read-steps { padding-left: 1.6em; }
.read-steps li p { margin: .2em 0 .6em; }
.read .fields { margin: 8px 0; break-inside: avoid; }

/* A narrow screen: the slides flow, nothing is scaled down. */
body.narrow #app { position: static; display: block; }
body.narrow { background: #fff; }
body.narrow #viewport { overflow: visible; }
body.narrow #stage { position: static; transform: none; width: auto; height: auto; }
body.narrow .slide { position: static; }
body.narrow .page .fixed { display: none; }
body.narrow .page .flow { display: block; padding: 16px; }
body.narrow .flow h2 { font-size: 24px; line-height: 1.3; margin: 0 0 8px; }
body.narrow .scene { display: block; padding: 16px; }
body.narrow .scene h2 { font-size: 24px; }
body.narrow .diagram { height: auto; aspect-ratio: var(--aspect, 1.6); }
body.narrow .under { flex-direction: column; }
body.narrow .legend { justify-content: flex-start; }
body.narrow .panel, body.narrow .scene.wide .panel { display: block; height: auto; overflow: visible; margin-top: 14px; }
body.narrow .detail { margin-top: 10px; }
body.narrow .minimap { display: none; }
body.narrow #bar { position: sticky; bottom: 0; }
body.narrow #bar select { max-width: 100%; flex: 1 1 100%; order: -1; }

/* Less motion: every change shows at once. */
html.still *, html.still *::before, html.still *::after { transition: none !important; animation: none !important; }

/* The check mode (?check) shows every slide at its own size. */
body.check #app { position: static; display: block; }
body.check #viewport { overflow: visible; }
body.check #stage { position: static; transform: none; background: none; height: auto; }
body.check .slide { position: relative; display: grid; height: 720px; margin-bottom: 8px; }
body.check .page { display: block; }
body.check #bar { display: none; }
#check { white-space: pre-wrap; background: #fff; padding: 8px; }

@page { margin: 12mm; }
@media print {
  html, body { height: auto; background: #fff; }
  #app, #check { display: none !important; }
  #reading { display: block !important; max-width: none; padding: 0; }
  .reading-head button { display: none; }
  .read { break-inside: auto; }
  a { color: inherit; }
  * { print-color-adjust: exact; -webkit-print-color-adjust: exact; }
}
`
