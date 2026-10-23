'use strict';
const test = require('node:test'), assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path');
if (process.argv[2] === '--write-fixture') {
  const target = path.resolve(process.argv[3]), placement = process.argv.includes('--placement');
  fs.mkdirSync(path.dirname(target), {recursive:true});
  fs.writeFileSync(target, '// cicada-pattern-song-test: disposable fixture\ncicada 2\ntitle "A phrase becomes a song"\ntempo 120\nseed 42\ntrack keys piano { level=-18dB }\ntrack bass acid { level=-24dB cutoff=400Hz }\ntrack drums drums {}\npattern pulse acid { c2 . g2 . }\npattern fresh performance length=4001ticks {\n  note 60 at=17ticks length=326ticks velocity=90;\n  note 64 at=1057ticks length=500ticks velocity=100;\n}\n' + (placement ? '// named placements\narrange { place opening bass pulse { at=@1.1.1 length=1bar } }\n' : 'scene main { bass=pulse keys=off }\nsong { main*4 }\n'));
} else {
  test('played phrases make variations, enter the song, export and reopen', {timeout:180000}, async t => {
    const base = new URL(process.env.CICADA_STUDIO_URL), proof = process.env.CICADA_PATTERN_SONG_PROOF_DIR;
    assert.equal(base.hostname, '127.0.0.1'); assert.ok(path.isAbsolute(proof)); fs.mkdirSync(proof, {recursive:true});
    const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
    const browser = await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']}); t.after(() => browser.close());
    const page = await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'}), errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.exposeFunction('__cicadaPatternSongAudio', (label, base64) => fs.writeFileSync(path.join(proof, label + '.wav'), Buffer.from(base64, 'base64')));
    await page.exposeFunction('__cicadaPatternSongViews', async () => {
      const panel = page.locator('[data-pattern-management=fresh]');
      await page.getByRole('separator',{name:'Resize Detail dock',exact:true}).press('End');
      await panel.screenshot({path:path.join(proof,'phrase-song-desktop.png')});
      await page.setViewportSize({width:390,height:844});
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
      await panel.screenshot({path:path.join(proof,'phrase-song-phone.png')});
      await page.setViewportSize({width:1440,height:1100});
    });
    if (process.env.CICADA_PATTERN_SONG_REOPEN === '1') {
      const previous = JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8')).result;
      await page.addInitScript(previous => sessionStorage.setItem('cicada.pattern-song-proof', JSON.stringify({...previous,phase:'reopen'})), previous);
    }
    await page.addInitScript(() => { window.__cicadaPatternSongResult = result => { window.__patternSongResult = result; }; });
    await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-pattern-song-workflow.js')});
    await page.goto(base.href); await page.goto(base.origin + '/studio?dock=clip&pattern=fresh&region=detail');
    await page.waitForFunction(() => window.__patternSongResult, null, {timeout:160000});
    const result = await page.evaluate(() => window.__patternSongResult);
    fs.writeFileSync(path.join(proof,process.env.CICADA_PATTERN_SONG_REOPEN === '1' ? 'reopen-report.json' : 'report.json'), JSON.stringify({result,errors},null,2)+'\n');
    if (!result.ok) {
      await page.screenshot({path:path.join(proof,'failure.png')});
      fs.writeFileSync(path.join(proof,'failure.html'), await page.content());
    }
    assert.equal(result.ok,true,result.error); assert.deepEqual(errors,[]);
  });
}
