'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

if (process.argv[2] === '--write-fixture') {
  fs.writeFileSync(process.argv[3], `cicada 2\nfx tone eq {}\nfx echo delay {}\ntrack bass acid { insert = tone send echo = -12dB }\npattern p { 1 . }\nscene main { bass = p }\nsong { main }\n`, {flag:'wx'});
  process.exit(0);
}

test('chorus mixer controls persist and support history', {timeout:120000}, async t => {
  const base=process.env.CICADA_STUDIO_URL, score=process.env.CICADA_CHORUS_SCORE, proof=process.env.CICADA_CHORUS_PROOF_DIR;
  assert.equal(new URL(base).hostname,'127.0.0.1');
  assert(path.isAbsolute(score) && path.isAbsolute(proof));
  fs.mkdirSync(proof,{recursive:true});
  const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close(), {timeout: 15000});
  const page=await browser.newPage({viewport:{width:1440,height:1000}}), errors=[];
  page.on('pageerror', e=>errors.push(e.message));
  page.on('response', async r=>{if (new URL(r.url()).pathname==='/__actions/mixer' && r.status()>=400) errors.push(`mixer ${r.status()}: ${(await r.text()).slice(0,400)}`)});
  const ready=()=>page.locator('#studio-shell[data-shell-actions=ready]').waitFor({timeout:30000});
  const source=()=>fs.readFileSync(score,'utf8');
  const waitSource=async predicate=>{const end=Date.now()+30000; while(Date.now()<end){let s=source();if(predicate(s))return s;await new Promise(r=>setTimeout(r,50))}assert.fail(`score not updated: ${JSON.stringify(errors)}`)};
  const waitRevision=async text=>{const hash=crypto.createHash('sha256').update(text).digest('hex');await page.waitForFunction(x=>document.querySelector('#mixer form input[name=revision]')?.value===x,hash,{timeout:30000})};
  await page.goto(new URL('/studio?tab=mixer',base).href); await ready();
  const panel=page.locator('#mixer'); await panel.waitFor();
  // Mixer strips show direct controls only. A strip's name opens the channel in
  // Device, which holds the insert chain and every effect's parameters.
  const device=page.locator('#device-editor');
  const open=async(name,channel)=>{await panel.getByRole('link',{name:`Open ${name} in Device`,exact:true}).click();await device.locator(`.device-groups[data-device-channel="${channel}"]`).waitFor();await page.locator('#detail-tab-device[aria-selected="true"]').waitFor()};
  const param=path=>device.locator('form').filter({has:page.locator(`input[name=path][value="${path}"]`)});
  const addEffect=panel.locator('.mixer-add-effect');
  // The chorus form sits behind the tool strip's Add effect, with the same fields and action.
  assert.equal(await addEffect.locator('form').isVisible(),false,'the chorus form is closed until asked for');
  await addEffect.locator('summary').click();
  const create=addEffect.locator('form').filter({has:page.locator('input[name=action][value=add-chorus]')});
  await create.locator('input[name=name]').fill('wide'); await create.getByRole('button',{name:'Create chorus'}).click();
  let saved=await waitSource(s=>s.includes('fx wide chorus')); await waitRevision(saved);
  // A new effect is on no insert chain yet: Add effect lists it and opens it in Device.
  await addEffect.locator('summary').click();
  await open('wide','effect:wide');
  assert.equal(await param('wide.mix').count(),1,'Device holds the parameters of an effect on no channel');
  assert.match(await device.innerText(),/This effect is on no channel\./);
  await open('bass','bass');
  let chain=device.locator('[aria-label="bass insert chain"]');
  await chain.locator('select[name=effect]').selectOption('wide'); await chain.getByRole('button',{name:'Add effect'}).click();
  saved=await waitSource(s=>/insert\s*=\s*tone\s*->\s*wide/.test(s)); await waitRevision(saved);
  await page.waitForFunction(()=>[...document.querySelectorAll('#mixer .strip[data-mixer-channel=bass] .strip-inserts li')].map(item=>item.textContent).join(' ')==='tone wide',null,{timeout:30000});
  assert.equal(await addEffect.locator('[data-mixer-select="effect:wide"]').count(),0,'an effect on a chain is no longer listed as on no channel');
  chain=device.locator('[aria-label="bass insert chain"]');
  await chain.getByRole('button',{name:'Move wide earlier'}).click();
  saved=await waitSource(s=>/insert\s*=\s*wide\s*->\s*tone/.test(s)); await waitRevision(saved);
  // Each insert's parameters open under the chain, in Device.
  const effect=device.locator('#device-effect-wide');
  await effect.locator('summary').click();
  await param('wide.mix').locator('[name=value]').fill('0.7'); await param('wide.mix').getByRole('button',{name:'Set',exact:true}).click();
  const edited=await waitSource(s=>s.includes('mix = 0.7')); await waitRevision(edited);
  await page.screenshot({path:path.join(proof,'chorus-desktop.png')});
  await page.locator('#undo-form button').click();
  let undone=await waitSource(s=>!s.includes('mix = 0.7')); await waitRevision(undone);
  await page.locator('#redo-form button').click(); await waitSource(s=>s===edited); await waitRevision(edited);
  await page.reload(); await ready();
  assert.equal(await device.locator('.device-groups').getAttribute('data-device-channel'),'bass','a reload keeps the channel Device shows');
  assert.equal(await param('wide.mix').locator('[name=value]').inputValue(),'0.7');
  // The strip face names the inserts in order and the send with its level, and holds no forms for them.
  const bass=panel.locator('.strip[data-mixer-channel=bass]');
  assert.deepEqual(await bass.locator('.strip-inserts li').allTextContents(),['wide','tone']);
  assert.deepEqual(await bass.locator('.strip-sends li').allTextContents(),['echo -12 dB']);
  assert.equal(await panel.locator('.strips .effect-chain, .strips .mixer-control, .strips select').count(),0,'the strip face holds no chain editor or Set form');
  chain=device.locator('[aria-label="bass insert chain"]');
  await chain.locator('li').filter({has:page.getByRole('link',{name:'wide',exact:true})}).getByRole('button',{name:'Remove'}).click();
  saved=await waitSource(s=>!/insert\s*=\s*wide/.test(s)); await waitRevision(saved);
  // A return has a strip of its own; its effect parameters save and undo in Device.
  await open('echo','return:echo');
  assert.equal(await panel.locator('.strip[data-mixer-channel="return:echo"]').getAttribute('data-mixer-selected'),'true');
  assert.equal(await panel.locator('.strip[data-mixer-channel="return:echo"] .strip-kind').textContent(),'Return');
  await param('echo.feedback').locator('[name=value]').fill('0.4'); await param('echo.feedback').getByRole('button',{name:'Set',exact:true}).click();
  const echoed=await waitSource(s=>/fx echo delay \{[^}]*feedback = 0\.4/.test(s)); await waitRevision(echoed);
  assert.equal(await device.locator('.device-groups').getAttribute('data-device-channel'),'return:echo','Device stays on the return after its save');
  await page.locator('#undo-form button').click();
  await waitSource(s=>s===saved); await waitRevision(saved);
  // The send level is exact in the track's Device.
  await open('bass','bass');
  assert.equal(await param('bass.send.echo').count(),1,'Device holds the send level');
  await page.setViewportSize({width:390,height:844});
  await panel.locator('.strip[data-mixer-channel=bass]').scrollIntoViewIfNeeded();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'phone layout overflows');
  await page.screenshot({path:path.join(proof,'chorus-phone.png')});
  // On a phone Device is in the Edit region.
  await page.locator('.shell-phone-nav [data-shell-region=detail]').click();
  await device.waitFor();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'phone Device overflows');
  assert.deepEqual(errors,[]);
});
