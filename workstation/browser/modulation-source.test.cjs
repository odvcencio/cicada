// Disposable score and null audio; no device access is needed.
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

if (process.argv[2] === '--write-fixture') {
  assert(process.argv[3], 'provide a disposable score path');
  fs.writeFileSync(process.argv[3], `cicada 2
tempo 120
track bass acid {}
pattern p { 1 . }
scene main { bass=p }
song { main }
lfo drift { rate=1/4 }
contour swell { period=1bar @0 0 @1 0 }
mod drift -> bass.pan depth=0.2
`, {flag:'wx'});
  process.exit(0);
}

test('modulation sources and contour points save, undo, and survive reload', {timeout:120000}, async t => {
  const base = process.env.CICADA_STUDIO_URL, score = process.env.CICADA_MODULATION_SCORE, proof = process.env.CICADA_MODULATION_PROOF_DIR;
  assert.equal(new URL(base).hostname, '127.0.0.1');
  assert(path.isAbsolute(score) && path.isAbsolute(proof));
  fs.mkdirSync(proof, {recursive:true});
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close());
  const context = await browser.newContext({viewport:{width:1440,height:1000}}), page = await context.newPage(), errors = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('response', async response => {
    if (new URL(response.url()).pathname === '/__actions/automation' && response.status() >= 400) {
      errors.push(`automation HTTP ${response.status()}: ${(await response.text()).slice(0,500)}`);
    }
  });
  const ready = () => page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout:30000});
  const waitSource = async predicate => {
    const end = Date.now()+30000;
    while (Date.now()<end) { const value = fs.readFileSync(score,'utf8'); if (predicate(value)) return value; await new Promise(r=>setTimeout(r,50)); }
    assert.fail(`score was not updated: ${JSON.stringify(errors)}; status=${await page.locator('[role=status]').allTextContents()}`);
  };
  const waitRevision = async source => {
    const revision = crypto.createHash('sha256').update(source).digest('hex');
    await page.waitForFunction(expected => document.querySelector('[data-mod-source="swell"] input[name="revision"]')?.value === expected, revision, {timeout:30000});
  };
  const open = async id => {
    const details = page.locator(`[data-mod-source="${id}"] details`);
    await details.waitFor();
    if (!(await details.evaluate(el => el.open))) await details.locator('summary').click();
    return details;
  };
  const setting = (details, name) => details.locator('form').filter({has:page.locator(`input[name="field"][value="${name}"]`)});
  await page.goto(new URL('/studio?tab=arrange&region=detail&dock=automation',base).href);
  await ready();
  let drift = await open('drift');
  await setting(drift,'rate').locator('[name=value]').fill('1/2');
  await setting(drift,'rate').getByRole('button',{name:'Save Rate or period'}).click();
  const rateSaved = await waitSource(s => s.includes('rate=1/2'));
  await waitRevision(rateSaved);
  await ready();
  let swell = await open('swell');
  const add = swell.locator('form').filter({has:page.locator('input[name="operation"][value="point-add"]')});
  await add.locator('[name=at]').fill('0.5');
  await add.locator('[name=value]').fill('0.75');
  await add.locator('[name=shape]').selectOption('smooth');
  await add.getByRole('button',{name:'Add point'}).click();
  const saved = await waitSource(s => s.includes('@0.5 0.75 smooth'));
  await waitRevision(saved);
  await page.waitForFunction(() => document.querySelectorAll('[data-mod-source="swell"] input[name="index"]').length === 3);
  await ready();
  swell = await open('swell');
  const resize = page.getByRole('separator',{name:'Resize Detail dock',exact:true});
  await resize.focus();
  await page.keyboard.press('End');
  await swell.getByRole('heading',{name:'Contour points'}).scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(proof,'modulation-source-desktop.png')});
  await page.locator('#undo-form button').click();
  const undone = await waitSource(s => !s.includes('@0.5 0.75 smooth'));
  await waitRevision(undone);
  await ready();
  await page.locator('#redo-form button').click();
  await waitSource(s => s === saved);
  await waitRevision(saved);
  await page.waitForFunction(() => document.querySelectorAll('[data-mod-source="swell"] input[name="index"]').length === 3);
  await page.reload();
  await ready();
  swell = await open('swell');
  const middle = swell.locator('form').filter({has:page.locator('input[name="index"][value="1"]')});
  assert.equal(await middle.locator('input[name=at]').inputValue(), '0.5');
  await page.setViewportSize({width:390,height:844});
  await page.goto(new URL('/studio?tab=arrange&region=detail&dock=automation',base).href);
  await ready();
  swell = await open('swell');
  await swell.getByRole('heading',{name:'Contour points'}).scrollIntoViewIfNeeded();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth <= innerWidth), true, 'phone layout overflows');
  await page.screenshot({path:path.join(proof,'modulation-source-phone.png')});
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(proof,'report.json'), JSON.stringify({checks:['rate save','contour insert','undo and redo','reload','desktop and phone layout'],errors},null,2)+'\n');
});
