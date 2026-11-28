// book-images.js draws the figures of the book for Zenn: Zenn takes
// PNG, not SVG, so "go run ./cmd/slides zenn" writes each figure as an
// SVG under images/<book>/ and points the chapters at a PNG of the same
// name, and this draws those PNGs, at twice the size of the SVG so that
// they stay sharp on a dense screen. It draws the cover of each book,
// books/<book>/cover.png, from cover.svg in the same way, at 500 × 700.
// It then removes the SVGs, which Zenn does not read.
//
// Run it in slides/ after the export, with Playwright and its Chromium
// installed (it loads nothing else):
//
//	node book-images.js [dist/zenn]
'use strict';

const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

// Zenn refuses an image larger than this.
const limit = 3 * 1024 * 1024;

function svgs(dir) {
  if (!fs.existsSync(dir)) return [];
  return fs.readdirSync(dir, { recursive: true })
    .filter((f) => f.endsWith('.svg'))
    .map((f) => path.join(dir, f));
}

async function main() {
  const out = process.argv[2] || 'dist/zenn';
  const figures = svgs(path.join(out, 'images'));
  const covers = svgs(path.join(out, 'books')).filter((f) => path.basename(f) === 'cover.svg');
  if (figures.length + covers.length === 0) {
    throw new Error(`no SVG under ${out}; run "go run ./cmd/slides zenn" first`);
  }
  const browser = await chromium.launch();
  const failures = [];
  for (const [files, scale] of [[figures, 2], [covers, 1]]) {
    const page = await browser.newPage({ deviceScaleFactor: scale });
    page.on('request', (r) => {
      if (!r.url().startsWith('file:')) failures.push(`${r.url()}: a figure asks the network for something`);
    });
    for (const file of files) {
      const svg = fs.readFileSync(file, 'utf8');
      const width = Number((svg.match(/<svg[^>]* width="([0-9.]+)"/) || [])[1]);
      const height = Number((svg.match(/<svg[^>]* height="([0-9.]+)"/) || [])[1]);
      if (!width || !height) {
        failures.push(`${file}: the SVG has no width and height`);
        continue;
      }
      await page.setViewportSize({ width: Math.ceil(width), height: Math.ceil(height) });
      await page.goto('file://' + path.resolve(file));
      const png = file.replace(/\.svg$/, '.png');
      await page.screenshot({ path: png, clip: { x: 0, y: 0, width, height } });
      if (fs.statSync(png).size > limit) failures.push(`${png}: larger than the 3 MB that Zenn takes`);
      fs.unlinkSync(file);
      console.log(png);
    }
    await page.close();
  }
  await browser.close();
  if (failures.length > 0) {
    console.error(failures.join('\n'));
    process.exit(1);
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
