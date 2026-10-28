/*
 * Visual placement and Session freshness regression against a disposable Studio.
 * node workstation/browser/placements.test.cjs --write-fixture /tmp/placements.cicada
 * ./build/cicada studio /tmp/placements.cicada --audio null --listen 127.0.0.1:8191
 * CICADA_STUDIO_URL=http://127.0.0.1:8191 node --test workstation/browser/placements.test.cjs
 * CICADA_PLAYWRIGHT_MODULE and CICADA_CHROME_PATH select local browser tooling.
 * CICADA_PLACEMENT_PROOF_DIR optionally saves desktop/mobile screenshots and results.
 */
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');
const test = require('node:test');

const marker = 'cicada-placement-test: disposable fixture';
const fixture = `// ${marker}\n${fs.readFileSync(path.join(__dirname, '../../examples/songs/tidal-glass.cicada'), 'utf8')}`;
if (process.argv[2] === '--write-fixture') {
  assert.ok(process.argv[3], 'supply a new disposable fixture path');
  fs.writeFileSync(process.argv[3], fixture, {flag:'wx'});
  console.log(`Wrote ${path.resolve(process.argv[3])}`);
  process.exit(0);
}

function playwright() {
  const location = process.env.CICADA_PLAYWRIGHT_MODULE;
  if (!location) return require('playwright');
  try { const loaded = require(path.resolve(location)); if (loaded.chromium) return loaded; }
  catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
  return createRequire(path.resolve(location))('playwright');
}
async function until(predicate, message) {
  const deadline = Date.now()+15000;
  while (Date.now()<deadline) {
    if (await predicate()) return;
    await new Promise(resolve=>setTimeout(resolve,30));
  }
  assert.fail(message);
}

// The Studio engine is one Go/WebAssembly program. When goroutines made Go-to-JavaScript calls that ran Go code inside
// the call (a Promise executor, a dispatched event), Go's runtime could lose track of which goroutine to resume. The
// engine then stopped with "traceback did not unwind completely", and the workspace stayed busy ("Saving…") for good.
// This counts how deeply the engine's calls into JavaScript nest. The Studio loops and the refresh job must keep it at
// one. The GoSX TinyGo runtime also has a gojs namespace; it is left alone (it imports wasi_snapshot_preview1).
const reentryProbe = `(() => {
  const probe = window.__goReentry = {hooked: false, max: 0, nested: [], reset() { this.max = 0; this.nested = []; }};
  for (const name of ['instantiate', 'instantiateStreaming']) {
    const original = WebAssembly[name];
    if (typeof original !== 'function') continue;
    WebAssembly[name] = function (source, imports) {
      if (imports && imports.gojs && !imports.wasi_snapshot_preview1) {
        let depth = 0;
        for (const key of ['syscall/js.valueCall', 'syscall/js.valueNew', 'syscall/js.valueInvoke']) {
          const call = imports.gojs[key];
          if (typeof call !== 'function' || call.__reentryProbe) continue;
          probe.hooked = true;
          imports.gojs[key] = Object.assign(function () {
            depth++;
            if (depth > probe.max) probe.max = depth;
            if (depth > 1 && probe.nested.length < 12) probe.nested.push(key.slice('syscall/js.'.length));
            try { return call.apply(this, arguments); } finally { depth--; }
          }, {__reentryProbe: true});
        }
      }
      return original.apply(this, arguments);
    };
  }
})();`;

test('placements edit visually and Session follows saved Score without reloading', {timeout:180000}, async t => {
  const base = process.env.CICADA_STUDIO_URL;
  assert.ok(base, 'set CICADA_STUDIO_URL to a disposable Studio');
  assert.ok(['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
  const browser = await playwright().chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox']});
  const context = await browser.newContext({viewport:{width:1440,height:1000}, reducedMotion:'reduce'});
  await context.addInitScript(reentryProbe);
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  const errors=[], checks=[], goOutput=[], polls={};
  // A stopped engine never clears the busy flag. Say so at once, with what Go printed, instead of timing out.
  let engineStopped; const engineStop=new Promise((_,reject)=>{ engineStopped=reject; }); engineStop.catch(()=>{});
  let engineStopReported=false;
  const reportEngineStop=reason=>{ if(engineStopReported) return; engineStopReported=true; setTimeout(()=>engineStopped(new Error(`the Go engine stopped: ${reason}\n${goOutput.join('\n')}`)),300); };
  page.on('console', message=>{ const text=message.text(); if(/^(fatal error|panic):/.test(text)) reportEngineStop(text.slice(0,200)); if((goOutput.length||/^(fatal error|panic):/.test(text)) && goOutput.length<60) goOutput.push(text.slice(0,300)); });
  page.on('pageerror', error=>{ errors.push(error.message); if(/Go program has already exited/.test(error.message)) reportEngineStop(error.message); });
  page.on('request', request=>{ const name=new URL(request.url()).pathname; if(/^\/api\/(capture|transport|automation\/recording)$/.test(name)) polls[name]=(polls[name]||0)+1; });
  const state = async () => (await page.request.get(new URL('/api/state',base).href)).json();
  const transport = async () => (await page.request.get(new URL('/api/transport',base).href)).json();
  const idle = async () => { const settled=page.locator('#cicada-workspace[aria-busy=true]').waitFor({state:'hidden'}); settled.catch(()=>{}); await Promise.race([settled,engineStop]); };
  const reentry = async label => {
    const seen = await page.evaluate(()=>({hooked:window.__goReentry.hooked,max:window.__goReentry.max,nested:window.__goReentry.nested}));
    assert.equal(seen.hooked,true,'the Go call counter was not installed in the engine');
    assert.ok(seen.max<=1,`${label}: Go was re-entered from inside a call to JavaScript (depth ${seen.max}: ${seen.nested.join(', ')})`);
  };
  // Waits until each Studio loop has polled twice since this call, so a quiet check really covers the loops.
  const pollsRan = async () => { const from={...polls}; const names=['/api/capture','/api/transport','/api/automation/recording']; await until(async()=>names.every(name=>(polls[name]||0)-(from[name]||0)>=2),'the Studio polls did not run'); };
  const ready = async target => { await target.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor(); };
  const block = (target,id='far-in') => target.locator(`[data-placement-block="${id}"]`);
  const at = async (ticks,target=page) => { await until(async()=>Number(await block(target).getAttribute('data-placement-at'))===ticks,`far-in did not move to ${ticks}`); await idle(); };
  const undo = async () => { await page.getByRole('button',{name:'Undo',exact:true}).click(); await until(async()=>(await state()).source===fixture,'Undo did not restore the exact fixture'); await at(16320); };
  const far = page.locator('[data-placement-inspector="far-in"]');
  const selectFar = async (watchReentry=false) => {
    await idle();
    await page.getByRole('link',{name:'Edit placement far-in',exact:true}).click();
    // Focusing the link runs a Go focusin listener inside the click. That one nest is by design; count from here on.
    if (watchReentry) await page.evaluate(()=>window.__goReentry.reset());
    await until(async()=>new URL(page.url()).searchParams.get('placement')==='far-in' && await block(page).getAttribute('data-placement-selected')==='true' && await far.getAttribute('open')!==null,'placement selection did not settle');
    await until(async()=>await page.locator('#cicada-workspace input[name="revision"]').first().inputValue()===(await state()).revision,'selection did not refresh the saved revision');
    await idle();
    if (watchReentry) await reentry('the refresh job that placement selection starts');
  };
  const drag = async (selector,dx,cancel=false) => {
    const target=page.locator(selector);
    await target.scrollIntoViewIfNeeded();
    const box=await target.boundingBox();
    assert.ok(box,'drag target has no box');
    const x=box.x+box.width/2,y=box.y+box.height/2;
    await page.mouse.move(x,y); await page.mouse.down(); await page.mouse.move(x+dx,y,{steps:6});
    if (cancel) await page.keyboard.press('Escape');
    await page.mouse.up();
  };
  try {
    await page.goto(new URL('/?panel=session',base).href,{waitUntil:'domcontentloaded'}); await ready(page);
    await page.evaluate(()=>window.__goReentry.reset()); // mounting the engines nests by design
    assert.equal((await state()).source,fixture,'runner refuses an unmarked or modified score');
    assert.equal(await page.locator('meta[name=gosx-page-cache]').getAttribute('content'),'no-store');
    assert.equal(await page.locator('[data-placement-block]').count(),9);
    await at(16320); await pollsRan(); await reentry('Studio polling with no input');
    await selectFar(true);
    assert.equal(await far.getByLabel('Start',{exact:true}).inputValue(),'@5.2.1','placement forms share another region’s draft identity');

    await page.getByRole('link',{name:'Score',exact:true}).click(); await page.getByRole('textbox',{name:'Cicada score',exact:true}).waitFor();
    await page.getByRole('link',{name:'Session',exact:true}).click(); await selectFar();
    await page.getByRole('link',{name:'Score',exact:true}).click();
    await page.getByRole('textbox',{name:'Cicada score',exact:true}).fill(fixture.replace('at = @5.2.1 length = 4bars','at = @5.3.1 length = 4bars'));
    await page.getByRole('button',{name:'Save file',exact:true}).click();
    await until(async()=>(await state()).source.includes('at = @5.3.1 length = 4bars'),'Score save did not commit'); await idle();
    await page.getByRole('link',{name:'Session',exact:true}).click(); await at(17280); await undo();
    checks.push('visited Session refreshes after a Score save without reload');

    await selectFar();
    await far.getByLabel('Start',{exact:true}).fill('@5.3.1'); await far.getByLabel('Length',{exact:true}).fill('3bars');
    await far.getByRole('button',{name:'Update placement',exact:true}).click(); await at(17280);
    assert.equal(await block(page).getAttribute('data-placement-length'),'11520'); await undo();
    checks.push('exact move/trim in one Undo entry');
    await selectFar();
    await drag('[data-placement-block="far-in"] .placement-select',12); await at(17280); await undo();
    checks.push('pointer move by one beat');
    await page.getByLabel('Timeline zoom',{exact:true}).selectOption('192');
    await until(async()=>await page.locator('.placement-timeline').getAttribute('data-placement-zoom')==='192','zoom did not settle');
    await drag('[data-placement-block="far-in"] .placement-select',48); await at(17280); await undo();
    await page.getByLabel('Timeline zoom',{exact:true}).selectOption('48');
    await until(async()=>await page.locator('.placement-timeline').getAttribute('data-placement-zoom')==='48','zoom reset did not settle');
    checks.push('a four-times-zoomed pointer move still saves one beat');
    await drag('[data-placement-block="far-in"] [data-placement-resize]',-12);
    await until(async()=>await block(page).getAttribute('data-placement-length')==='14400','pointer resize did not trim a beat'); await idle(); await undo();
    checks.push('pointer resize by one beat');
    await drag('[data-placement-block="far-in"] .placement-select',12,true);
    assert.equal((await state()).source,fixture); assert.equal(await block(page).getAttribute('data-placement-at'),'16320');
    checks.push('Escape cancels a drag without a write');

    await page.getByRole('link',{name:'Edit placement far-in',exact:true}).focus(); await page.keyboard.press('ArrowRight'); await at(16560);
    await page.keyboard.press('Shift+ArrowRight'); await at(20400);
    await page.getByRole('button',{name:'Undo',exact:true}).click(); await at(16560); await undo();
    await page.getByRole('button',{name:'Resize placement far-in',exact:true}).focus(); await page.keyboard.press('Shift+ArrowLeft');
    await until(async()=>await block(page).getAttribute('data-placement-length')==='11520','keyboard resize did not trim a bar'); await idle(); await undo();
    checks.push('keyboard move and resize in sixteenth/bar increments');

    await selectFar(); await far.getByLabel('Start',{exact:true}).fill('@15.1.1');
    await far.getByRole('button',{name:'Update placement',exact:true}).click();
    await until(async()=>/overlap/i.test(await page.locator('[data-workspace-status]').textContent()),'overlap was not rejected');
    assert.equal((await state()).source,fixture); assert.equal(await far.getByLabel('Start',{exact:true}).inputValue(),'@15.1.1');
    await far.getByLabel('Start',{exact:true}).fill('@5.3.1'); await far.getByRole('button',{name:'Update placement',exact:true}).click(); await at(17280); await undo();
    checks.push('invalid overlap preserves source and draft; corrected edit succeeds');

    await selectFar(); await far.getByText('Make a copy',{exact:true}).click();
    await far.getByRole('button',{name:'Duplicate placement',exact:true}).click();
    await block(page,'far-in-copy').waitFor(); await idle(); assert.equal(await page.locator('[data-placement-block]').count(),10);
    await page.getByRole('link',{name:'Edit placement far-in-copy',exact:true}).click(); await idle();
    await page.locator('[data-placement-inspector="far-in-copy"]').getByRole('button',{name:'Remove placement',exact:true}).click();
    await block(page,'far-in-copy').waitFor({state:'detached'}); await idle();
    await page.getByRole('button',{name:'Undo',exact:true}).click(); await block(page,'far-in-copy').waitFor(); await idle(); await undo();
    checks.push('duplicate/remove and Undo retain placement identity');

    await page.getByText('Add a placement',{exact:true}).click();
    const add=page.locator('.placement-management').filter({has:page.getByRole('button',{name:'Add placement',exact:true})});
    await add.getByLabel('Placement name',{exact:true}).fill('new-ending'); await add.locator('select[name=track]').selectOption('far');
    await add.locator('select[name=content]').selectOption('return');
    await add.getByRole('button',{name:'Add placement',exact:true}).click(); await block(page,'new-ending').waitFor(); await idle(); await undo();
    checks.push('add a placement with existing track/content');
    await page.getByText('Edit markers',{exact:true}).click();
    const current=page.getByLabel('current',{exact:true}); await current.fill('@6.1.1');
    await current.locator('..').locator('..').getByRole('button',{name:'Move marker',exact:true}).click();
    await until(async()=>/current @6.1.1/.test(await page.locator('[aria-label="Arrangement markers"]').textContent()),'marker did not move'); await idle(); await undo();
    checks.push('marker edits and Undo');

    // Listening controls use the native player and do not add score edits.
    await selectFar();
    const listen=page.locator('[aria-label="Session listening"]');
    await listen.getByLabel('Section',{exact:true}).selectOption('current');
    await listen.getByRole('button',{name:'Loop section',exact:true}).click();
    await until(async()=>{const l=(await transport()).listening;return l?.kind==='section'&&l.name==='current'&&l.startTick===15360&&l.endTick===30720&&l.loop;},'section did not loop to its next marker'); await idle();
    assert.equal((await state()).source,fixture);
    await listen.getByText('Loop an exact range',{exact:true}).click();
    await listen.getByLabel('Loop start',{exact:true}).fill('@5.2.1'); await listen.getByLabel('Loop end',{exact:true}).fill('@5.2.2');
    await listen.getByRole('button',{name:'Loop range',exact:true}).click();
    await until(async()=>{const l=(await transport()).listening;return l?.startTick===16320&&l.endTick===16560&&l.cycles>=2;},'short pickup range did not repeat'); await idle();
    const listeningRoot=await page.locator('#cicada-workspace').evaluate(node=>{window.__listeningRoot=node;return true;}); assert.ok(listeningRoot);
    await listen.getByLabel('Loop end',{exact:true}).fill('@1.1.1'); await listen.getByRole('button',{name:'Loop range',exact:true}).click();
    await until(async()=>/choose a start/i.test(await page.locator('[data-workspace-status]').textContent()),'invalid loop end was accepted');
    assert.equal((await transport()).listening.endTick,16560); assert.equal(await listen.getByLabel('Loop end',{exact:true}).inputValue(),'@1.1.1');
    await listen.getByLabel('Loop end',{exact:true}).fill('@5.2.2'); await listen.getByRole('button',{name:'Loop range',exact:true}).click(); await idle();
    assert.equal(await page.locator('#cicada-workspace').evaluate(node=>node===window.__listeningRoot),true);
    checks.push('marker sections and exact pickup ranges loop; invalid ranges retain playback and drafts');

    await far.getByRole('button',{name:'Loop solo',exact:true}).click();
    await until(async()=>{const l=(await transport()).listening;return l?.name==='far-in'&&l.mode==='solo'&&l.loop;},'placement solo loop did not start'); await idle();
    await far.getByLabel('Length',{exact:true}).fill('1/4'); await far.getByRole('button',{name:'Update placement',exact:true}).click(); await idle();
    await until(async()=>{const l=(await transport()).listening;return l?.startTick===16320&&l.endTick===17280&&l.cycles>=1;},'solo loop did not take the placement trim');
    await far.getByLabel('Start',{exact:true}).fill('@5.3.1'); await far.getByRole('button',{name:'Update placement',exact:true}).click(); await at(17280);
    await until(async()=>{const l=(await transport()).listening;return l?.startTick===17280&&l.endTick===18240;},'solo loop did not follow the moved placement');
    await page.getByRole('button',{name:'Undo',exact:true}).click(); await at(16320);
    await until(async()=>(await transport()).listening.startTick===16320,'loop did not follow Undo');
    checks.push('solo placement loops follow live trims, moves, and Undo');
    const shortSource=(await state()).source;
    await far.getByRole('button',{name:'Audition solo',exact:true}).click();
    await until(async()=>{const t=await transport();return t.listening?.name==='far-in'&&t.listening.mode==='solo'&&!t.listening.loop;},'once-through solo audition did not start');
    await until(async()=>{const t=await transport();return !t.playing&&t.listening?.completed;},'solo audition did not stop at its exclusive end');
    assert.equal((await state()).source,shortSource);
    await far.getByRole('button',{name:'Audition in song',exact:true}).click();
    await until(async()=>{const t=await transport();return t.listening?.mode==='context'&&!t.listening.loop;},'song-context audition did not start');
    await until(async()=>{const t=await transport();return !t.playing&&t.listening?.completed;},'context audition did not finish');
    await listen.getByRole('button',{name:'Play full song',exact:true}).click();
    await until(async()=>{const t=await transport();return t.playing&&!t.listening;},'full song did not restore normal playback'); await idle();
    assert.equal((await state()).source,shortSource);
    await undo();
    await listen.getByRole('button',{name:'Stop listening',exact:true}).click(); await until(async()=>!(await transport()).playing,'listening did not stop'); await idle();
    checks.push('solo/context auditions finish once; full-song playback restores every part without source writes');

    const native=await browser.newContext({javaScriptEnabled:false,storageState:await context.storageState()});
    const nativePage=await native.newPage(); await nativePage.goto(new URL('/?panel=session&placement=far-in',base).href);
    const nativeFar=nativePage.locator('[data-placement-inspector="far-in"]'); await nativeFar.getByLabel('Start',{exact:true}).fill('@5.3.1');
    await nativeFar.getByRole('button',{name:'Update placement',exact:true}).click();
    await until(async()=>await block(nativePage).getAttribute('data-placement-at')==='17280','native form did not refresh');
    assert.match(nativePage.url(),/placement=far-in/);
    await nativePage.getByRole('button',{name:'Undo',exact:true}).click(); await until(async()=>(await state()).source===fixture,'native Undo did not restore score');
    await nativePage.getByRole('link',{name:'Edit placement far-in',exact:true}).click();
    await nativePage.locator('[data-placement-inspector="far-in"]').getByRole('button',{name:'Loop solo',exact:true}).click();
    await until(async()=>(await transport()).listening?.mode==='solo','native placement audition failed');
    await nativePage.getByRole('button',{name:'Stop listening',exact:true}).click(); await until(async()=>!(await transport()).playing,'native listening did not stop'); await native.close();
    checks.push('native forms work without JavaScript and preserve selection');

    await page.getByRole('button',{name:'Play',exact:true}).click(); await until(async()=>(await transport()).playing,'transport did not start');
    await page.getByRole('link',{name:'Score',exact:true}).click(); await page.getByRole('textbox',{name:'Cicada score',exact:true}).waitFor();
    await page.getByRole('link',{name:'Session',exact:true}).click(); await block(page).waitFor();
    await ready(page);
    assert.equal(await page.getByRole('button',{name:'Resize placement far-in',exact:true}).isVisible(),true,'managed navigation hid the resize handle');
    assert.equal((await transport()).playing,true,'panel navigation stopped playback');
    await page.getByRole('button',{name:'Stop',exact:true}).click(); await until(async()=>!(await transport()).playing,'transport did not stop'); await idle();
    checks.push('Score/Session navigation preserves running transport');
    await selectFar(); await page.locator('.masthead').scrollIntoViewIfNeeded();
    const desktop=await page.evaluate(()=>({width:innerWidth,height:innerHeight,bodyWidth:document.documentElement.scrollWidth}));
    assert.equal(desktop.bodyWidth,desktop.width);

    const mobileContext=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,deviceScaleFactor:1,reducedMotion:'reduce',storageState:await context.storageState()});
    const mobile=await mobileContext.newPage(); await mobile.goto(new URL('/?panel=session&placement=far-in',base).href,{waitUntil:'domcontentloaded'}); await ready(mobile);
    await mobile.getByRole('link',{name:'Edit placement far-in',exact:true}).tap(); await ready(mobile);
    const mobileFar=mobile.locator('[data-placement-inspector="far-in"]');
    await mobileFar.getByRole('button',{name:'Loop in song',exact:true}).tap(); await until(async()=>(await transport()).listening?.mode==='context','mobile listening did not start');
    await mobile.getByRole('button',{name:'Stop listening',exact:true}).tap(); await until(async()=>!(await transport()).playing,'mobile listening did not stop');
    await mobileFar.getByLabel('Start',{exact:true}).fill('@5.3.1'); await mobileFar.getByRole('button',{name:'Update placement',exact:true}).tap(); await at(17280,mobile);
    await mobile.getByRole('button',{name:'Undo',exact:true}).tap(); await at(16320,mobile);
    await until(async()=>(await state()).source===fixture,'mobile Undo did not restore the fixture');
    checks.push('touch selection, exact edit, and Undo on mobile');
    const narrow=await mobile.evaluate(()=>{const scroller=document.querySelector('.placement-scroll');return {width:innerWidth,height:innerHeight,bodyWidth:document.documentElement.scrollWidth,visible:scroller.clientWidth,content:scroller.scrollWidth};});
    assert.equal(narrow.bodyWidth,narrow.width); assert.ok(narrow.content>narrow.visible);
    const beforeScroll=await mobile.locator('.placement-scroll').evaluate(node=>node.scrollLeft);
    await mobile.locator('.placement-scroll').hover(); await mobile.mouse.wheel(400,0); await until(async()=>await mobile.locator('.placement-scroll').evaluate(node=>node.scrollLeft)>beforeScroll,'mobile timeline did not scroll');
    checks.push('desktop/mobile layouts have no page overflow; timeline scrolls');
    const proof=process.env.CICADA_PLACEMENT_PROOF_DIR;
    if (proof) {
      fs.mkdirSync(proof,{recursive:true});
      await selectFar();
      await far.getByRole('button',{name:'Loop solo',exact:true}).click(); await until(async()=>(await transport()).listening?.mode==='solo','screenshot loop did not start');
      await until(async()=>/Loop: far-in/.test(await page.locator('.listening-status').textContent()),'desktop loop status did not update');
      await until(async()=>/Loop: far-in/.test(await mobile.locator('.listening-status').textContent()),'mobile loop status did not update');
      await mobile.locator('.placement-scroll').evaluate(node=>{node.scrollLeft=node.querySelector('[data-placement-block="far-in"]').offsetLeft;});
      await page.screenshot({path:path.join(proof,'placement-editor-desktop.png'),fullPage:true});
      await mobile.screenshot({path:path.join(proof,'placement-editor-mobile.png'),fullPage:true});
      const manifest=JSON.parse(fs.readFileSync(path.join(__dirname,'../../build/workstation/build.json'),'utf8'));
      fs.writeFileSync(path.join(proof,'placement-editor-proof.json'),JSON.stringify({build:process.env.CICADA_PLACEMENT_BUILD||'make build',manifest,browser:browser.version(),desktop,mobile:narrow,checks,errors},null,2));
    }
    assert.deepEqual(errors,[]); assert.equal((await state()).source,fixture);
    t.diagnostic(JSON.stringify({desktop,mobile:narrow,checks}));
  } catch (error) {
    const proof=process.env.CICADA_PLACEMENT_PROOF_DIR;
    if (proof) {
      fs.mkdirSync(proof,{recursive:true});
      await page.screenshot({path:path.join(proof,'placement-editor-failure.png'),fullPage:true});
      fs.writeFileSync(path.join(proof,'placement-editor-failure.html'),await page.content());
      if (goOutput.length) fs.writeFileSync(path.join(proof,'placement-editor-go-output.txt'),goOutput.join('\n')+'\n');
    }
    throw error;
  } finally {
    try { const form=page.locator('form[action="/__actions/transport"]'); if(await form.count()) { await form.getByRole('button',{name:'Stop',exact:true}).click({timeout:2000}); } } catch (_) {}
    await browser.close();
  }
});
