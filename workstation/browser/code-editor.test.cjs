/* Real Studio code editing on Windows/Linux/Mac browser profiles and a narrow
 * viewport. The supplied Studio must own only this disposable fixture.
 * node workstation/browser/code-editor.test.cjs --write-fixture /tmp/editor.cicada
 * CICADA_STUDIO_URL=... node --test workstation/browser/code-editor.test.cjs
 * CICADA_PLAYWRIGHT_MODULE and CICADA_CHROME_PATH select browser tooling.
 * --write-project /tmp/new-project creates a separate two-file fixture. Run it
 * with CICADA_EDITOR_PROJECT=1 and CICADA_EDITOR_PROJECT_ROOT=/tmp/new-project.
 */
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {createRequire} = require('node:module');
const test = require('node:test');

const fixture = `// cicada-code-editor-test: disposable fixture
// café 🎵 日本語
cicada 2
title "Code editor platform checks"
track bass acid { cutoff=700Hz }
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
const projectEntry = `// cicada-code-editor-project-test: disposable fixture
cicada 2
title "Code editor project checks"
track bass acid { cutoff=700Hz }
scene main { bass=pulse }
song { main*2 }
`;
const projectPart = 'pattern pulse { 1 . 5 . }\n';
if (process.argv[2] === '--write-project') {
  assert.ok(process.argv[3], 'supply a new disposable project directory');
  const root=path.resolve(process.argv[3]);
  fs.mkdirSync(root); fs.mkdirSync(path.join(root,'parts'));
  fs.writeFileSync(path.join(root,'cicada.mod'),'project code_editor_regression\ncicada 2\nentry "main.cicada"\nsource "parts/patterns.cicada"\n',{flag:'wx'});
  fs.writeFileSync(path.join(root,'main.cicada'),projectEntry,{flag:'wx'});
  fs.writeFileSync(path.join(root,'parts/patterns.cicada'),projectPart,{flag:'wx'});
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
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  assert.fail(message);
}
const lf = value => value.replace(/\r\n/g, '\n');
const profiles = [
  {name:'Windows', platform:'Win32', os:'Windows', mod:'Control'},
  {name:'Linux', platform:'Linux x86_64', os:'Linux', mod:'Control'},
  {name:'Mac', platform:'MacIntel', os:'macOS', mod:'Meta'},
];

test('Studio Code typing, commands, drafts and guarded saves work across platform profiles', {timeout:180000,skip:process.env.CICADA_EDITOR_PROJECT==='1'}, async t => {
  const base = process.env.CICADA_STUDIO_URL;
  assert.ok(base, 'set CICADA_STUDIO_URL to a fresh disposable Studio');
  assert.ok(['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
  const browser = await playwright().chromium.launch({headless:true, executablePath:process.env.CICADA_CHROME_PATH, args:['--no-sandbox']});
  // Studio's first authenticated session owns the score. Profiles reuse that
  // owner's cookies while each page initializes its own platform-specific keys.
  const context = await browser.newContext({viewport:{width:1440,height:960}});
  const errors = [], posts = [];
  const state = async page => (await page.request.get(new URL('/api/state',base).href)).json();
  const ready = async page => {
    await page.waitForFunction(()=>window.__gosx?.ready===true);
    await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
    await page.locator('.editor-page.editor-highlight-ready').waitFor();
    await page.waitForFunction(()=>!document.querySelector('form[data-gosx-form-state=pending]'));
    await until(async()=>await page.locator('#undo-form input[name=revision]').inputValue()===(await state(page)).revision,'Code navigation did not refresh song revisions');
  };
  const watch = page => {
    page.setDefaultTimeout(15000);
    page.on('pageerror',error=>errors.push(error.message));
    page.on('request',request=>{if(request.method()==='POST' && new URL(request.url()).pathname.startsWith('/__actions/')) posts.push(request);});
  };
  const select = async (editor,start,end=start,direction='forward') => editor.evaluate((node,{start,end,direction})=>{
    node.focus({preventScroll:true}); node.setSelectionRange(start,end,direction);
  },{start,end,direction});
  const visibleText = async page => {
    const value = await page.locator('#editor-content').inputValue();
    await until(async()=>{
      const rendered=await page.locator('#editor-highlight-content').textContent();
      // The client adds a final display row; the server's initial markup does not.
      return rendered===value || rendered===value+'\n';
    },'visible code did not follow the current editor buffer');
  };
  const semanticText = async page => {
    await until(async()=>await page.locator('#editor-native-form').getAttribute('data-code-intelligence-lane')==='server' && await page.locator('#editor-highlight-content .syntax-keyword-directive').allTextContents().then(values=>values.includes('cicada')),'live semantic highlighting did not follow the current Code draft');
  };
  const fit = async page => {
    for (const label of ['Save file','Check file']) {
      const button = page.getByRole('button',{name:label,exact:true});
      assert.equal(await button.evaluate(node=>{
        const rect=node.getBoundingClientRect(), pane=document.querySelector('#studio-code .shell-scroll').getBoundingClientRect();
        return rect.width>0 && rect.left>=pane.left-1 && rect.right<=Math.min(pane.right,innerWidth)+1 && rect.top>=pane.top-1 && rect.bottom<=Math.min(pane.bottom,innerHeight)+1;
      }),true,label+' must fit inside the visible Code dock');
    }
  };
  let page;
  try {
    for (const profile of profiles) {
      page = await context.newPage(); watch(page);
      await page.addInitScript(({platform,os})=>{
        Object.defineProperty(navigator,'platform',{get:()=>platform});
        Object.defineProperty(navigator,'userAgentData',{get:()=>({platform:os})});
      },profile);
      await page.goto(new URL('/studio?placement=one&region=code&dock=device',base).href);
      await ready(page);
      assert.equal(lf((await state(page)).source),fixture,'runner refuses an unmarked or modified score');
      await fit(page); await visibleText(page);
      const editor=page.locator('#editor-content');
      const beforePosts=posts.length;
      await editor.fill('');
      await editor.pressSequentially('pattern pulse {');
      await editor.press('Enter');
      assert.equal(await editor.inputValue(),'pattern pulse {\n  ',profile.name+' Enter must indent a new block');
      await editor.pressSequentially('1 . 5 .');
      await visibleText(page);
      await editor.press('Enter');
      await editor.press('Shift+Tab');
      await editor.pressSequentially('}');
      assert.equal(await editor.inputValue(),'pattern pulse {\n  1 . 5 .\n}',profile.name+' Enter and dedent must follow the configured two spaces');
      await editor.fill('{}');
      await select(editor,1); await editor.press('Enter');
      assert.equal(await editor.inputValue(),'{\n  \n}',profile.name+' paired braces must open an indented line');
      assert.deepEqual(await editor.evaluate(node=>[node.selectionStart,node.selectionEnd]),[4,4]);
      await editor.press(profile.mod+'+z');
      assert.equal(await editor.inputValue(),'{}',profile.name+' smart Enter must undo as one edit');
      await editor.fill('alpha\nbeta');
      await select(editor,0,10,'backward'); await editor.press('Tab');
      assert.equal(await editor.inputValue(),'  alpha\n  beta',profile.name+' block Tab must use spaces');
      assert.equal(await editor.evaluate(node=>node.selectionDirection),'backward','indent must retain a backward block selection');
      await editor.press('Shift+Tab');
      assert.equal(await editor.inputValue(),'alpha\nbeta',profile.name+' block dedent must reverse indentation');
      await select(editor,5); await editor.press('Tab');
      assert.equal(await editor.inputValue(),'alpha  \nbeta',profile.name+' Tab must use two spaces');
      await editor.press(profile.mod+'+z');
      assert.equal(await editor.inputValue(),'alpha\nbeta',profile.name+' undo must stay local');
      await editor.press(profile.mod+'+Shift+z');
      assert.equal(await editor.inputValue(),'alpha  \nbeta',profile.name+' redo must restore the local edit');
      await visibleText(page);

      // Text input includes non-ASCII and supplementary Unicode. The visible
      // highlight and the real textarea must agree at UTF-16 selections too.
      await editor.fill('alpha beta alpha\n');
      await select(editor,(await editor.inputValue()).length);
      await page.keyboard.insertText('// café 🎵 日本語');
      await visibleText(page);
      assert.equal(await editor.inputValue(),'alpha beta alpha\n// café 🎵 日本語');
      await editor.press(profile.mod+'+f');
      const query=page.locator('[data-code-find=query]');
      await query.fill('alpha');
      assert.equal(await page.locator('.editor-code-find-status').textContent(),'1 / 2');
      await page.getByRole('button',{name:'Next match',exact:true}).click();
      assert.deepEqual(await editor.evaluate(node=>[node.selectionStart,node.selectionEnd]),[11,16]);
      await editor.focus();
      await page.keyboard.press(profile.mod+(profile.name==='Mac'?'+Alt+f':'+h'));
      assert.equal(await page.locator('[data-code-find=replacement]').evaluate(node=>document.activeElement===node),true,profile.name+' Replace shortcut must focus replacement');
      await page.locator('[data-code-find=replacement]').fill('omega');
      await page.locator('[data-code-find-action=replace-all]').click();
      assert.equal(await editor.inputValue(),'omega beta omega\n// café 🎵 日本語');
      await page.getByRole('button',{name:'Close find and replace',exact:true}).click();
      await select(editor,0,16);
      await editor.press(profile.mod+'+/');
      assert.equal((await editor.inputValue()).split('\n')[0],'// omega beta omega');
      await page.getByRole('button',{name:'Toggle line comment',exact:true}).click();
      assert.equal((await editor.inputValue()).split('\n')[0],'omega beta omega');
      await editor.fill('track bass acid {}');
      await select(editor,16); // Opening brace.
      await page.getByRole('button',{name:'Go to matching bracket',exact:true}).click();
      assert.deepEqual(await editor.evaluate(node=>[node.selectionStart,node.selectionEnd]),[17,18]);
      await visibleText(page);
      assert.equal(posts.length,beforePosts,'local '+profile.name+' text commands must not save or trigger song Undo');

      const draft=fixture+'// unsaved '+profile.name+' vocal idea 🎵\n';
      await editor.fill(draft); await visibleText(page);
      // Typing clears stale server spans immediately; the analysis provider
      // must then recolor the current Unicode buffer at its UTF-16 offsets.
      await semanticText(page);
      assert.equal(await page.locator('#editor-native-form').getAttribute('data-code-intelligence-server'),'/__code/analysis','Code must use the live analysis provider');
      await page.evaluate(()=>{window.__codeEditorRefs={document,editor:document.querySelector('#editor-content')};});
      for (const view of ['session','mixer','arrange']) {
        await page.locator('#view-tab-'+view).click();
        await page.locator('#view-panel-'+view).waitFor({state:'visible'});
        assert.equal(await editor.inputValue(),draft,'view switch discarded '+profile.name+' draft');
      }
      assert.equal(await page.evaluate(()=>window.__codeEditorRefs.document===document && window.__codeEditorRefs.editor===document.querySelector('#editor-content')),true,'view changes replaced the Code editor');
      assert.equal(lf((await state(page)).source),fixture);
      await page.setViewportSize({width:390,height:844});
      for (const region of ['Center','Detail','Code']) await page.locator('.shell-phone-nav').getByRole('link',{name:region,exact:true}).click();
      await fit(page); await visibleText(page);
      assert.equal(await editor.inputValue(),draft,'narrow region switch discarded '+profile.name+' draft');
      t.diagnostic(profile.name+': typing, indentation, local history, Unicode, search/replace, comments, brackets and draft-preserving view/narrow navigation passed');
      const sourcePosts=()=>posts.filter(request=>new URL(request.url()).pathname==='/__actions/source').length;
      const beforeSavePosts=sourcePosts();
      const saveResponse=page.waitForResponse(response=>new URL(response.url()).pathname==='/__actions/source' && response.request().method()==='POST');
      await editor.focus(); await page.keyboard.press(profile.mod+'+s');
      assert.ok([200,303].includes((await saveResponse).status()),profile.name+' keyboard Save failed');
      await until(async()=>lf((await state(page)).source)===draft,profile.name+' keyboard Save did not use the current draft');
      await ready(page); await visibleText(page);
      assert.equal(await editor.inputValue(),draft);
      assert.equal(sourcePosts(),beforeSavePosts+1,'keyboard Save must submit once');
      await page.getByRole('button',{name:'Undo',exact:true}).click();
      await until(async()=>lf((await state(page)).source)===fixture,'song Undo did not restore the fixture after '+profile.name+' Save');
      await ready(page); await visibleText(page);
      assert.equal(await editor.inputValue(),fixture,'Undo did not refresh visible Code');
      const secondDraft=fixture+'// second '+profile.name+' keyboard Save after managed navigation\n';
      await editor.fill(secondDraft); await visibleText(page); await semanticText(page);
      const beforeSecondSavePosts=sourcePosts();
      const secondSaveResponse=page.waitForResponse(response=>new URL(response.url()).pathname==='/__actions/source' && response.request().method()==='POST');
      await editor.focus(); await page.keyboard.press(profile.mod+'+s');
      assert.ok([200,303].includes((await secondSaveResponse).status()),'second keyboard Save failed after managed navigation');
      await until(async()=>lf((await state(page)).source)===secondDraft,'second keyboard Save did not reach the current form');
      await ready(page); await visibleText(page);
      assert.equal(sourcePosts(),beforeSecondSavePosts+1,'second keyboard Save must submit once');
      await page.getByRole('button',{name:'Undo',exact:true}).click();
      await until(async()=>lf((await state(page)).source)===fixture,'second Save Undo did not restore the fixture');
      await ready(page); await visibleText(page);
      assert.equal(await editor.inputValue(),fixture);
      await page.close(); page=undefined;
    }

    // The actual server validates source writes. Check never writes; 422 and
    // a genuinely stale revision must retain the draft and allow recovery.
    page=await context.newPage(); watch(page);
    await page.goto(new URL('/studio?region=code&placement=one&dock=device',base).href);
    await ready(page);
    const editor=page.locator('#editor-content');
    const original=await state(page);
    const submit=async button=>{
      const responsePromise=page.waitForResponse(response=>new URL(response.url()).pathname==='/__actions/source' && response.request().method()==='POST');
      await button(); const response=await responsePromise;
      await ready(page);
      return response;
    };
    const checked=fixture+'// check this draft, do not save 🎵\n';
    await editor.fill(checked);
    let response=await submit(()=>page.getByRole('button',{name:'Check file',exact:true}).click());
    assert.equal(response.status(),422,'Check uses the retained-draft validation result');
    assert.match((await response.json()).message,/File check passed/);
    assert.deepEqual(await state(page),original,'Check changed the saved score');
    assert.equal(await editor.inputValue(),checked);
    await visibleText(page);
    const invalid='cicada 2\nthis is invalid\n';
    await editor.fill(invalid);
    response=await submit(()=>page.getByRole('button',{name:'Save file',exact:true}).click());
    assert.equal(response.status(),422);
    assert.match((await response.json()).message,/syntax|CICADA-/i);
    assert.deepEqual(await state(page),original,'invalid source reached disk');
    assert.equal(await editor.inputValue(),invalid,'422 discarded the invalid draft');
    await visibleText(page);
    const saved=fixture+'// saved with keyboard 🎵 café\n';
    await editor.fill(saved);
    // Only replace the submitted revision: the real backend must reject it.
    let stale=false;
    await page.route('**/__actions/source',async route=>{
      const request=route.request();
      if(stale) return route.continue();
      stale=true;
      const contentType=request.headers()['content-type'];
      let data=request.postData();
      if(contentType.includes('application/json')) {const values=JSON.parse(data);values.fileRevision='stale-editor-test';data=JSON.stringify(values);}
      else if(contentType.includes('application/x-www-form-urlencoded')) {const values=new URLSearchParams(data);values.set('fileRevision','stale-editor-test');data=values.toString();}
      else data=data.replace(/(name="fileRevision"\r\n\r\n)[^\r\n]*/,'$1stale-editor-test');
      await route.continue({postData:data});
    });
    response=await submit(()=>page.getByRole('button',{name:'Save file',exact:true}).click());
    await page.unroute('**/__actions/source');
    assert.equal(stale,true);
    assert.equal(response.status(),409,'stale source revision was not rejected by the backend');
    assert.deepEqual(await state(page),original,'stale source overwrote the score');
    assert.equal(await editor.inputValue(),saved,'409 discarded the corrected draft');
    await visibleText(page);
    response=await submit(async()=>{await editor.focus();await page.keyboard.press('Control+s');});
    assert.ok([200,303].includes(response.status()),'keyboard Save failed');
    await until(async()=>lf((await state(page)).source)===saved,'keyboard Save did not persist the current buffer');
    await ready(page); await visibleText(page);
    assert.equal(await editor.inputValue(),saved);
    assert.ok(['idle','success'].includes(await page.locator('#editor-native-form').getAttribute('data-gosx-form-state')),'a successful correction must clear the previous source validation error state');
    assert.equal(await page.locator('#score .editor-save-status-error').count(),0,'a successful correction must clear editor error feedback');
    assert.equal(new URL(page.url()).searchParams.get('region'),'code','save hid the Code region');
    assert.deepEqual(errors,[],'editor callback errors');
    t.diagnostic('Valid Check did not save; invalid422 and stale409 kept the draft; keyboard Save recovered and retained visible Unicode source');
  } finally {
    await browser.close();
  }
});


test('Studio Code remounts after managed project file navigation and retains checked drafts per file', {timeout:90000,skip:process.env.CICADA_EDITOR_PROJECT!=='1'}, async t => {
  const base=process.env.CICADA_STUDIO_URL;
  assert.ok(base,'set CICADA_STUDIO_URL to a fresh --write-project Studio');
  assert.ok(['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
  const browser=await playwright().chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});
  const context=await browser.newContext({viewport:{width:1440,height:960}});
  const page=await context.newPage(), errors=[];
  page.setDefaultTimeout(15000);
  page.on('pageerror',error=>errors.push(error.message));
  const projectRoot=process.env.CICADA_EDITOR_PROJECT_ROOT;
  assert.ok(projectRoot,'set CICADA_EDITOR_PROJECT_ROOT to the private --write-project directory');
  const files=async()=>({files:['main.cicada','parts/patterns.cicada'].map(name=>({name,source:fs.readFileSync(path.join(projectRoot,name),'utf8')}))});
  const ready=async()=>{
    await page.waitForFunction(()=>window.__gosx?.ready===true);
    await page.locator('#studio-shell[data-shell-actions=ready]').waitFor();
    await page.locator('.editor-page.editor-highlight-ready').waitFor();
    await page.waitForFunction(()=>!document.querySelector('form[data-gosx-form-state=pending]'));
  };
  const editor=page.locator('#editor-content');
  const visible=async()=>until(async()=>{
    const value=await editor.inputValue(),rendered=await page.locator('#editor-highlight-content').textContent();
    return rendered===value || rendered===value+'\n';
  },'managed file editor rendered stale code');
  const check=async()=>{
    const response=page.waitForResponse(response=>new URL(response.url()).pathname==='/__actions/source' && response.request().method()==='POST');
    await page.getByRole('button',{name:'Check file',exact:true}).click();
    assert.equal((await response).status(),422);
    await ready();
  };
  const navigate=async name=>{
    await page.getByRole('navigation',{name:'Project files',exact:true}).getByRole('link',{name,exact:true}).click();
    await until(async()=>new URL(page.url()).searchParams.get('file')===name && await page.locator('#editor-native-form input[name=file]').inputValue()===name,'file link did not load '+name);
    await ready();
    assert.equal(await page.evaluate(()=>window.__editorProjectDocument===document),true,'project file link replaced the document');
  };
  try {
    await page.goto(new URL('/studio?region=code&file=main.cicada',base).href); await ready();
    const original=await files();
    assert.equal(await page.getByRole('navigation',{name:'Project files',exact:true}).getByRole('link').count(),2,'Studio must load both private project files');
    assert.equal(original.files.find(file=>file.name==='main.cicada')?.source,projectEntry);
    assert.equal(original.files.find(file=>file.name==='parts/patterns.cicada')?.source,projectPart);
    await page.evaluate(()=>{window.__editorProjectDocument=document;});
    const mainID=await page.locator('[data-editor-surface=code]').evaluate(node=>node.closest('.editor-page').id);
    const mainDraft=projectEntry+'// main file draft 🎵\n';
    await editor.fill(mainDraft); await visible(); await check();
    assert.equal(await editor.inputValue(),mainDraft);
    await navigate('parts/patterns.cicada');
    const partID=await page.locator('[data-editor-surface=code]').evaluate(node=>node.closest('.editor-page').id);
    assert.notEqual(partID,mainID,'each project file must mount its own editor identity');
    assert.equal(await editor.inputValue(),projectPart,'main draft leaked into the pattern file');
    await editor.focus(); await editor.press('Control+End');
    await editor.press('Tab'); await page.keyboard.insertText('// file switch input café 🎵');
    const partDraft=projectPart+'  // file switch input café 🎵';
    assert.equal(await editor.inputValue(),partDraft,'newly mounted editor did not accept configured Tab and Unicode');
    await visible();
    await until(async()=>await page.locator('#editor-highlight-content .syntax-keyword-function').allTextContents().then(values=>values.includes('pattern')),'live highlighting did not mount for the second file');
    await editor.press('Control+f');
    await page.locator('[data-code-find=query]').fill('pulse');
    assert.equal(await page.locator('.editor-code-find-status').textContent(),'1 / 1','Find did not mount on the second editor');
    await page.locator('[data-code-find=query]').press('Escape');
    await check();
    assert.deepEqual(await files(),original,'checking either file changed disk');
    await navigate('main.cicada');
    assert.equal(await editor.inputValue(),mainDraft,'returning to entry lost its checked draft');
    await editor.focus(); await editor.press('Control+End'); await editor.press('Tab');
    assert.equal(await editor.inputValue(),mainDraft+'  ','returning editor lost keyboard handlers');
    await visible();
    await editor.press('Control+z');
    assert.equal(await editor.inputValue(),mainDraft,'returning editor lost local history');
    await navigate('parts/patterns.cicada');
    assert.equal(await editor.inputValue(),partDraft,'returning to part lost its own checked draft');
    await visible();
    await editor.press('Control+f');
    await page.locator('[data-code-find=query]').fill('café');
    assert.equal(await page.locator('.editor-code-find-status').textContent(),'1 / 1');
    await page.getByRole('button',{name:'Close find and replace',exact:true}).click();
    await page.goBack();
    await until(async()=>new URL(page.url()).searchParams.get('file')==='main.cicada' && await page.locator('#editor-native-form input[name=file]').inputValue()==='main.cicada','browser Back did not load the entry file');
    await ready(); await visible();
    assert.equal(await editor.inputValue(),mainDraft,'browser Back lost the entry draft');
    assert.equal(await page.evaluate(()=>window.__editorProjectDocument===document),true,'browser Back replaced the editor document');
    await editor.focus(); await editor.press('Control+End'); await editor.press('Tab');
    assert.equal(await editor.inputValue(),mainDraft+'  ','browser Back lost editor keyboard handlers');
    await editor.press('Control+z');
    assert.equal(await editor.inputValue(),mainDraft);
    await page.goForward();
    await until(async()=>new URL(page.url()).searchParams.get('file')==='parts/patterns.cicada' && await page.locator('#editor-native-form input[name=file]').inputValue()==='parts/patterns.cicada','browser Forward did not load the pattern file');
    await ready(); await visible();
    assert.equal(await editor.inputValue(),partDraft,'browser Forward lost the pattern draft');
    assert.deepEqual(await files(),original,'project navigation or local editing wrote a file');
    assert.deepEqual(errors,[],'managed editor lifecycle callback errors');
    t.diagnostic('Distinct entry/part editors remounted typing, Tab, history, Find and live highlighting on managed links and browser Back/Forward; each checked draft stayed with its own file, disk unchanged');
  } finally {await browser.close();}
});
