'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

if (process.argv[2] === '--write-fixture') {
  fs.writeFileSync(process.argv[3], `cicada 2\nfx tone eq {}\ntrack bass acid { insert = tone }\npattern p { 1 . }\nscene main { bass = p }\nsong { main }\n`, {flag:'wx'});
  process.exit(0);
}

test('chorus mixer controls persist and support history', {timeout:120000}, async t => {
  const base=process.env.CICADA_STUDIO_URL, score=process.env.CICADA_CHORUS_SCORE, proof=process.env.CICADA_CHORUS_PROOF_DIR;
  assert.equal(new URL(base).hostname,'127.0.0.1');
  assert(path.isAbsolute(score) && path.isAbsolute(proof));
  fs.mkdirSync(proof,{recursive:true});
  const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox','--mute-audio']});
  t.after(()=>browser.close());
  const page=await browser.newPage({viewport:{width:1440,height:1000}}), errors=[];
  page.on('pageerror', e=>errors.push(e.message));
  page.on('response', async r=>{if (new URL(r.url()).pathname==='/__actions/mixer' && r.status()>=400) errors.push(`mixer ${r.status()}: ${(await r.text()).slice(0,400)}`)});
  const ready=()=>page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout:30000});
  const source=()=>fs.readFileSync(score,'utf8');
  const waitSource=async predicate=>{const end=Date.now()+30000; while(Date.now()<end){let s=source();if(predicate(s))return s;await new Promise(r=>setTimeout(r,50))}assert.fail(`score not updated: ${JSON.stringify(errors)}`)};
  const waitRevision=async text=>{const hash=crypto.createHash('sha256').update(text).digest('hex');await page.waitForFunction(x=>document.querySelector('#mixer form input[name=revision]')?.value===x,hash,{timeout:30000})};
  await page.goto(new URL('/studio?tab=mixer',base).href); await ready();
  const panel=page.locator('#mixer'); await panel.waitFor();
  const create=panel.locator('form').filter({has:page.locator('input[name=action][value=add-chorus]')});
  await create.locator('input[name=name]').fill('wide'); await create.getByRole('button',{name:'Create chorus'}).click();
  let saved=await waitSource(s=>s.includes('fx wide chorus')); await waitRevision(saved);
  let chain=panel.locator('[aria-label="bass insert chain"]');
  await chain.locator('select[name=effect]').selectOption('wide'); await chain.getByRole('button',{name:'Add effect'}).click();
  saved=await waitSource(s=>/insert\s*=\s*tone\s*->\s*wide/.test(s)); await waitRevision(saved);
  chain=panel.locator('[aria-label="bass insert chain"]');
  await chain.getByRole('button',{name:'Move wide earlier'}).click();
  saved=await waitSource(s=>/insert\s*=\s*wide\s*->\s*tone/.test(s)); await waitRevision(saved);
  const effect=panel.locator('#effect-wide');
  const mix=effect.locator('form').filter({has:page.locator('input[name=path][value="wide.mix"]')});
  await mix.locator('[name=value]').fill('0.7'); await mix.getByRole('button',{name:'Set',exact:true}).click();
  const edited=await waitSource(s=>s.includes('mix = 0.7')); await waitRevision(edited);
  await effect.scrollIntoViewIfNeeded(); await page.screenshot({path:path.join(proof,'chorus-desktop.png')});
  await page.locator('#undo-form button').click();
  let undone=await waitSource(s=>!s.includes('mix = 0.7')); await waitRevision(undone);
  await page.locator('#redo-form button').click(); await waitSource(s=>s===edited); await waitRevision(edited);
  await page.reload(); await ready();
  assert.equal(await page.locator('#effect-wide form').filter({has:page.locator('input[name=path][value="wide.mix"]')}).locator('[name=value]').inputValue(),'0.7');
  chain=panel.locator('[aria-label="bass insert chain"]');
  await chain.locator('li').filter({has:page.getByRole('link',{name:'wide',exact:true})}).getByRole('button',{name:'Remove'}).click();
  saved=await waitSource(s=>!/insert\s*=\s*wide/.test(s)); await waitRevision(saved);
  await page.setViewportSize({width:390,height:844});
  await page.locator('#effect-wide').scrollIntoViewIfNeeded();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'phone layout overflows');
  await page.screenshot({path:path.join(proof,'chorus-phone.png')});
  assert.deepEqual(errors,[]);
});
