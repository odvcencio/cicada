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
const {liveReleaseBeacon} = require('./live-release.cjs');

const fixture = `// cicada-placement-commands-test: disposable fixture
cicada 2
title "Placement keyboard checks"
track bass acid {}
track lead acid {}
pattern pulse { 1 . 5 . }
pattern answer { 2 . 4 . }
arrange {
  place one bass pulse { at = @1.1.1 length = 4bars }
  place two lead answer { at = @5.1.1 length = 4bars }
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

test('Studio views, Record entry and Mac placement commands retain drafts and Undo', {timeout:120000}, async t => {
  const base = process.env.CICADA_STUDIO_URL;
  assert.ok(base, 'set CICADA_STUDIO_URL to a fresh disposable Studio');
  assert.ok(['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
  const browser = await playwright().chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox']});
  const context = await browser.newContext({viewport:{width:1600,height:1000}});
  const page = await context.newPage();
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'platform', {get:()=>'MacIntel'});
    Object.defineProperty(navigator, 'userAgentData', {get:()=>({platform:'macOS'})});
    // Exercise the real Web MIDI adapter with an in-memory input, as the
    // portable MIDI suite does; the Studio and Live service remain real.
    const access=new EventTarget(), port=new EventTarget();
    Object.assign(port,{id:'held-note-test',name:'Test MIDI keyboard',state:'connected'});
    access.inputs=new Map([[port.id,port]]);
    window.__clipTestMIDI=(...bytes)=>{const event=new Event('midimessage');Object.defineProperty(event,'data',{value:new Uint8Array(bytes)});port.dispatchEvent(event)};
    Object.defineProperty(navigator,'requestMIDIAccess',{configurable:true,value:()=>Promise.resolve(access)});
  });
  const posts = [], navigations = [], errors = [], liveReleases = [];
  const csrfTokens = new Set();
  page.on('request', request => {
    const pathname=new URL(request.url()).pathname;
    if (request.method()==='POST' && pathname.startsWith('/__actions/') && pathname!=='/__actions/live' && !/^\/__actions\/(source|note)-checkpoint$/.test(pathname)) posts.push(request);
    if (request.isNavigationRequest() || request.method()==='GET' && pathname==='/studio') navigations.push(request);
  });
  page.on('pageerror', error => errors.push(error.message));
  // Interception exposes Blob Beacon bodies that Chromium's request event
  // can omit. All non-release Live actions still count as score commands.
  await page.route('**/__actions/live', async route => {
    const release=liveReleaseBeacon(route.request(),csrfTokens);
    if(release) liveReleases.push(release);
    else if(route.request().method()==='POST') posts.push(route.request());
    await route.continue();
  });
  const state = async () => (await page.request.get(new URL('/api/state',base).href)).json();
  const transport = async () => (await page.request.get(new URL('/api/transport',base).href)).json();
  const ready = async () => {
    await page.waitForFunction(()=>!document.querySelector('form[data-gosx-form-state=pending]'));
    await page.waitForFunction(()=>window.__gosx?.ready===true);
    await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
    csrfTokens.add(await page.locator('input[name="csrf_token"]').first().inputValue());
    await until(async()=>await page.locator('#undo-form input[name=revision]').inputValue()===(await state()).revision, 'managed navigation did not refresh revisions');
  };
  const visit = async search => { await page.goto(new URL('/studio'+search,base).href); await ready(); };
  const block = name => page.locator(`[data-placement-block="${name}"]`);
  const select = async name => {
    await page.getByRole('link',{name:new RegExp('^Edit placement '+name+':')}).click();
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
  const headingInCenter = async id => page.locator(id+' .panel-heading').evaluate(node=>{
    const bounds=node.getBoundingClientRect(), pane=document.querySelector('#studio-center .shell-scroll').getBoundingClientRect();
    return bounds.top>=pane.top-1 && bounds.bottom<=Math.min(pane.bottom,innerHeight)+1;
  });
  const paneIdentity = async () => page.evaluate(()=>({
    document:window.__arrangeNavigationDocument===document,
    shell:!!window.__arrangePaneRefs.shell && window.__arrangePaneRefs.shell===document.querySelector('#studio-shell'),
    live:!!window.__arrangePaneRefs.live && window.__arrangePaneRefs.live===document.querySelector('#cicada-live'),
  }));
  const recordingRevealed = async (entry='vocal navigation') => {
    await page.locator('#view-panel-arrange').waitFor({state:'visible'});
    await page.locator('#studio-center').waitFor({state:'visible'});
    try { await until(()=>headingInCenter('#vocals'),'Record did not scroll vocal controls into the visible center pane'); }
    catch (error) {
      const layout=await page.evaluate(()=>{
        const pane=document.querySelector('#studio-center .shell-scroll'), heading=document.querySelector('#vocals .panel-heading');
        return {pane:pane.getBoundingClientRect().toJSON(),heading:heading.getBoundingClientRect().toJSON(),scrollTop:pane.scrollTop,scrollHeight:pane.scrollHeight,view:document.querySelector('#studio-center').getAttribute('data-studio-region'),region:document.querySelector('#studio-shell').getAttribute('data-phone-region'),href:location.href};
      });
      assert.fail(entry+': '+error.message+' '+JSON.stringify(layout));
    }
    await page.waitForTimeout(100);
    assert.equal(await page.locator('#vocals [data-capture-control=record]').isDisabled(),true,'fixture without an audio track must keep Record take disabled');
    assert.equal(await block('one').getAttribute('data-placement-selected'),'true','revealing recording changed the selected placement');
  };
  const shellTab = async (group,name) => {
    await page.locator('#'+group+'-tab-'+name).click();
    await page.locator('#'+group+'-panel-'+name).waitFor({state:'visible'});
    await until(async()=>await page.locator('#'+group+'-tab-'+name).getAttribute('aria-selected')==='true','selected tab state did not follow '+name);
  };
  try {
    await visit('?placement=one');
    assert.equal((await state()).source,fixture,'runner refuses an unmarked or modified score');

    // A producer opens the pattern from its arrangement clip, without making
    // a score edit. Selection and its Clip editor follow the same click.
    const proof=process.env.CICADA_PLACEMENT_OPEN_PROOF_DIR;
    if(proof) fs.mkdirSync(proof,{recursive:true});
    await page.locator('#transport-form button[value=play]').click();
    await until(async()=>(await transport()).playing,'owner song playback did not start');
    const beforeOpen=await transport();
    const initialPosts=posts.length;
    assert.equal(await block('two').locator('.placement-select').getAttribute('aria-label'),'Edit placement two: pattern answer');
    await block('two').locator('.placement-select').click();
    await until(async()=>new URL(page.url()).searchParams.get('pattern')==='answer' && await page.locator('#detail-panel-clip .piano-roll[data-pattern-id=answer]').count()===1,'click did not open the second clip pattern');
    await ready();
    assert.equal(await page.locator('#detail-panel-clip').isVisible(),true);
    assert.equal(new URL(page.url()).searchParams.get('placement'),'two');
    assert.equal(new URL(page.url()).searchParams.get('track'),'lead');
    assert.equal(await page.getByLabel('Hear pattern with').inputValue(),'lead','pattern preview must use the placement track, not the first compatible track');
    for(const request of posts.slice(initialPosts)) {
      assert.equal(new URL(request.url()).pathname,'/__actions/live','opening a clip submitted a score action');
      assert.equal((await actionValues(request)).type,'release','opening a clip submitted a Live action other than releasing its old mount');
    }
    assert.equal((await state()).source,fixture);
    await until(async()=>{const now=await transport();return now.playing && (now.bar!==beforeOpen.bar || now.step!==beforeOpen.step)},'song transport did not continue advancing after clip opening');
    const cellVisible=()=>page.locator('#detail-panel-clip .piano-roll .roll-cell').first().evaluate(cell=>{
      const c=cell.getBoundingClientRect(), pane=document.querySelector('#studio-detail .shell-scroll').getBoundingClientRect();
      return c.right>pane.left && c.left<pane.right && c.bottom>pane.top && c.top<Math.min(pane.bottom,innerHeight);
    });
    const noteVisible=()=>page.locator('#pattern-shared-note').evaluate(note=>{
      const n=note.getBoundingClientRect(), pane=document.querySelector('#studio-detail .shell-scroll').getBoundingClientRect();
      return n.right>pane.left && n.left<pane.right && n.bottom>pane.top && n.top<Math.min(pane.bottom,innerHeight);
    });
    assert.equal(await cellVisible(),true,'a piano-roll cell must be visible in the Clip dock without scrolling');
    assert.equal(await page.getByText('Edits affect every use of this pattern.',{exact:true}).count(),1,'the editor must explain shared pattern edits');
    assert.equal(await noteVisible(),true,'the shared-pattern note must be visible alongside the grid');
    assert.equal(await page.locator('#detail-panel-clip .piano-roll .roll-cell').first().getAttribute('aria-describedby'),'pattern-shared-note');
    if(proof) await page.screenshot({path:path.join(proof,'opened-answer.png')});
    await page.setViewportSize({width:390,height:844});
    assert.equal(await cellVisible(),true,'a piano-roll cell must be visible in the phone Clip dock without scrolling');
    assert.equal(await noteVisible(),true,'the phone must show the shared-pattern note alongside the grid');
    if(proof) await page.screenshot({path:path.join(proof,'opened-answer-phone.png')});
    await page.setViewportSize({width:1600,height:1000});
    const skipGrid=page.getByRole('link',{name:'Skip grid to listening and quantize'});
    await skipGrid.focus();
    assert.equal(await skipGrid.evaluate(node=>node===document.activeElement),true,'the grid skip link must be keyboard focusable');
    assert.equal(new URL(await skipGrid.getAttribute('href'),base).hash,'#pattern-controls');
    await page.keyboard.press('Enter');
    assert.equal(await page.locator('#pattern-controls').evaluate(node=>node===document.activeElement),true,'skip link must focus the listening and quantize controls');
    await page.locator('#transport-form button[value=stop]').click();
    await until(async()=>!(await transport()).playing,'owner song playback did not stop');

    const sameClipContext=async label=>{
      const q=new URL(page.url()).searchParams;
      assert.equal(q.get('pattern'),'answer',label+' pattern');
      assert.equal(q.get('placement'),'two',label+' placement');
      assert.equal(q.get('track'),'lead',label+' track');
      assert.equal(await page.getByLabel('Hear pattern with').inputValue(),'lead',label+' audition track');
    };
    await page.getByLabel('Lowest octave').fill('3');
    await page.getByRole('button',{name:'View register'}).click();
    await until(()=>new URL(page.url()).searchParams.get('octave')==='3','register selection did not settle');await ready();
    await sameClipContext('register selection');
    await page.locator('#detail-panel-clip .step-number').nth(1).click();
    await until(()=>new URL(page.url()).searchParams.get('step')==='2','step selection did not settle');await ready();
    await sameClipContext('step selection');
    await page.locator('#detail-panel-clip .tie-step').nth(2).click();
    await until(()=>new URL(page.url()).searchParams.get('step')==='3','tie selection did not settle');await ready();
    await sameClipContext('tie selection');

    // The opened grid remains an editor, and its change is one Undo step.
    await page.locator('#studio-shell[data-shell-edits=ready]').waitFor();
    const empty=page.locator('#detail-panel-clip .piano-roll .roll-cell[data-active=false]').first();
    await empty.click();
    try { await until(async()=>(await state()).source!==fixture,'drawing into opened pattern did not save'); }
    catch (error) {
      const sent=[];
      for (const request of posts.slice(initialPosts)) {
        const values=await actionValues(request).catch(()=>({}));
        sent.push({path:new URL(request.url()).pathname,action:values.action,type:values.type});
      }
      assert.fail(error.message+' '+JSON.stringify({sent,status:await page.locator('[data-workspace-status]').allTextContents(),errors}));
    }
    await sameClipContext('after note edit');
    await page.getByRole('button',{name:'Undo',exact:true}).click();
    await sourceIs(fixture); await ready();
    await sameClipContext('after Undo');
    if(proof) await page.screenshot({path:path.join(proof,'draw-undone.png')});

    await page.locator('#detail-panel-clip .pattern-choice').filter({hasText:'pulse'}).click();
    await until(()=>new URL(page.url()).searchParams.get('pattern')==='pulse','different pattern did not open');await ready();
    assert.equal(new URL(page.url()).searchParams.has('placement'),false,'a different pattern inherited the old placement');
    assert.notEqual(new URL(page.url()).searchParams.get('track'),'lead','a different pattern inherited the old audition track: '+page.url());

    await block('one').locator('.placement-select').focus();
    await page.keyboard.press('Enter');
    await until(async()=>new URL(page.url()).searchParams.get('pattern')==='pulse' && await page.locator('#detail-panel-clip .piano-roll[data-pattern-id=pulse]').count()===1,'Enter did not open the focused pattern');
    await ready();
    assert.equal(await page.evaluate(()=>document.activeElement?.id),'patterns','keyboard opening must focus the Clip editor');
    await page.setViewportSize({width:390,height:844});
    await page.getByRole('link',{name:'View',exact:true}).click();
    await block('two').locator('.placement-select').focus();
    await page.keyboard.press('Enter');
    await until(async()=>new URL(page.url()).searchParams.get('pattern')==='answer' && await page.evaluate(()=>document.activeElement?.id)==='patterns','phone keyboard opening did not focus its Clip editor');
    assert.equal(await page.locator('#studio-shell').getAttribute('data-phone-region'),'detail');
    await page.setViewportSize({width:1600,height:1000});
    await block('one').locator('.placement-select').focus();
    await page.keyboard.press('Enter');
    await until(async()=>new URL(page.url()).searchParams.get('pattern')==='pulse' && await page.evaluate(()=>document.activeElement?.id)==='patterns','return to the first pattern did not settle');
    await ready();
    const draft=page.locator('#score textarea').first();
    if(!(await page.locator('#studio-code > details').evaluate(node=>node.open))) await page.locator('.shell-pane-toggle[data-pane=code][data-pane-action=show]').click();
    await draft.fill(fixture+'// unsaved clip idea\n');
    const guardedPosts=posts.length;
    await block('two').locator('.placement-select').focus();
    await page.keyboard.press('Enter');
    await block('two').locator('.placement-select').click();
    await page.waitForTimeout(150);
    assert.equal(new URL(page.url()).searchParams.get('pattern'),'pulse','opening with an unsaved draft changed the editor');
    assert.equal(await draft.inputValue(),fixture+'// unsaved clip idea\n');
    assert.equal(posts.length,guardedPosts,'guarded clip opening wrote the score');
    assert.equal((await state()).source,fixture);
    await draft.fill(fixture);
    await until(async()=>/No unsaved Code edits/.test(await page.locator('[data-source-recovery-status]').textContent()),'restored Code draft was not cleared before continuing');
    t.diagnostic('clip-open: click and Enter open their patterns; opening writes nothing; drawing and one Undo restore the score; unsaved Code blocks opening');
    if(proof) fs.writeFileSync(path.join(proof,'clip-open.json'),JSON.stringify({fixture:'placement-commands',click:'answer',enter:'pulse',scoreWrites:0,liveRelease:'observed on navigation',songTransportContinued:true,desktopAndPhoneGridVisible:true,desktopAndPhoneKeyboardFocus:'patterns',skipGridKeyboardFocus:'pattern-controls',drawUndoRestored:true,unsavedDraftPreserved:true},null,2)+'\n');

    // Arrange opens on the song rather than placing its timeline below the
    // entire vocal workflow. The sticky links move within the mounted pane.
    assert.equal(await page.locator('#arrangement').evaluate(node=>Boolean(node.compareDocumentPosition(document.querySelector('#vocals')) & Node.DOCUMENT_POSITION_FOLLOWING)),true,'song timeline must precede vocal recording');
    assert.equal(await headingInCenter('#arrangement'),true,'song timeline must be visible when Arrange first opens');
    assert.equal(await page.locator('#arrangement .placement-scroll').evaluate(node=>{
      const bounds=node.getBoundingClientRect(), pane=document.querySelector('#studio-center .shell-scroll').getBoundingClientRect();
      return bounds.top>=pane.top && bounds.top<Math.min(pane.bottom,innerHeight) && bounds.bottom>pane.top;
    }),true,'placement timeline must appear in the initial Arrange viewport');
    const vocalLink=page.locator('#view-panel-arrange').getByRole('link',{name:'Record & comp vocals',exact:true});
    const timelineLink=page.locator('#view-panel-arrange').getByRole('link',{name:'Song timeline',exact:true});
    assert.equal(await vocalLink.getAttribute('href'),'#vocals');
    assert.equal(await timelineLink.getAttribute('href'),'#arrangement');
    const beforeLinks={posts:posts.length,navigations:navigations.length};
    await page.evaluate(()=>{window.__arrangeNavigationDocument=document;window.__arrangePaneRefs={shell:document.querySelector('#studio-shell'),live:document.querySelector('#cicada-live')};});
    await vocalLink.click(); await recordingRevealed();
    await timelineLink.click();
    await until(()=>headingInCenter('#arrangement'),'Song timeline link did not return to the timeline');
    assert.equal(posts.length,beforeLinks.posts,'in-pane navigation must not submit score actions: '+posts.slice(beforeLinks.posts).map(request=>new URL(request.url()).pathname).join(', ')+'; page requests: '+navigations.slice(beforeLinks.navigations).map(request=>new URL(request.url()).search).join(', '));
    assert.equal(navigations.length,beforeLinks.navigations,'in-pane navigation must not request another page: '+JSON.stringify(navigations.slice(beforeLinks.navigations).map(request=>({path:new URL(request.url()).pathname,search:new URL(request.url()).search,type:request.resourceType(),document:request.isNavigationRequest()}))));
    const initialPaneIdentity=await paneIdentity();
    assert.deepEqual(initialPaneIdentity,{document:true,shell:true,live:true},'in-pane links must retain the existing document, shell and Live mounts');
    assert.equal((await state()).source,fixture);
    t.diagnostic(JSON.stringify({scenario:'initial Arrange section jumps',expected:{posts:0,navigations:0,document:true,shell:true,live:true,sourceUnchanged:true},actual:{posts:posts.length-beforeLinks.posts,navigations:navigations.length-beforeLinks.navigations,...initialPaneIdentity,sourceUnchanged:(await state()).source===fixture}}));

    const scoreDraft=fixture+'// keep this unsaved vocal idea\n';
    // Code starts closed; open it from the top bar to keep a draft there.
    if(!(await page.locator('#studio-code > details').evaluate(node=>node.open)))await page.locator('.shell-pane-toggle[data-pane=code][data-pane-action=show]').click();
    const draftEditor=page.locator('#score textarea').first();
    await draftEditor.fill(scoreDraft);
    const beforeTabs={posts:posts.length,navigations:navigations.length};
    for (const view of ['session','mixer','arrange']) {
      await shellTab('view',view);
      assert.equal(new URL(page.url()).searchParams.get('tab')||'arrange',view,'view selection must agree with its URL');
      assert.equal(await draftEditor.inputValue(),scoreDraft,'view navigation discarded a Code draft');
    }
    for (const dock of ['device','automation','clip']) {
      await shellTab('detail',dock);
      assert.equal(new URL(page.url()).searchParams.get('dock')||'clip',dock,'dock selection must agree with its URL');
      assert.equal(await draftEditor.inputValue(),scoreDraft,'dock navigation discarded a Code draft');
    }
    for (const [key,view] of [['Digit2','session'],['Digit3','mixer'],['Digit1','arrange']]) {
      await page.locator('#view-tab-arrange').focus();
      await page.keyboard.press('Alt+Shift+'+key);
      await page.locator('#view-panel-'+view).waitFor({state:'visible'});
      assert.equal(await page.locator('#view-tab-'+view).getAttribute('aria-selected'),'true','view shortcut must update accessible selection');
      assert.equal(new URL(page.url()).searchParams.get('tab')||'arrange',view);
      assert.equal(await draftEditor.inputValue(),scoreDraft,'view shortcut discarded a Code draft');
    }
    assert.equal(await page.evaluate(()=>window.__arrangeNavigationDocument===document),true);
    assert.equal(navigations.length,beforeTabs.navigations,'view and dock switching must not request another page: '+navigations.slice(beforeTabs.navigations).map(request=>new URL(request.url()).search).join(', '));
    assert.equal(posts.length,beforeTabs.posts,'view and dock switching must not submit an edit');
    assert.equal(new URL(page.url()).searchParams.get('placement'),'one');
    assert.equal((await state()).source,fixture);

    // Local view/dock changes update the URL without replacing the workbench.
    // Native fragment popstate must not turn that updated query into a GoSX
    // page fetch, dispose Live, or replace an unsaved score draft.
    const beforeUpdatedLinks={posts:posts.length,navigations:navigations.length,state:await state()};
    await vocalLink.click(); await recordingRevealed();
    await timelineLink.click();
    await until(()=>headingInCenter('#arrangement'),'Song timeline link after view changes did not return to the timeline');
    assert.equal(posts.length,beforeUpdatedLinks.posts,'section jumps after view/dock changes must not submit any score action');
    assert.equal(navigations.length,beforeUpdatedLinks.navigations,'section jumps after view/dock changes must not request another page');
    const updatedPaneIdentity=await paneIdentity();
    assert.deepEqual(updatedPaneIdentity,{document:true,shell:true,live:true},'section jumps after view/dock changes must retain existing document, shell and Live mounts');
    assert.equal(await draftEditor.inputValue(),scoreDraft,'section jumps after view/dock changes must retain the exact unsaved draft');
    assert.deepEqual(await state(),beforeUpdatedLinks.state,'section jumps after view/dock changes must not mutate the song');
    t.diagnostic(JSON.stringify({scenario:'Arrange section jumps after local view and dock changes',expected:{posts:0,navigations:0,document:true,shell:true,live:true,draftPreserved:true,songUnchanged:true},actual:{posts:posts.length-beforeUpdatedLinks.posts,navigations:navigations.length-beforeUpdatedLinks.navigations,...updatedPaneIdentity,draftPreserved:(await draftEditor.inputValue())===scoreDraft,songUnchanged:JSON.stringify(await state())===JSON.stringify(beforeUpdatedLinks.state)}}));

    // Both Record entry points reveal the actual controls even before a vocal
    // track exists. The recording view must retain unsaved lyrics or score ideas.
    for (const record of ['button','shortcut']) {
      await shellTab('view','session');
      await ready();
      await draftEditor.fill(scoreDraft);
      const beforeRecord={posts:posts.length,navigations:navigations.length};
      if (record==='button') await page.locator('[data-gosx-command="transport.record"]').click();
      else {
        await page.locator('#view-tab-session').focus();
        await page.keyboard.press('Meta+Shift+r');
      }
      await recordingRevealed(record);
      assert.equal(await draftEditor.inputValue(),scoreDraft,'Record discarded an unsaved Code draft');
      assert.equal(posts.length,beforeRecord.posts,'Record must not submit a disabled take form');
      assert.equal(navigations.length,beforeRecord.navigations,'Record must reveal the mounted controls without navigation');
      assert.equal(await page.evaluate(()=>window.__arrangeNavigationDocument===document),true);
    }
    assert.equal((await state()).source,fixture);

    // On a narrow window, Record also returns from Detail to the center region
    // so the revealed Arrange controls are actually on screen.
    await shellTab('view','session');
    await page.setViewportSize({width:390,height:844});
    const beforeRegions={posts:posts.length,navigations:navigations.length};
    for (const [label,region,panel] of [['Edit','detail','#studio-detail'],['Code','code','#studio-code'],['View','center','#studio-center'],['Edit','detail','#studio-detail']]) {
      await page.locator('.shell-phone-nav').getByRole('link',{name:label,exact:true}).click();
      await page.locator(panel).waitFor({state:'visible'});
      assert.equal(await page.locator('#studio-shell').getAttribute('data-phone-region'),region);
      assert.equal(new URL(page.url()).searchParams.get('region')||'center',region);
      assert.equal(await draftEditor.inputValue(),scoreDraft,'mobile region navigation discarded a Code draft');
    }
    assert.equal(posts.length,beforeRegions.posts);
    assert.equal(navigations.length,beforeRegions.navigations,'mobile region switching must not request another page');
    assert.equal(await page.evaluate(()=>window.__arrangeNavigationDocument===document),true);
    assert.equal(await page.locator('#studio-center').isVisible(),false);
    const beforeMobileRecord={posts:posts.length,navigations:navigations.length};
    await page.locator('[data-gosx-command="transport.record"]').click();
    await recordingRevealed();
    assert.equal(await page.locator('#studio-shell').getAttribute('data-phone-region'),'center');
    assert.equal(posts.length,beforeMobileRecord.posts);
    assert.equal(navigations.length,beforeMobileRecord.navigations);
    assert.equal(await draftEditor.inputValue(),scoreDraft,'mobile Record discarded a Code draft');
    assert.equal((await state()).source,fixture);
    await page.setViewportSize({width:1600,height:1000});
    await draftEditor.fill(fixture);
    await page.getByText('No unsaved Code edits.',{exact:true}).waitFor();
    await visit('?placement=one');

    // A server-rendered placement link and form must inherit the current local
    // view/dock choices, rather than reviving the previous page's selections.
    await shellTab('detail','device');
    await shellTab('view','session');
    await page.locator('[data-gosx-command="transport.record"]').click();
    await recordingRevealed('context round-trip');
    // Hold the real placement GET so the local choice is newer than the
    // server-rendered dock defaults. Do not mock its response or add a delay.
    let releaseSelection, selectionRequested;
    const selectionGate=new Promise(resolve=>releaseSelection=resolve);
    const selectionSeen=new Promise(resolve=>selectionRequested=resolve);
    const selectionURL=url=>url.pathname==='/studio'&&url.searchParams.get('placement')==='two';
    await page.route(selectionURL,async route=>{selectionRequested();await selectionGate;await route.continue()});
    await page.getByRole('link',{name:/^Edit placement two:/}).click();
    await selectionSeen;
    await shellTab('detail','automation');
    await shellTab('view','mixer');
    releaseSelection();
    await until(async()=>await page.evaluate(()=>document.documentElement.getAttribute('data-gosx-navigation-state')==='idle'),'placement navigation did not complete');
    await page.unroute(selectionURL);await ready();
    assert.equal(await page.locator('#view-panel-mixer').isVisible(),true,'in-flight placement selection replaced the newer Mixer view');
    assert.equal(await page.locator('#detail-panel-automation').isVisible(),true,'in-flight placement selection replaced the newer Automation dock');
    assert.equal(new URL(page.url()).searchParams.get('tab'),'mixer');
    assert.equal(new URL(page.url()).searchParams.get('dock'),'automation');
    assert.equal((await state()).source,fixture,'local choices during selection changed the score');
    await shellTab('view','arrange');
    await key('Meta+d'); await block('two-copy').waitFor(); await ready();
    assert.equal(await page.locator('#view-panel-arrange').isVisible(),true,'placement mutation changed the current view');
    assert.equal(await page.locator('#detail-panel-automation').isVisible(),true,'placement mutation revived a stale dock');
    assert.equal(new URL(page.url()).searchParams.get('dock'),'automation');
    await undo(fixture);
    assert.equal(await page.locator('#detail-panel-automation').isVisible(),true,'Undo lost the chosen dock');
    t.diagnostic('Held real placement GET: newer Mixer and Automation choices survive completion; duplicate and exact Undo retain Automation.');
    await visit('?placement=one');

    const before = (await state()).revision;
    const beforeDuplicatePosts=posts.length;
    await key('Meta+d'); await block('one-copy').waitFor(); await ready();
    assert.equal(await block('one-copy').getAttribute('data-placement-at'),'15360','duplicate appends after the last placement on its track');
    const duplicateSource=(await state()).source;
    assert.notEqual((await state()).revision,before);
    const duplicate=posts.slice(beforeDuplicatePosts).find(request=>new URL(request.url()).pathname==='/__actions/arrange');
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
    await visit('?placement=one');
    await page.locator('#transport-form button[value=play]').click();
    await until(async()=>(await transport()).playing,'owner song playback did not start for held MIDI');
    // A held Web MIDI note survives the local Session -> Arrange view switch.
    // Opening a clip would remount Live, so it must refuse until note-off.
    await shellTab('view','session');
    await page.locator('[data-live-control=midi]').click();
    await until(async()=>/MIDI enabled/.test(await page.locator('[data-live-status]').textContent()),'test MIDI input did not enable');
    await shellTab('view','arrange');
    await page.waitForFunction(()=>!document.querySelector('[data-live-note="60"]').disabled);
    const liveNoteResponse=page.waitForResponse(async response=>{
      if(new URL(response.url()).pathname!=='/__actions/live')return false;
      const fields=await actionValues(response.request()).catch(()=>({}));
      if(fields.type!=='input-batch')return false;
      return JSON.parse(fields.messages).some(message=>message.type==='note'&&message.note==='60'&&message.on==='true');
    });
    await page.evaluate(()=>window.__clipTestMIDI(0x90,60,100));
    assert.equal((await liveNoteResponse).status(),200,'held note-on was not accepted by the real Live service');
    await until(async()=>await page.locator('[data-live-note="60"]').getAttribute('aria-pressed')==='true','MIDI note was not held');
    await page.evaluate(()=>{window.__heldClipNavigationCount=0;document.addEventListener('gosx:navigate',()=>window.__heldClipNavigationCount++);});
    const heldURL=page.url(), heldReleaseCount=liveReleases.length, heldSource=(await state()).source, heldPosts=posts.length;
    await block('two').locator('.placement-select').click();
    await block('two').locator('.placement-select').focus();
    await page.keyboard.press('Enter');
    await page.waitForFunction(()=>document.documentElement.getAttribute('data-gosx-navigation-state')==='idle');
    await page.waitForTimeout(150);
    assert.match(await page.locator('[data-placement-edit-status]').textContent(),/Save or discard unsaved edits/);
    assert.equal(await page.evaluate(()=>window.__heldClipNavigationCount),0,'clip activation navigated while a MIDI note was held');
    assert.equal(page.url(),heldURL,'clip activation changed the location while a MIDI note was held');
    assert.equal(await page.locator('[data-live-note="60"]').getAttribute('aria-pressed'),'true','clip activation released the held MIDI note');
    assert.equal(liveReleases.length,heldReleaseCount,'clip activation released the Live lease');
    for(const request of posts.slice(heldPosts)) {
      assert.equal(new URL(request.url()).pathname,'/__actions/live','clip activation submitted a score action');
      assert.equal((await actionValues(request)).type,'heartbeat','clip activation sent a note-off or lease release');
    }
    assert.equal((await state()).source,heldSource);
    assert.equal((await transport()).playing,true,'clip activation stopped song playback');
    // The player releases its own key normally; no forced lease release.
    const liveNoteOffResponse=page.waitForResponse(async response=>{
      if(new URL(response.url()).pathname!=='/__actions/live')return false;
      const fields=await actionValues(response.request()).catch(()=>({}));
      if(fields.type!=='input-batch')return false;
      return JSON.parse(fields.messages).some(message=>message.type==='note'&&message.note==='60'&&message.on==='false');
    });
    await page.evaluate(()=>window.__clipTestMIDI(0x80,60,0));
    assert.equal((await liveNoteOffResponse).status(),200,'matching MIDI note-off was not accepted');
    await until(async()=>await page.locator('[data-live-note="60"]').getAttribute('aria-pressed')==='false','MIDI note-off did not release the note');
    await block('two').locator('.placement-select').click();
    await until(async()=>new URL(page.url()).searchParams.get('pattern')==='answer' && await page.locator('#detail-panel-clip .piano-roll[data-pattern-id=answer]').count()===1,'clip did not open after the matching MIDI note-off');
    await ready();
    assert.equal(new URL(page.url()).searchParams.get('placement'),'two');
    assert.equal(new URL(page.url()).searchParams.get('track'),'lead');
    assert.equal((await state()).source,heldSource,'opening after note-off changed the score');
    assert.equal((await transport()).playing,true,'opening after note-off stopped song playback');
    t.diagnostic('held MIDI note: pointer and Enter refuse clip opening without navigation, score write, note-off, lease release or transport stop; opening succeeds after matching note-off');
    await page.locator('#transport-form button[value=stop]').click();
    await until(async()=>!(await transport()).playing,'owner song playback did not stop after MIDI note-off');
    // Unload delivery is browser best effort. Exercise the actual pagehide
    // listener while this fixture document lives, then prove the same mount
    // releases its known lease with an advancing sequence on the next event.
    const beforeProducer={releases:liveReleases.length,state:await state()};
    await page.evaluate(()=>{window.__releaseProducerDocument=document;});
    const produceRelease=async()=>{
      const emitted=await page.evaluate(async()=>{
        const original=navigator.sendBeacon,captured=[];
        navigator.sendBeacon=function(url,data){
          const sent=original.apply(this,arguments);
          if(new URL(url,location.href).pathname==='/__actions/live' && data instanceof Blob){
            captured.push(data.text().then(text=>{const fields=new URLSearchParams(text);return {lease:fields.get('lease'),sequence:Number(fields.get('sequence')),sent};}));
          }
          return sent;
        };
        try { window.dispatchEvent(new PageTransitionEvent('pagehide',{persisted:false})); }
        finally { navigator.sendBeacon=original; }
        return Promise.all(captured);
      });
      assert.equal(emitted.length,1,'the current Live pagehide producer must emit one release');
      assert.equal(emitted[0].sent,true,'the native Beacon transport must accept the fixture release');
      return emitted[0];
    };
    const firstEmission=await produceRelease();
    await until(()=>liveReleases.slice(beforeProducer.releases).some(release=>release.lease===firstEmission.lease && release.sequence===firstEmission.sequence),'Live pagehide producer did not deliver its exact fixture release');
    const beforeRepeat=liveReleases.length,repeatedEmission=await produceRelease();
    assert.ok(repeatedEmission.lease===firstEmission.lease,'the repeated producer must retain its known mount lease');
    assert.ok(repeatedEmission.sequence>firstEmission.sequence,'the repeated producer must advance its known lease sequence');
    await until(()=>liveReleases.slice(beforeRepeat).some(release=>release.lease===repeatedEmission.lease && release.sequence===repeatedEmission.sequence),'Live pagehide producer did not deliver its exact repeated release');
    assert.equal(await page.evaluate(()=>window.__releaseProducerDocument===document),true,'release producer proof must keep the fixture document alive');
    assert.deepEqual(await state(),beforeProducer.state,'release producer must not mutate the song');
    assert.deepEqual(errors,[]);
    t.diagnostic(JSON.stringify({scenario:'deterministic Live pagehide producer',expected:{sameLease:true,sequenceAdvanced:true,documentSame:true,songUnchanged:true},actual:{sameLease:repeatedEmission.lease===firstEmission.lease,sequenceAdvanced:repeatedEmission.sequence>firstEmission.sequence,firstSequence:firstEmission.sequence,repeatedSequence:repeatedEmission.sequence,documentSame:await page.evaluate(()=>window.__releaseProducerDocument===document),songUnchanged:JSON.stringify(await state())===JSON.stringify(beforeProducer.state)}}));
  } finally {
    await page.unroute('**/__actions/live');
    await context.close(); await browser.close();
  }
});
