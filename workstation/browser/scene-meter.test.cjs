/* Scene playhead and Follow in a non-4/4 song. */
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const fixture = `// cicada-scene-meter-test: disposable fixture
cicada 2
title "Scene meter"
meter 3/4
tempo 60
track bass acid {}
pattern pulse { 1 . 5 . }
scene main { bass=pulse }
scene alternate { bass=pulse }
song { main*4 alternate*1 }
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
test('scene playhead follows 3/4 transport bars', {timeout:60000}, async t => {
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(base.hostname, '127.0.0.1');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close());
  const page = await browser.newPage({viewport:{width:800,height:1000}});
  await page.goto(new URL('/studio',base).href);
  await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
  await page.locator('#view-tab-arrange').click();
  await page.locator('[data-scene-navigation][data-placement-navigation-ready]').waitFor();
  const scroller = page.locator('[data-scene-navigation] .timeline');
  await page.getByLabel('Timeline zoom',{exact:true}).selectOption('192');
  await page.locator('[name=placementFollow]').check();
  await page.locator('#transport-form button[value=play]').click();
  await until(async()=>{
    const t = await (await page.request.get(new URL('/api/transport',base).href)).json();
    if (!t.frames?.transport) return false;
    const frame = Buffer.from(t.frames.transport,'base64');
    if (frame.length!==64) return false;
    const bar=frame.readInt32LE(52), beat=frame.readInt32LE(56), tick=frame.readInt32LE(60);
    if (bar<2 || beat!==1 || tick>240) return false;
    const fraction=await scroller.evaluate(el=>{const w=el.querySelector('.scene-timeline'), rail=w.querySelector('.timeline-track').getBoundingClientRect().width, line=w.querySelector('[data-placement-playhead]'); return (parseFloat(line.style.left)-rail)/(w.getBoundingClientRect().width-rail);});
    assert.ok(Math.abs(fraction-(bar-1)/5)<.04, `3/4 playhead fraction ${fraction} at bar ${bar}`);
    return true;
  },'transport did not reach a 3/4 bar line');
  await until(async()=>scroller.evaluate(el=>{const r=el.getBoundingClientRect(), h=el.querySelector('[data-placement-playhead]').getBoundingClientRect(); return el.scrollLeft>0 && h.left>=r.left && h.left<=r.right;}),'Follow did not keep the playhead visible at 4x');
  await page.locator('#transport-form button[value=pause]').click();
  await until(async()=>!(await (await page.request.get(new URL('/api/transport',base).href)).json()).playing,'transport did not pause');
});

test('a scene view opens fitted on a desktop and at 1x on a phone, and a stored zoom wins', {timeout:60000}, async t => {
  const base = new URL(process.env.CICADA_STUDIO_URL);
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close());
  const page = await browser.newPage({viewport:{width:1440,height:1000}});
  const open = async () => {
    await page.goto(new URL('/studio',base).href);
    await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
    await page.locator('#view-tab-arrange').click();
    await page.locator('[data-scene-navigation][data-placement-navigation-ready]').waitFor();
  };
  const zoom = page.getByLabel('Timeline zoom',{exact:true});
  await open();
  await page.evaluate(() => sessionStorage.removeItem('cicada-scene-view'));
  await open();
  assert.equal(await zoom.inputValue(),'fit','a desktop window opens fitted');
  // The Edit link of a one-bar section is the only way to its inspector: it
  // must stay whole and inside its header at every default.
  const edit = page.getByRole('link', {name:/^Edit alternate/});
  const editFits = () => edit.evaluate(el => { const r=el.getBoundingClientRect(), p=el.closest('.timeline-heading').getBoundingClientRect(); return r.width>0 && r.left>=p.left-.5 && r.right<=p.right+.5; });
  assert.equal(await editFits(),true,'Edit must fit in a one-bar header of a fitted desktop view');
  await page.setViewportSize({width:390,height:844});
  await page.evaluate(() => sessionStorage.removeItem('cicada-scene-view'));
  await open();
  assert.equal(await zoom.inputValue(),'48','a phone-width window opens at 1x');
  assert.equal(await editFits(),true,'Edit must fit in a one-bar header at phone width and 1x');
  await zoom.selectOption('96');
  await open();
  assert.equal(await zoom.inputValue(),'96','a stored zoom wins over the default');
  await page.setViewportSize({width:1440,height:1000});
  await open();
  assert.equal(await zoom.inputValue(),'96','a stored zoom wins on a desktop too');
});
