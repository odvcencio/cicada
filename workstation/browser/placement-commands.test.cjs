/* Real Mac placement commands against a fresh, disposable Studio process.
 * node workstation/browser/placement-commands.test.cjs --write-fixture /tmp/keys.cicada
 * CICADA_STUDIO_URL=... node --test workstation/browser/placement-commands.test.cjs
 * CICADA_PLAYWRIGHT_MODULE / CICADA_CHROME_PATH select local browser tooling.
 */
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');
const test = require('node:test');

const fixture = `// cicada-placement-commands-test: disposable fixture
cicada 2
title "Placement keyboard checks"
track bass acid {}
pattern pulse { 1 . 5 . }
arrange {
  place one bass pulse { at = @1.1.1 length = 4bars }
  place two bass pulse { at = @5.1.1 length = 4bars }
}
`;
if (process.argv[2] === '--write-fixture') {
  assert.ok(process.argv[3], 'supply a new disposable fixture path');
  fs.writeFileSync(process.argv[3], fixture, {flag:'wx'});
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
  const end = Date.now() + 15000;
  while (Date.now() < end) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 30));
  }
  assert.fail(message);
}
const actionValues = async request => request.headers()['content-type']?.includes('application/json')
  ? request.postDataJSON() : Object.fromEntries(await new Response(request.postDataBuffer(), {headers:{'Content-Type':request.headers()['content-type']}}).formData());

test('Mac Arrange commands mutate real placements, retain Undo and respect focused editors', {timeout:120000}, async () => {
  const base = process.env.CICADA_STUDIO_URL;
  assert.ok(base, 'set CICADA_STUDIO_URL to a fresh disposable Studio');
  assert.ok(['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
  const browser = await playwright().chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox']});
  const context = await browser.newContext({viewport:{width:1600,height:1000}});
  const page = await context.newPage();
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'platform', {get:()=>'MacIntel'});
    Object.defineProperty(navigator, 'userAgentData', {get:()=>({platform:'macOS'})});
  });
  const posts = [], errors = [];
  page.on('request', request => { if (request.method()==='POST' && new URL(request.url()).pathname.startsWith('/__actions/')) posts.push(request); });
  page.on('pageerror', error => errors.push(error.message));
  const state = async () => (await page.request.get(new URL('/api/state',base).href)).json();
  const transport = async () => (await page.request.get(new URL('/api/transport',base).href)).json();
  const ready = async () => {
    await page.waitForFunction(()=>!document.querySelector('form[data-gosx-form-state=pending]'));
    await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
    await until(async()=>await page.locator('#undo-form input[name=revision]').inputValue()===(await state()).revision, 'managed navigation did not refresh revisions');
  };
  const visit = async search => { await page.goto(new URL('/studio'+search,base).href); await ready(); };
  const block = name => page.locator(`[data-placement-block="${name}"]`);
  const select = async name => {
    await page.getByRole('link',{name:'Edit placement '+name,exact:true}).click();
    await until(async()=>new URL(page.url()).searchParams.get('placement')===name && await block(name).getAttribute('data-placement-selected')==='true','placement selection did not settle');
    await ready();
  };
  const focusArrange = async () => { await page.locator('#view-tab-arrange').focus(); };
  const key = async chord => { await focusArrange(); await page.keyboard.press(chord); };
  const sourceIs = async source => until(async()=>(await state()).source===source, 'source did not match the expected saved score');
  const undo = async source => { await key('Meta+z'); await sourceIs(source); await ready(); };
  const noPost = async (focus, chords) => {
    await focus(); const before=posts.length;
    for (const chord of chords) await page.keyboard.press(chord);
    await page.waitForTimeout(150);
    assert.equal(posts.length,before,'guarded keys submitted an action: '+JSON.stringify({
      sent:posts.slice(before).map(request=>new URL(request.url()).pathname),
      focus:await page.evaluate(()=>({tag:document.activeElement?.tagName,id:document.activeElement?.id,classes:document.activeElement?.getAttribute('class'),detail:!!document.activeElement?.closest('#studio-detail'),available:window.__gosx.commands.available('edit.duplicate')})),
    }));
  };
  try {
    await visit('?placement=one');
    assert.equal((await state()).source,fixture,'runner refuses an unmarked or modified score');
    const before = (await state()).revision;
    await key('Meta+d'); await block('one-copy').waitFor(); await ready();
    assert.equal(await block('one-copy').getAttribute('data-placement-at'),'30720','duplicate appends after the last placement on its track');
    const duplicateSource=(await state()).source;
    assert.notEqual((await state()).revision,before);
    const duplicate=posts.find(request=>new URL(request.url()).pathname==='/__actions/arrange');
    const duplicateData=await actionValues(duplicate);
    assert.equal(duplicateData.action,'duplicate');
    assert.equal(duplicateData.name,'one');
    assert.equal(duplicateData.revision,before);
    assert.ok(duplicateData.csrf_token,'keyboard mutation retains the rendered CSRF token');
    await undo(fixture);
    await key('Meta+Shift+z'); await sourceIs(duplicateSource); await ready();
    await undo(fixture);

    // A placement link performs real managed navigation and remounts command
    // bindings with the current revision, rather than reusing stale forms.
    await select('two');
    await key('Backspace'); await block('two').waitFor({state:'detached'}); await ready();
    const oneOnly=(await state()).source;
    assert.ok(!oneOnly.includes('place two '));
    assert.equal(await page.locator('[data-placement-command=delete]').isDisabled(),true);
    await noPost(focusArrange,['Delete','Backspace']);
    assert.equal((await state()).source,oneOnly,'the last placement must stay intact');
    await undo(fixture);
    await select('two');
    await key('Delete'); await block('two').waitFor({state:'detached'}); await ready(); await undo(fixture);

    await select('one');
    await key('Meta+l');
    await until(async()=>{ const t=await transport(); return t.playing && t.listening?.kind==='placement' && t.listening.name==='one' && t.listening.mode==='context' && t.listening.loop; },'selected placement did not loop in song context');
    await ready(); assert.equal((await state()).source,fixture,'looping must not edit the score');
    await page.locator('[data-gosx-command="transport.loop"]').click();
    await until(async()=>!(await transport()).playing,'toolbar Loop did not stop the selected loop'); await ready();
    await page.locator('[data-gosx-command="transport.loop"]').click();
    await until(async()=>(await transport()).playing && (await transport()).listening?.loop,'toolbar Loop did not start the selected loop'); await ready();
    await key('Meta+l'); await until(async()=>!(await transport()).playing,'Cmd-L did not toggle the selected loop off'); await ready();

    const chords=['Meta+d','Meta+l','Delete','Backspace'];
    const code=page.locator('#score textarea').first();
    await noPost(()=>code.focus(),chords); await code.fill(fixture);
    const start=page.locator('[data-placement-inspector=one] input[name=at]').first();
    const startValue=await start.inputValue();
    await noPost(()=>start.focus(),chords); await start.fill(startValue);
    await code.fill(fixture+'// unsaved vocal idea\n');
    await noPost(focusArrange,chords);
    assert.equal((await state()).source,fixture,'placement commands discarded an unsaved Code draft');
    await code.fill(fixture);
    await focusArrange();
    assert.equal(await page.evaluate(()=>window.__gosx.commands.available('edit.duplicate')),true,'restored drafts must leave Arrange commands available');
    const roll=page.locator('#detail-panel-clip .roll-cell').first();
    if (await roll.count()) {
      await roll.focus();
      assert.equal(await page.evaluate(()=>window.__gosx.commands.available('edit.delete')),false,'placement delete must not consume piano-roll keys');
      await noPost(()=>roll.focus(),['Meta+d','Meta+l']);
    }
    await page.locator('#detail-tab-automation').click();
    await page.locator('#detail-panel-automation').waitFor({state:'visible'}); await ready();
    await noPost(async()=>{
      const curve=page.locator('#detail-panel-automation [data-automation-curve]');
      await curve.focus();
      assert.equal(await curve.evaluate(node=>node===document.activeElement),true,'Automation graph must actually hold keyboard focus');
    },chords);
    await page.locator('#detail-tab-device').click();
    await page.locator('#detail-panel-device').waitFor({state:'visible'}); await ready();
    await noPost(()=>page.locator('#detail-tab-device').focus(),chords);
    await noPost(()=>page.locator('#studio-browser summary').first().focus(),chords);
    await visit('?tab=session&placement=one');
    await noPost(()=>page.locator('#view-tab-session').focus(),chords);
    await visit('?placement=missing'); await noPost(focusArrange,chords);
    assert.equal((await state()).source,fixture);

    // A copied or stale form still goes through the normal revision conflict
    // path. Command dispatch must never replace its revision with a fresh one.
    await visit('?placement=one');
    await page.locator('[data-placement-inspector=one] [data-placement-command=duplicate]').evaluate(button=>{button.closest('form').querySelector('[name=revision]').value='stale-revision';});
    const rejected=page.waitForResponse(response=>response.request().method()==='POST'&&new URL(response.url()).pathname==='/__actions/arrange');
    await key('Meta+d'); await rejected;
    assert.equal((await state()).source,fixture,'stale keyboard edit changed the score');
    assert.equal((await actionValues(posts.at(-1))).revision,'stale-revision');
    assert.deepEqual(errors,[]);
  } finally {
    await context.close(); await browser.close();
  }
});
