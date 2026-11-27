// check-html.js opens every deck that "go run ./cmd/slides build" wrote
// in a headless Chromium, in the check mode of the HTML (?check), and
// fails when the browser drew text larger than its box on any slide. The
// check mode also draws every step of every scene and reports a panel
// whose text does not fit, a node whose name is wider than its box, and
// a label of a message that covers a node or another label. The
// generator measures text with fixed widths; this measures it with the
// fonts that the machine has.
//
// It also fails when the browser drew more than 1% of the glyphs of a
// deck in fonts that the style sheet of the deck does not name: without
// the fonts of the decks the browser falls back to others, and a check
// with those says nothing about the slides. A symbol that none of the
// named fonts has, such as the "⊕" of the VoLTE deck, is drawn from
// another font on any machine; it is listed but does not fail.
//
// Run it in slides/ after the build, with Playwright and its Chromium
// installed (it loads nothing else):
//
//	node check-html.js [dist]
'use strict';

const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

// The elements whose fonts are read: every text box, table cell and
// piece of code, and the text of the scenes: their headings, the text
// of the steps, the fields and the labels of the diagrams.
const textElements = '.box p, td, th, code, .mono, .scene h2, .scene .lead, .step h3, .step p, .fields h4, .fields dt, .fields dd, .diagram text';

async function fontsOf(page, client) {
  const { root } = await client.send('DOM.getDocument', { depth: -1 });
  const { nodeIds } = await client.send('DOM.querySelectorAll', { nodeId: root.nodeId, selector: textElements });
  const used = new Map();
  for (const nodeId of nodeIds) {
    const [{ fonts }, { computedStyle }] = await Promise.all([
      client.send('CSS.getPlatformFontsForNode', { nodeId }),
      client.send('CSS.getComputedStyleForNode', { nodeId }),
    ]);
    const family = computedStyle.find((p) => p.name === 'font-family').value;
    const named = family.split(',').map((f) => f.trim().replace(/^["']|["']$/g, '').toLowerCase());
    for (const font of fonts) {
      const name = font.familyName;
      const entry = used.get(name) || { glyphs: 0, named: false };
      entry.glyphs += font.glyphCount;
      entry.named = entry.named || named.includes(name.toLowerCase());
      used.set(name, entry);
    }
  }
  return used;
}

async function main() {
  const dist = path.resolve(process.argv[2] || 'dist');
  const decks = fs.readdirSync(dist)
    .flatMap((lang) => fs.readdirSync(path.join(dist, lang)).filter((f) => f.endsWith('.html')).map((f) => path.join(dist, lang, f)))
    .sort();
  if (decks.length === 0) {
    console.error(`no deck in ${dist}; run "go run ./cmd/slides build" first`);
    process.exit(2);
  }
  const browser = await chromium.launch();
  let failed = false;
  try {
    for (const file of decks) {
      const page = await browser.newPage({ viewport: { width: 1280, height: 720 } });
      await page.goto('file://' + file + '?check');
      await page.evaluate(() => document.fonts.ready);
      const report = await page.textContent('#check');
      const client = await page.context().newCDPSession(page);
      await client.send('DOM.enable');
      await client.send('CSS.enable');
      const fonts = await fontsOf(page, client);
      const name = path.relative(dist, file);
      const overflow = parseInt(report, 10);
      const unnamed = [...fonts].filter(([, f]) => !f.named);
      const glyphs = [...fonts].reduce((sum, [, f]) => sum + f.glyphs, 0);
      const fallback = unnamed.reduce((sum, [, f]) => sum + f.glyphs, 0);
      const list = [...fonts].map(([n, f]) => `${n} ${f.glyphs}`).join(', ');
      if (Number.isNaN(overflow) || overflow > 0 || fallback > glyphs / 100) {
        failed = true;
      }
      console.log(`${name}: ${report.split('\n')[0]}; fonts: ${list}`);
      for (const line of report.split('\n').slice(1).filter(Boolean)) {
        console.log('  ' + line);
      }
      if (unnamed.length > 0) {
        console.log(`  ${fallback} of ${glyphs} glyphs in fonts that the deck does not name: ` + unnamed.map(([n]) => n).join(', '));
      }
      await page.close();
    }
  } finally {
    await browser.close();
  }
  if (failed) {
    console.log('FAIL');
    process.exit(1);
  }
  console.log(`${decks.length} decks: no box overflows, and the fonts that each deck names drew its text`);
}

main().catch((err) => {
  console.error(err);
  process.exit(2);
});
