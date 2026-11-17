/* Placement playhead in a non-4/4 song. */
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const fixture = `// cicada-placement-meter-test: disposable fixture
cicada 2
title "Placement meter"
meter 3/4
tempo 60
track bass acid {}
pattern pulse { 1 . 5 . }
arrange {
  place one bass pulse { at = @1.1.1 length = 5bars }
}
`;
if (process.argv[2] === '--write-fixture') {
  fs.writeFileSync(process.argv[3], fixture, {flag:'wx'});
  process.exit(0);
}
async function until(predicate, message) {
  const end = Date.now() + 15000;
  while (Date.now() < end) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  assert.fail(message);
}
test('placement playhead follows 3/4 transport bars', {timeout:60000}, async t => {
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(base.hostname, '127.0.0.1');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close());
  const page = await browser.newPage({viewport:{width:800,height:1000}});
  await page.goto(new URL('/studio',base).href);
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
  await page.locator('#view-tab-arrange').click();
  await page.locator('[data-placement-navigation][data-placement-navigation-ready]').waitFor();
  const scroller = page.locator('[data-placement-navigation] .placement-scroll');
  await page.locator('#transport-form button[value=play]').click();
  // The transport and the page are read at different moments, so the song
  // position is sampled on both sides of the page read and the playhead must
  // lie between the two. A pair that wrapped, or that took too long to tell a
  // 3/4 playhead from a 4/4 one, is taken again. The song is five 3/4 bars of
  // 2880 ticks; from bar 3 on, a playhead that assumed 4/4 is off by a tenth
  // of the song or more.
  const position = async () => {
    const t = await (await page.request.get(new URL('/api/transport',base).href)).json();
    if (!t.frames?.transport) return null;
    const frame = Buffer.from(t.frames.transport,'base64');
    if (frame.length!==64) return null;
    const bar=frame.readInt32LE(52), beat=frame.readInt32LE(56), tick=frame.readInt32LE(60);
    return {bar, fraction:((bar-1)+(beat-1)/3+tick/2880)/5};
  };
  let last = 'no sample';
  for (const targetBar of [3, 5]) for (const end = Date.now()+30000;;) {
    assert.ok(Date.now() < end, `no pair of transport samples bracketed the playhead from bar ${targetBar} on (${last})`);
    const before = await position();
    if (before && before.bar >= targetBar && (targetBar !== 5 || before.fraction < .94)) {
      const fraction=await scroller.evaluate(el=>{const w=el.querySelector('.placement-timeline'), rail=w.querySelector('.placement-track').getBoundingClientRect().width, line=w.querySelector('[data-placement-playhead]'); return (parseFloat(line.style.left)-rail)/(w.getBoundingClientRect().width-rail);});
      const after = await position();
      last = `playhead ${fraction}, transport ${before.fraction}..${after && after.fraction}`;
      if (after && after.fraction >= before.fraction && after.fraction-before.fraction <= .06) {
        assert.ok(fraction > before.fraction-.03 && fraction < after.fraction+.03, `3/4 ${last}`);
        if (targetBar === 5) assert.ok(fraction < .98, `playhead reached the end early: ${last}`);
        break;
      }
    }
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  await page.locator('#transport-form button[value=pause]').click();
  await until(async()=>!(await (await page.request.get(new URL('/api/transport',base).href)).json()).playing,'transport did not pause');
});
