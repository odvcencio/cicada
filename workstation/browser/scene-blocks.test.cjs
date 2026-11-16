/* Real scene arrangement controls against a fresh disposable Studio. */
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const fixture = `// cicada-scene-block-test: disposable fixture
cicada 2
title "Scene block editing"
track bass acid {}
midi { port keys; keys ch 1 -> bass }
${Array.from({length:14}, (_, i) => `track layer${i} acid {}`).join('\n')}
pattern pulse { 1 . 5 . }
scene main { bass=pulse }
scene alternate_long_scene_name { bass=pulse }
song { main*4 alternate_long_scene_name*1 }
`;
if (process.argv[2] === '--write-fixture') {
  fs.writeFileSync(process.argv[3], fixture, {flag:'wx'});
  fs.writeFileSync(path.join(path.dirname(process.argv[3]), 'cicada.local'), 'port keys = \"Synthetic Keyboard\"\n', {flag:'wx'});
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
test('scene headers open guarded block controls and Undo restores the song', {timeout: 60000}, async t => {
  const base = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(base.hostname, '127.0.0.1');
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox','--mute-audio']});
  t.after(() => browser.close());
  const page = await browser.newPage({viewport:{width:1440,height:1000}});
  const errors = [], writes = [], notes = [], nonGet = [];
  await page.addInitScript(() => {
    const access = new EventTarget(), port = new EventTarget();
    Object.assign(port, {id:'test-keys', name:'Synthetic Keyboard', state:'connected'});
    access.inputs = new Map([['test-keys',port]]);
    window.__sendSceneMIDI = (...bytes) => { const event = new Event('midimessage'); Object.defineProperty(event,'data',{value:new Uint8Array(bytes)}); port.dispatchEvent(event); };
    Object.defineProperty(navigator,'requestMIDIAccess',{configurable:true,value:() => Promise.resolve(access)});
  });
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', msg => { if(msg.text().includes('scene reveal')) console.log(msg.text()); });
  page.on('request', request => {
    if (request.method() !== 'GET') nonGet.push(request.url());
    const endpoint = new URL(request.url()).pathname;
    if (request.method()==='POST' && endpoint==='/__actions/song') writes.push(request);
    if (request.method()==='POST' && endpoint==='/__actions/live') {
      try { const data=request.postDataJSON(); if(data.type==='input-batch') notes.push(...JSON.parse(data.messages).filter(message=>message.type==='note')); } catch {}
    }
  });
  const state = async () => (await page.request.get(new URL('/api/state',base).href)).json();
  const ready = async () => {
    await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
    await until(async () => await page.locator('#undo-form input[name=revision]').inputValue() === (await state()).revision, 'shell revision did not settle');
  };
  await page.goto(new URL('/studio',base).href);
  await ready();
  // Isolated engine builds can exercise source CSS before assets are rebuilt.
  if (process.env.CICADA_SCENE_SOURCE_CSS === '1') await page.addStyleTag({path:path.resolve(__dirname,'../app/ui/view-arrange.css')});
  const before = await state();
  assert.equal(before.source, fixture);
  await page.locator('#view-tab-session').click();
  await page.locator('[data-live-control=midi]').click();
  await page.waitForFunction(() => document.querySelector('[data-live-status]')?.textContent.includes('MIDI enabled'));
  await page.locator('#transport-form button[value=play]').click();
  await page.waitForFunction(() => !document.querySelector('[data-live-control=record]').disabled);
  await page.locator('#view-tab-arrange').click();
  const scroller=page.locator('[data-scene-navigation] .timeline'), zoom=page.getByLabel('Timeline zoom',{exact:true}), follow=page.locator('[name=placementFollow]');
  await page.locator('[data-scene-navigation][data-placement-navigation-ready]').waitFor();
  await until(async()=> (await page.locator('[data-scene-navigation] [data-placement-playback-status]').textContent()).startsWith('Playing'), 'scene playback did not reach the view');
  assert.equal(await zoom.inputValue(),'fit', 'desktop defaults to Fit');
  // The phone default and a stored zoom need page reloads, which would drop
  // this suite's MIDI connection: scene-meter.test.cjs covers them.
  const edit = page.getByRole('link', {name:'Edit alternate_long_scene_name · bars 5–5', exact:true});
  const editFits = () => edit.evaluate(el => { const r=el.getBoundingClientRect(), p=el.closest('.timeline-heading').getBoundingClientRect(); return r.width>0 && r.left>=p.left && r.right<=p.right; });
  await zoom.selectOption('48');
  assert.equal(await editFits(),true,'Edit must fit at desktop 1x');
  await page.setViewportSize({width:800,height:1000});
  const viewRequests=nonGet.length, viewRevision=(await state()).revision;
  // The anchor is checked with Follow off: while the song plays, a following
  // view moves under the pointer by itself between the reading and the wheel.
  await zoom.selectOption('192'); await follow.uncheck();
  const box=await scroller.boundingBox(), rail=await scroller.locator('.timeline-track').first().evaluate(el=>el.getBoundingClientRect().width), pointerX=box.x+rail+100;
  const anchor=()=>scroller.evaluate((el,x)=>{const w=el.querySelector('.scene-timeline'), rail=w.querySelector('.timeline-track').getBoundingClientRect().width; return (x-el.getBoundingClientRect().left-rail+el.scrollLeft)/(w.getBoundingClientRect().width-rail);},pointerX);
  const worldWidth=()=>scroller.evaluate(el=>{const w=el.querySelector('.scene-timeline'); return w.getBoundingClientRect().width-w.querySelector('.timeline-track').getBoundingClientRect().width;});
  await page.mouse.move(pointerX,box.y+50);
  const beforeAnchor=await anchor();
  await page.keyboard.down('Control'); await page.mouse.wheel(0,-120); await page.keyboard.up('Control');
  await until(async()=>await zoom.inputValue()==='384','scene wheel zoom did not apply');
  assert.ok(Math.abs((await anchor())-beforeAnchor)*(await worldWidth())<=1,'wheel zoom must preserve the pointer anchor');
  // With Follow on, a zoom at the pointer pauses it, so the view stays where
  // the user zoomed; the control stays checked and reads "Follow (paused)".
  await follow.check();
  await page.mouse.move(pointerX,box.y+50);
  await page.keyboard.down('Control'); await page.mouse.wheel(0,120); await page.keyboard.up('Control');
  await until(async()=>await zoom.inputValue()==='192','scene wheel zoom out did not apply');
  await until(async()=>await follow.getAttribute('data-follow-paused')!==null,'zoom did not pause Follow');
  assert.equal(await page.getByLabel('Follow (paused)',{exact:true}).count(),1);
  await follow.click();
  assert.equal(await follow.isChecked(),true);
  await until(async()=>await follow.getAttribute('data-follow-paused')===null,'one click did not resume Follow');
  await page.mouse.move(pointerX,box.y+50);
  await page.mouse.wheel(0,40);
  assert.equal(await follow.getAttribute('data-follow-paused'),null,'vertical wheel without horizontal movement must not pause Follow');
  await page.mouse.wheel(150,0);
  await until(async()=>await follow.getAttribute('data-follow-paused')!==null,'sideways wheel did not pause Follow');
  await follow.click();
  await until(async()=>await follow.getAttribute('data-follow-paused')===null,'one click did not resume Follow');
  await scroller.evaluate(el=>{el.scrollLeft = el.scrollLeft > 0 ? 0 : 150;});
  await until(async()=>await follow.getAttribute('data-follow-paused')!==null,'scroll event did not pause Follow');
  await follow.click();
  await until(async()=>await follow.getAttribute('data-follow-paused')===null,'one click did not resume Follow');
  await until(async()=>scroller.evaluate(el=>{const r=el.getBoundingClientRect(), h=el.querySelector('[data-placement-playhead]').getBoundingClientRect(); return el.scrollLeft>0 && h.left>=r.left && h.left<=r.right;}),'Follow did not scroll to keep the playhead visible');
  assert.equal(nonGet.length,viewRequests); assert.equal((await state()).revision,viewRevision);
  await page.locator('#transport-form button[value=pause]').click();
  await until(async()=>!(await (await page.request.get(new URL('/api/transport',base).href)).json()).playing,'transport did not pause');
  await until(async()=> (await page.locator('[data-scene-navigation] [data-placement-playback-status]').textContent()).startsWith('Stopped'), 'scene view did not observe the stop');
  await page.locator('#transport-form button[value=play]').click();
  await until(async()=>await follow.getAttribute('data-follow-paused')===null,'scene Follow did not resume');
  // The view steps ran in a narrow window so that 4x overflows; the rest of
  // the suite uses the full width again, fitted.
  await page.setViewportSize({width:1440,height:1000});
  await page.getByRole('button',{name:'Fit song',exact:true}).click();
  await page.evaluate(() => { window.__sceneLive = document.querySelector('#cicada-live'); window.__sceneDocument = document; __sendSceneMIDI(0x90,60,100); });
  await until(() => notes.some(note => note.on === 'true' && note.note === '60'), 'MIDI note-on was not delivered');
  const oldURL = await edit.getAttribute('href');
  assert.equal(new URL(oldURL,base).searchParams.has('songRevision'),false);
  const beforeSelectionURL = page.url();
  if (process.env.CICADA_SCENE_PROOF_DIR) { fs.mkdirSync(process.env.CICADA_SCENE_PROOF_DIR,{recursive:true}); await page.locator('.timeline-heading').nth(1).screenshot({path:path.join(process.env.CICADA_SCENE_PROOF_DIR,'scene-header.png')}); }
  await edit.focus();
  await page.keyboard.press('Enter');
  await until(async () => await page.locator('[data-song-index="1"][data-song-selected]').count() === 1, 'block selection did not open');
  await ready();
  const selected = page.locator('[data-song-index="1"][data-song-selected]');
  assert.equal(await selected.getAttribute('open'), '');
  await until(async () => selected.locator('summary').evaluate(el => { const r=el.getBoundingClientRect(), pane=el.closest('.shell-scroll').getBoundingClientRect(); return r.bottom>pane.top && r.top<pane.bottom && r.bottom>0 && r.top<innerHeight; }), 'selected block controls should be scrolled into view');
  assert.equal(await selected.locator('summary').evaluate(el => document.activeElement === el),true,'keyboard activation should focus the block');
  if (process.env.CICADA_SCENE_PROOF_DIR) await selected.screenshot({path:path.join(process.env.CICADA_SCENE_PROOF_DIR,'scene-controls.png')});
  await page.keyboard.press('Tab');
  assert.equal(await selected.evaluate(el => el.contains(document.activeElement)), true, 'Tab should reach the selected block controls');
  await page.locator('#view-tab-mixer').focus();
  await page.waitForTimeout(150);
  assert.equal(await page.locator('#view-tab-mixer').evaluate(el => document.activeElement === el), true, 'later focus must remain the user’s choice');
  assert.equal(await selected.locator('input[name=target_key]').first().inputValue(), 'alternate_long_scene_name@5+1');
  assert.equal((await state()).source, before.source);
  assert.equal(writes.length, 0);
  assert.equal(await page.locator('.timeline-heading').nth(1).getByRole('button',{name:'alternate_long_scene_name',exact:true}).count(),1);
  assert.equal(await page.evaluate(() => window.__sceneLive === document.querySelector('#cicada-live') && window.__sceneDocument === document), true, 'selection must keep Live mounted');
  assert.equal(page.url(),beforeSelectionURL,'selection must not navigate');
  assert.equal(notes.some(note => note.on === 'false' && note.note === '60'),false,'selection must not release the held note');
  await page.evaluate(() => __sendSceneMIDI(0x80,60,0));
  await until(() => notes.some(note => note.on === 'false' && note.note === '60'), 'MIDI note-off did not reach the same Live engine');
  // An address explicitly naming a block takes precedence over the stored selection.
  await page.locator('.timeline-edit[data-song-index="0"]').click();
  await page.goto(new URL(oldURL,base).href);
  await ready();
  assert.equal(await page.locator('#arrangement details.arrangement-block[open]').count(), 1);
  assert.equal(await page.locator('#song-block-1[open]').count(), 1);
  assert.doesNotMatch(await page.locator('#arrangement').innerText(), /The song changed\./);
  await page.goto(new URL('/studio',base).href);
  await ready();
  // Closing the inspector discards its draft before opening a clip.
  await page.locator('#song-block-1 input[name=bars]').fill('7');
  await page.locator('#song-block-1 summary').click();
  await page.locator('#detail-tab-device').click();
  await page.locator('#arrangement .timeline-clip[data-scene-pattern-open]').first().click();
  await page.locator('#detail-tab-clip[aria-selected="true"]').waitFor();
  assert.doesNotMatch(await page.locator('[data-scene-open-status]').textContent(), /Save or discard/);
  await edit.click();
  assert.equal(await page.locator('#song-block-1 input[name=bars]').inputValue(), '1');
  // Leaving a draft in another block must not block a clip or survive the switch.
  await page.locator('.timeline-edit[data-song-index="0"]').click();
  await page.locator('#song-block-0 input[name=bars]').fill('7');
  await edit.click();
  assert.equal(await page.locator('#song-block-0 input[name=bars]').inputValue(), '4');
  await page.locator('#arrangement .timeline-clip[data-scene-pattern-open]').first().click();
  await until(async () => !(await page.locator('[data-scene-open-status]').textContent()).includes('Save or discard'), 'hidden draft blocked the clip');
  await page.locator('#detail-tab-device').click();
  await page.locator('#detail-tab-device[aria-selected="true"]').waitFor();
  const addPanel = page.locator('details[name="arrange-tools"]').filter({has: page.getByText('Add section', {exact:true})});
  await addPanel.locator('summary').click();
  const addBars = addPanel.locator('input[name=bars]');
  const originalBars = await addBars.inputValue();
  await addBars.fill('9');
  // A press on the strip itself is part of the same task: the panel and its draft stay.
  // Wait for the transport action to finish: a pending action would refuse the clip click below.
  await Promise.all([
    page.waitForResponse(response => response.url().includes('/__actions/transport')),
    page.locator('#arrangement .arrange-tools').getByRole('button', {name: 'Stop listening', exact: true}).click(),
  ]);
  await page.locator('#studio-shell[data-shell-edits=ready]').waitFor();
  await page.waitForTimeout(150);
  assert.equal(await addPanel.evaluate(el => el.open), true, 'a press on the tool strip closed Add section');
  assert.equal(await addBars.inputValue(), '9', 'a press on the tool strip discarded the Add section draft');
  // An outside click closes the floating panel and opens the clip in one gesture.
  // The clip must not be the one already open: a click on the open clip changes nothing.
  const otherClip = await page.evaluate(() => {
    const now = new URLSearchParams(location.search);
    return [...document.querySelectorAll('#arrangement .timeline-clip[data-scene-pattern-open]')].findIndex(link => {
      const next = new URL(link.href).searchParams;
      return ['pattern', 'scene', 'track'].some(name => next.get(name) !== now.get(name));
    });
  });
  assert.notEqual(otherClip, -1, 'the fixture needs a second clip');
  await page.locator('#arrangement .timeline-clip[data-scene-pattern-open]').nth(otherClip).click();
  try {
    await page.locator('#detail-tab-clip[aria-selected="true"]').waitFor({timeout: 8000});
  } catch {
    assert.fail('the clip did not open after an outside click; the page says: ' + JSON.stringify(await page.evaluate(() => ({status: document.querySelector('[data-scene-open-status]')?.textContent, messages: [...document.querySelectorAll('[data-gosx-toast-host] *, .workspace-feedback')].map(node => node.textContent.trim()).filter(Boolean).slice(0, 3), panelOpen: [...document.querySelectorAll('details[name="arrange-tools"]')].map(d => d.open), edited: [...document.querySelectorAll('[data-edited]')].map(node => node.name || node.id).slice(0, 5), address: location.search}))));
  }
  await until(async () => !(await addPanel.evaluate(el => el.open)) && await addBars.inputValue() === originalBars, 'outside click did not discard the draft');
  await addPanel.locator('summary').click();
  await addBars.fill('9');
  await addBars.press('Escape');
  await until(async () => !(await addPanel.evaluate(el => el.open)) && await addBars.inputValue() === originalBars, 'closing Add section did not discard the draft');
  await page.locator('#arrangement .timeline-clip[data-scene-pattern-open]').first().click();
  await until(async () => !(await page.locator('[data-scene-open-status]').textContent()).includes('Save or discard'), 'Add section draft blocked the clip');
  await page.locator('#detail-tab-clip[aria-selected="true"]').waitFor();
  const beforeMute = (await state()).revision;
  await page.locator('#arrangement button[aria-label="Mute bass"]').click();
  await until(async () => (await state()).revision !== beforeMute, 'mute was not saved');
  await ready();
  assert.equal(await page.locator('#arrangement details.arrangement-block[open]').count(),1);
  assert.equal(await page.locator('#song-block-1[open]').count(),1);
  await page.reload();
  await ready();
  assert.equal(await page.locator('#arrangement details.arrangement-block[open]').count(),1);
  assert.equal(await page.locator('#song-block-1[open]').count(),1);
  await page.locator('#song-block-1 summary').click();
  const beforeUnmute = (await state()).revision;
  await page.locator('#arrangement button[aria-label="Mute bass"]').click();
  await until(async () => (await state()).revision !== beforeUnmute, 'unmute was not saved');
  await ready();
  assert.equal(await page.locator('#arrangement details.arrangement-block[open]').count(),0);
  await page.locator('.timeline-edit[data-song-index="1"]').click();
  const muted = await state();
  await page.locator('#song-block-1[open]').getByRole('button',{name:'Move earlier',exact:true}).click();
  await until(async () => (await state()).revision !== muted.revision, 'move was not saved');
  await ready();
  assert.equal(writes.length,1);
  assert.notEqual((await state()).source,before.source);
  assert.equal(await page.locator('[data-song-selected]').count(),0);
  assert.equal(await page.locator('#arrangement details.arrangement-block[open]').count(),0);
  assert.match(await page.locator('#song-block-1 summary').innerText(), /main · bars 2–5/);
  // A copied URL cannot target a different block now occupying the slot.
  await page.goto(new URL(oldURL,base).href);
  await ready();
  assert.equal(await page.locator('[data-song-selected]').count(),0);
  await page.locator('#arrangement [role=status]').filter({hasText:/^The song changed\./}).waitFor();
  await page.locator('.timeline-edit[data-song-index="1"]').click();
  await page.locator('#undo-form button[type=submit]').click();
  await until(async () => (await state()).source === muted.source, 'Undo did not restore the song');
  await ready();
  assert.equal(await page.locator('#arrangement details.arrangement-block[open]').count(),0);
  assert.equal(await page.locator('#arrangement [data-scene-open-status]').textContent(), 'The song changed. Choose Edit on a block to continue editing it.');
  await page.locator('.timeline-edit[data-song-index="1"]').click();
  assert.equal(await page.locator('#song-block-1[open]').count(), 1);
  assert.equal(await page.locator('#arrangement [data-scene-open-status]').textContent(), '');
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),true);
  assert.deepEqual(errors,[]);
});
