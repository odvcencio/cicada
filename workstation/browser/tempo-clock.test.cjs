'use strict';
const test = require('node:test'), assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path');
if (process.argv[2] === '--write-fixture') {
  const target = path.resolve(process.argv[3]); fs.mkdirSync(path.dirname(target), {recursive:true});
  fs.writeFileSync(target, '// cicada-tempo-clock-test: disposable fixture\ntitle "Tempo clock proof"\ntempo 120\ntrack keys piano { level=-24dB }\npattern intro notes { . . . . }\npattern phrase notes { . . . . }\nscene main { keys=intro }\nscene slow { keys=intro tempo=60 }\nscene capture { keys=phrase }\nsong { main slow*4 }\n');
} else {
  test('packaged recording clock crosses an authored tempo boundary and retains played timing', {timeout:120000}, async t => {
    const base = new URL(process.env.CICADA_STUDIO_URL), proof = process.env.CICADA_TEMPO_CLOCK_PROOF_DIR;
    assert.equal(base.hostname, '127.0.0.1'); assert.ok(path.isAbsolute(proof)); fs.mkdirSync(proof, {recursive:true});
    const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
    const browser = await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']}); t.after(() => browser.close());
    const page = await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'}), errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.exposeFunction('__cicadaTempoClockReview', () => page.locator('.note-preview').screenshot({path:path.join(proof,'played-tempo-review.png')}));
    await page.addInitScript(() => { window.__cicadaTempoClockResult = result => { window.__tempoClockResult = result; }; });
    await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-tempo-clock-workflow.js')});
    await page.goto(base.href);
    await page.goto(base.origin + '/studio');
    await page.waitForFunction(() => window.__tempoClockResult, null, {timeout:100000});
    const result = await page.evaluate(() => window.__tempoClockResult);
    fs.writeFileSync(path.join(proof,'report.json'), JSON.stringify({result,errors},null,2)+'\n');
    assert.equal(result.ok,true,result.error); assert.deepEqual(errors,[]);
  });
}
