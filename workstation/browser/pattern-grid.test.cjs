'use strict';
const test = require('node:test'), assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path');
if (process.argv[2] === '--write-fixture') {
  const target = path.resolve(process.argv[3]); fs.mkdirSync(path.dirname(target), {recursive:true});
  fs.writeFileSync(target, '// cicada-pattern-grid-test: disposable fixture\ncicada 2\ntitle "Complete phrases"\ntempo 120\nseed 42\ntrack keys piano { level=-24dB }\ntrack bass acid { level=-24dB cutoff=400Hz }\npattern pulse acid { c2 . g2 . }\npattern slow notes { step=1/2 c4 e4 g4 c5 }\npattern triplet notes { step=1/8t c4 e4 g4 c5 }\npattern dotted notes { step=1/8. c4 e4 g4 c5 }\npattern tuplet acid { step=1/8 [1 3 5] - }\narrange { place opening bass pulse { at=@1.1.1 length=1bar } }\n');
} else {
  test('authored grids keep their full musical duration when placed, exported and reopened', {timeout:180000}, async t => {
    const base = new URL(process.env.CICADA_STUDIO_URL), proof = process.env.CICADA_PATTERN_GRID_PROOF_DIR;
    assert.equal(base.hostname, '127.0.0.1'); assert.ok(path.isAbsolute(proof)); fs.mkdirSync(proof, {recursive:true});
    const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
    const browser = await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']}); t.after(() => browser.close());
    const page = await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'}), errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.exposeFunction('__cicadaPatternGridAudio', (label, base64) => fs.writeFileSync(path.join(proof,label+'.wav'), Buffer.from(base64,'base64')));
    await page.exposeFunction('__cicadaPatternGridViews', async id => {
      await page.getByRole('separator',{name:'Resize Detail dock',exact:true}).press('End');
      const roll = page.locator('.pattern-workspace');
      await roll.evaluate(node => node.scrollIntoView({block:'start'}));
      await page.locator('#studio-detail').screenshot({path:path.join(proof,'grid-desktop.png')});
      await page.setViewportSize({width:390,height:844});
      await roll.evaluate(node => node.scrollIntoView({block:'start'}));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),true);
      await page.locator('#studio-detail').screenshot({path:path.join(proof,'grid-phone.png')});
      const form = page.locator('[data-pattern-management='+id+'] form:has(input[name=action][value=append])');
      await form.evaluate(node => node.scrollIntoView({block:'center'}));
      await form.screenshot({path:path.join(proof,'grid-placement-phone.png')});
      await page.setViewportSize({width:1440,height:1100});
    });
    if (process.env.CICADA_PATTERN_GRID_REOPEN === '1') {
      const previous = JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8')).result;
      await page.addInitScript(previous => sessionStorage.setItem('cicada.pattern-grid-proof',JSON.stringify({...previous,phase:'reopen'})),previous);
    }
    await page.addInitScript(() => { window.__cicadaPatternGridResult = result => { window.__patternGridResult = result; }; });
    await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-pattern-grid-workflow.js')});
    await page.goto(base.href); await page.goto(base.origin+'/studio?dock=clip&pattern=slow&region=detail');
    await page.waitForFunction(() => window.__patternGridResult,null,{timeout:160000});
    const result = await page.evaluate(() => window.__patternGridResult);
    fs.writeFileSync(path.join(proof,process.env.CICADA_PATTERN_GRID_REOPEN === '1' ? 'reopen-report.json' : 'report.json'),JSON.stringify({result,errors},null,2)+'\n');
    if (!result.ok) { await page.screenshot({path:path.join(proof,'failure.png')}); fs.writeFileSync(path.join(proof,'failure.html'),await page.content()); }
    assert.equal(result.ok,true,result.error); assert.deepEqual(errors,[]);
  });
}
