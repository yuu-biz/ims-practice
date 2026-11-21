package html

import (
	"fmt"

	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// style is the style sheet of a deck. The sizes and the colours of each
// element are on the element itself, from the layout; the sheet holds
// only what every element shares.
func style() string {
	return fmt.Sprintf(`html, body { margin: 0; height: 100%%; background: #2b2f33; }
body { font-family: %[1]s; color: #%[3]s; }
main { position: relative; width: 100%%; height: 100%%; overflow: hidden; }
.slide { position: absolute; left: 50%%; top: 50%%; width: %[4]s; height: %[5]s; overflow: hidden;
  transform: translate(-50%%, -50%%) scale(var(--scale, 1)); display: none; }
.slide.current { display: block; }
.box, .rect, .line, table { position: absolute; box-sizing: border-box; margin: 0; }
.box { display: flex; flex-direction: column; }
.box.middle { justify-content: center; }
.box.bottom { justify-content: flex-end; }
.box p { position: relative; margin: 0; overflow-wrap: anywhere; }
.box.nowrap p { white-space: pre; overflow-wrap: normal; }
.marker { position: absolute; top: 0; }
.marker.bullet { color: #%[6]s; }
.mono, code { font-family: %[2]s; }
/* Smaller inline code would move the baseline and make the line taller. */
.box p code, td code, th code { font-size: %[10]s; line-height: 0; }
a { color: #%[7]s; }
.line { overflow: visible; }
table { border-collapse: collapse; table-layout: fixed; }
th, td { vertical-align: top; overflow-wrap: anywhere; border-bottom: 1px solid #%[8]s; }
th { background: #%[9]s; border-bottom: 1.5px solid #%[6]s; text-align: left; }
@page { size: 10in 5.625in; margin: 0; }
@media print {
  html, body, main { height: auto; overflow: visible; background: none; }
  .slide { display: block; position: relative; left: 0; top: 0; transform: none; break-after: page;
    print-color-adjust: exact; -webkit-print-color-adjust: exact; }
}
/* The check mode (?check) lays out every slide at its own size. */
.all main { height: auto; overflow: visible; }
.all .slide { display: block; position: relative; left: 0; top: 0; transform: none; margin-bottom: 8px; }
`, layout.CSSSans, layout.CSSMono, layout.ColorText, px(layout.SlideWidth), px(layout.SlideHeight),
		layout.ColorAccent, layout.ColorLink, layout.ColorRule, layout.ColorHeaderFill,
		fmt.Sprintf("%gem", layout.CodeScale))
}

// script moves between the slides and keeps the number of the slide in
// the address, so that a reload or a link opens the same slide. With
// "?check" in the address it shows every slide and lists the boxes whose
// text the browser drew taller or wider than the box; a browser can then
// confirm the measure of the build with its own fonts.
const script = `(function () {
  const slides = Array.from(document.querySelectorAll('.slide'));
  if (location.search.includes('check')) {
    document.body.classList.add('all');
    const report = [];
    for (const slide of slides) {
      for (const box of slide.querySelectorAll('.box')) {
        let height = 0, width = 0;
        for (const p of box.children) {
          height += p.offsetHeight + parseFloat(getComputedStyle(p).marginTop);
          width = Math.max(width, p.scrollWidth);
        }
        const room = box.clientHeight - 2 * parseFloat(getComputedStyle(box).paddingTop);
        const across = box.clientWidth - 2 * parseFloat(getComputedStyle(box).paddingLeft);
        if (height > room + 1 || width > across + 1) {
          report.push('slide ' + slide.id + ': "' + box.textContent.slice(0, 40) + '" needs ' +
            Math.ceil(width) + 'x' + Math.ceil(height) + ' in ' + Math.floor(across) + 'x' + Math.floor(room));
        }
      }
      for (const table of slide.querySelectorAll('table')) {
        const planned = Array.from(table.rows).reduce((sum, row) => sum + parseFloat(row.style.height), 0);
        // Each row adds the line below it.
        if (table.offsetHeight > planned + table.rows.length + 1) {
          report.push('slide ' + slide.id + ': a table needs ' + table.offsetHeight + ' of ' + planned);
        }
      }
    }
    const out = document.createElement('pre');
    out.id = 'check';
    out.textContent = report.length + ' boxes overflow\n' + report.join('\n');
    document.body.appendChild(out);
    return;
  }
  let current = 0;
  function fit() {
    const size = getComputedStyle(slides[0]);
    const scale = Math.min(innerWidth / parseFloat(size.width), innerHeight / parseFloat(size.height));
    document.documentElement.style.setProperty('--scale', scale);
  }
  function show(n) {
    current = Math.max(0, Math.min(slides.length - 1, n));
    slides.forEach((slide, i) => slide.classList.toggle('current', i === current));
    history.replaceState(null, '', '#' + slides[current].id);
  }
  addEventListener('resize', fit);
  addEventListener('hashchange', () => show(slides.findIndex((s) => '#' + s.id === location.hash)));
  addEventListener('keydown', (event) => {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    const keys = {
      ArrowRight: current + 1, ArrowDown: current + 1, PageDown: current + 1, ' ': current + 1, Enter: current + 1,
      ArrowLeft: current - 1, ArrowUp: current - 1, PageUp: current - 1, Backspace: current - 1,
      Home: 0, End: slides.length - 1,
    };
    if (event.key in keys) {
      event.preventDefault();
      show(keys[event.key]);
    } else if (event.key === 'f') {
      if (document.fullscreenElement) document.exitFullscreen(); else document.documentElement.requestFullscreen();
    }
  });
  fit();
  show(Math.max(0, slides.findIndex((s) => '#' + s.id === location.hash)));
})();
`
