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
// Then it steps through each deck that has scenes: forward from the
// first slide to the last step, back to the first, and once more by
// opening the address of every state, in another order. Each state has
// to look the same each way: the same events drawn and in the same
// state, the same nodes in focus, the same text and fields, the same
// view. It also fails when the page asks the network for anything.
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

// stateOf describes what the current slide shows, for comparing the
// ways of reaching a state.
function stateOf() {
  // The page runs this function alone, so it holds its own helpers.
  const box = (e) => {
    const r = e.getBoundingClientRect();
    return Math.round(r.width * 10) / 10 + 'x' + Math.round(r.height * 10) / 10;
  };
  const s = document.querySelector('#stage > .slide:not([hidden])');
  const svg = s.querySelector('svg.diagram');
  const shown = (list) => Array.from(list).filter((e) => !e.hidden).map((e) => e.dataset.step || e.dataset.id).join(',');
  return [
    location.hash,
    Array.from(s.querySelectorAll('.event')).map((g) => g.dataset.id + ':' + g.dataset.state).join(','),
    Array.from(s.querySelectorAll('.world .node')).map((g) => g.dataset.id + ':' + g.dataset.state).join(','),
    shown(s.querySelectorAll('.panel .step')),
    shown(s.querySelectorAll('.panel .detail .fields')),
    svg ? svg.getAttribute('viewBox') : '',
    // The height that the narrow layout gives the diagram comes from
    // --aspect, so it has to follow the view as well.
    svg ? svg.style.getPropertyValue('--aspect') + ' ' + box(svg) : '',
  ].join('|');
}

// steps walks a deck forward, back and by address, and returns its
// problems.
async function steps(browser, file, viewport) {
  const context = await browser.newContext({ viewport, reducedMotion: 'reduce' });
  const problems = [];
  context.on('request', (r) => {
    if (!r.url().startsWith('file://')) problems.push('asks the network for ' + r.url());
  });
  const page = await context.newPage();
  page.on('pageerror', (e) => problems.push('script error: ' + e));
  await page.goto('file://' + file);
  const scenes = await page.evaluate(() => document.querySelectorAll('#stage > .scene').length);
  if (scenes === 0) {
    await context.close();
    return { states: 0, problems };
  }
  const forward = [];
  for (;;) {
    forward.push(await page.evaluate(stateOf));
    if (await page.evaluate(() => document.getElementById('next').disabled)) break;
    await page.keyboard.press('ArrowRight');
  }
  for (let i = forward.length - 1; i >= 0; i--) {
    const state = await page.evaluate(stateOf);
    if (state !== forward[i]) problems.push('stepping back to ' + forward[i].split('|')[0] + ' shows another state');
    await page.keyboard.press('ArrowLeft');
  }
  // Every seventh state from the end, round the deck: an order that is
  // neither forward nor back.
  const order = forward.map((_, i) => (i * 7) % forward.length).filter((v, i, a) => a.indexOf(v) === i);
  for (const i of order) {
    await page.goto('file://' + file + forward[i].split('|')[0]);
    // Only the address changed, so the page has not loaded again: a link
    // or a reload loads it from the start.
    await page.reload();
    if ((await page.evaluate(stateOf)) !== forward[i]) problems.push('opening ' + forward[i].split('|')[0] + ' shows another state than stepping to it');
  }
  await page.goto('file://' + file + '#no-such-scene/no-step');
  if (!(await page.evaluate(stateOf)).startsWith(forward[0].split('|')[0])) problems.push('an unknown address does not open the first slide');
  await context.close();
  return { states: forward.length, problems };
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
      // A wide window, and a phone, where the diagram takes the width
      // of the screen and its height comes from --aspect.
      for (const [label, viewport] of [['wide', { width: 1280, height: 720 }], ['narrow', { width: 390, height: 844 }]]) {
        const walk = await steps(browser, file, viewport);
        if (walk.states > 0) {
          console.log(`  ${walk.states} states on a ${label} window, the same forward, back and by address`.replace('the same', walk.problems.length ? 'not all the same' : 'the same'));
        }
        for (const p of walk.problems) {
          console.log(`  (${label}) ${p}`);
          failed = true;
        }
      }
    }
  } finally {
    await browser.close();
  }
  if (failed) {
    console.log('FAIL');
    process.exit(1);
  }
  console.log(`${decks.length} decks: no box overflows, the fonts that each deck names drew its text, every step of every scene looks the same however it is reached, and nothing is asked of the network`);
}

main().catch((err) => {
  console.error(err);
  process.exit(2);
});
