'use strict';
const test = require('node:test'), assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path');
if (process.argv[2] === '--write-fixture') {
  const target = path.resolve(process.argv[3]); fs.mkdirSync(path.dirname(target), {recursive:true});
  fs.writeFileSync(target, '// cicada-library-recording-test: disposable fixture\ncicada 2\ntitle "A new phrase"\ntempo 120\nseed 42\ntrack keys piano { level=-24dB }\ntrack bass acid { level=-24dB cutoff=400Hz }\npattern pulse acid { c2 . g2 . }\nscene main { bass=pulse keys=off }\nsong { main*8 }\n');
} else {
  test('create, record and audition an unused library pattern without changing the song', {timeout:120000}, async t => {
    const base = new URL(process.env.CICADA_STUDIO_URL), proof = process.env.CICADA_LIBRARY_RECORDING_PROOF_DIR;
    assert.equal(base.hostname, '127.0.0.1'); assert.ok(path.isAbsolute(proof)); fs.mkdirSync(proof, {recursive:true});
    const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
    const browser = await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']}); t.after(() => browser.close());
    const page = await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'}), errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.exposeFunction('__cicadaLibraryCreationProof', () => page.locator('#live details[aria-label="Create pattern"]').screenshot({path:path.join(proof,'library-create-desktop.png')}));
    await page.exposeFunction('__cicadaLibraryPickerProof', async () => {
      await page.locator('#cicada-live .actions').first().screenshot({path:path.join(proof,'library-picker-desktop.png')});
      await page.setViewportSize({width:390,height:844}); await page.waitForTimeout(150);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
      await page.locator('#cicada-live .actions').first().screenshot({path:path.join(proof,'library-picker-phone.png')});
      await page.setViewportSize({width:1440,height:1100});
    });
    await page.exposeFunction('__cicadaLibraryReviewProof', () => page.locator('.note-preview').screenshot({path:path.join(proof,'library-review.png')}));
    await page.addInitScript(() => { window.__cicadaLibraryRecordingResult = result => { window.__libraryRecordingResult = result; }; });
    await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-library-recording-workflow.js')});
    await page.goto(base.href); await page.goto(base.origin + '/studio');
    await page.waitForFunction(() => window.__libraryRecordingResult, null, {timeout:100000});
    const result = await page.evaluate(() => window.__libraryRecordingResult);
    fs.writeFileSync(path.join(proof,'report.json'), JSON.stringify({result,errors},null,2)+'\n');
    assert.equal(result.ok,true,result.error); assert.deepEqual(errors,[]);
  });
}
