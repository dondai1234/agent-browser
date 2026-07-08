#!/usr/bin/env node
// Regenerate the README diagram PNGs from their SVG vector sources.
//
// GitHub refuses to inline SVG via <img> in a README (a security restriction),
// so the diagrams are shipped as PNG and referenced in the README. The .svg
// files in docs/img/ are the editable source; this script rasterizes them to
// PNG at 2x the README display width for retina crispness.
//
// Run after editing an SVG:
//   npm install -g sharp   # one-time, if sharp isn't resolvable
//   node scripts/rasterize-diagrams.cjs
const sharp = require('sharp');
const fs = require('fs');
const path = require('path');

const dir = 'docs/img';
// [svgFile, displayWidthInReadme]
const jobs = [
  ['flow.svg', 780],
  ['tokens.svg', 760],
  ['cognition.svg', 820],
];

(async () => {
  for (const [file, displayW] of jobs) {
    const src = path.join(dir, file);
    const out = path.join(dir, file.replace(/\.svg$/, '.png'));
    const buf = fs.readFileSync(src);
    const natW = (await sharp(buf).metadata()).width;
    const targetW = displayW * 2;                       // 2x for retina
    const density = Math.round((targetW / natW) * 72);  // crisp internal raster
    await sharp(buf, { density })
      .resize(targetW, null, { fit: 'outside' })
      .png()
      .toFile(out);
    console.log(`${file} -> ${path.basename(out)}  ${targetW}px  ${fs.statSync(out).size} bytes`);
  }
  console.log('done');
})().catch(e => { console.error(e); process.exit(1); });
