// Owner-session Studio: create a part from an unambiguous silent scene cell.
'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');

const fixture=`// cicada-silent-scene-create: disposable fixture
cicada 2
title "Silent repeated scene"
track lead acid {}
pattern pulse { 1 . 5 . }
scene air { lead = keep }
scene tone { lead = pulse }
scene quiet { lead = off }
scene carry { lead = keep }
song { air tone air quiet carry quiet }
`;
if(process.argv[2]==='--write-fixture'){
  assert.ok(process.argv[3],'supply a new disposable score path');
  fs.writeFileSync(process.argv[3],fixture,{flag:'wx'});
  process.exit(0);
}

test('a silent scene cell opens an explicit, scene-scoped pattern creation flow',{timeout:120000},async t=>{
  const base=new URL(process.env.CICADA_STUDIO_URL),proof=process.env.CICADA_SILENT_SCENE_PROOF_DIR;
  assert.ok(['127.0.0.1','localhost','[::1]'].includes(base.hostname));
  assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
  const score=process.env.CICADA_SILENT_SCENE_SCORE;
  assert.ok(path.isAbsolute(score),'supply the disposable score path for the stale-source check');
  const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');
  const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});
  t.after(()=>browser.close(),{timeout:15000});
  const context=await browser.newContext({viewport:{width:1600,height:1000}}),page=await context.newPage(),errors=[],posts=[];
  page.on('pageerror',error=>errors.push(error.message));
  page.on('request',request=>{if(request.method()==='POST'&&new URL(request.url()).pathname==='/__actions/pattern')posts.push(request)});
  const until=async(fn,label)=>{const end=Date.now()+20000;while(Date.now()<end){if(await fn())return;await new Promise(resolve=>setTimeout(resolve,40))}assert.fail(label)};
  const state=async()=>{const response=await page.request.get(new URL('/api/state',base).href);assert.equal(response.status(),200);return response.json()};
  const ready=async()=>{await page.locator('#studio-shell[data-shell-actions=ready][data-shell-edits=ready]').waitFor();await until(async()=>await page.locator('#undo-form input[name=revision]').inputValue()===(await state()).revision,'score revision did not settle')};
  const exact=async(source,label)=>until(async()=>(await state()).source===source,label);
  const cell=(scene)=>page.locator('#arrangement [title="'+scene+' / lead"]');
  await page.goto(new URL('/studio',base).href);await ready();
  const original=await state();assert.equal(original.source,fixture,'runner requires the untouched marked fixture');
  assert.equal(await cell('air').count(),2);
  assert.match(await cell('air').first().innerText(),/Silence/);
  assert.match(await cell('air').last().innerText(),/pulse/);
  assert.equal(await cell('air').first().getAttribute('data-scene-create'),null,'ambiguous keep must not offer creation');
  assert.equal(await cell('air').last().getAttribute('data-scene-create'),null,'sounding occurrence must not offer creation');
  assert.equal(await cell('quiet').count(),2);
  const createCell=cell('quiet').first();
  assert.equal(await createCell.getAttribute('data-scene-create'),'quiet','the explicit off cell must offer creation');
  assert.equal(await cell('quiet').last().getAttribute('data-scene-create'),'quiet','the repeated explicit off cell must offer the same action');

  // Dirty Code blocks the navigation, with its local text intact.
  if(!(await page.locator('#studio-code > details').evaluate(node=>node.open)))await page.locator('.shell-pane-toggle[data-pane=code][data-pane-action=show]').click();
  const editor=page.locator('#score textarea').first(),draft=fixture+'// keep my unsaved words\n';
  await editor.fill(draft);const dirtyWrites=posts.length;
  await page.evaluate(()=>{window.__silentSceneNavigations=0;document.addEventListener('gosx:navigate',()=>window.__silentSceneNavigations++);});
  await createCell.click();await page.waitForTimeout(150);
  assert.equal(await page.evaluate(()=>window.__silentSceneNavigations),0);assert.equal(new URL(page.url()).searchParams.has('createScene'),false);
  assert.equal(await editor.inputValue(),draft);assert.equal(posts.length,dirtyWrites);assert.equal((await state()).source,fixture);
  await editor.fill(fixture);await until(async()=>/No unsaved Code edits/.test(await page.locator('[data-source-recovery-status]').textContent()),'restored draft did not clear');

  const writesBefore=posts.length;
  await createCell.click();
  const create=page.locator('#detail-panel-clip [data-gosx-key="create-pattern-lead"]');
  await create.waitFor({state:'visible'});
  assert.match(await page.locator('#studio-detail').innerText(),/New pattern[\s\S]*lead\s*·\s*quiet/,'the dock must identify the new pattern and destination');
  assert.equal(await create.evaluate(node=>node.open),true,'the create form must open');
  assert.equal(await create.locator('[name=track]').inputValue(),'lead');
  assert.equal(await create.locator('[name=scene]').inputValue(),'quiet');
  assert.equal(posts.length,writesBefore,'opening the form must not submit a pattern edit');
  assert.equal((await state()).source,fixture);
  const warning=create.locator('[data-scene-create-scope]');
  await warning.waitFor({state:'visible'});
  const scope=await warning.innerText();
  assert.match(scope,/quiet/);assert.match(scope,/Stop/i);assert.match(scope,/every occurrence/i);assert.match(scope,/carried/i);assert.match(scope,/live scene launches/i);
  const visibleInDock=locator=>locator.evaluate(node=>{
    const item=node.getBoundingClientRect(),pane=document.querySelector('#studio-detail .shell-scroll').getBoundingClientRect();
    return item.top>=pane.top-1 && item.bottom<=Math.min(pane.bottom,innerHeight)+1 && item.left>=pane.left-1 && item.right<=pane.right+1;
  });
  const submit=create.getByRole('button',{name:'Create pattern',exact:true});
  assert.equal(await submit.getAttribute('aria-describedby'),'scene-create-scope','the create action must announce its scope');
  assert.equal(await visibleInDock(warning),true,'the scope warning must fit in the desktop dock without scrolling');
  assert.equal(await visibleInDock(submit),true,'the create button must fit in the desktop dock without scrolling');
  await page.screenshot({path:path.join(proof,'silent-scene-create-desktop.png')});
  await page.setViewportSize({width:390,height:844});
  assert.equal(await create.isVisible(),true,'the preselected create form must remain reachable on phone');
  assert.equal(await visibleInDock(warning),true,'the scope warning must fit in the phone dock without scrolling');
  assert.equal(await visibleInDock(submit),true,'the create button must fit in the phone dock without scrolling');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'the phone view must not overflow');
  await page.screenshot({path:path.join(proof,'silent-scene-create-phone.png')});
  await page.setViewportSize({width:1600,height:1000});

  await create.locator('[name=newName]').fill('quiet-line');
  await create.getByRole('button',{name:'Create pattern',exact:true}).click();
  await until(async()=>{const s=(await state()).source;return s.includes('pattern quiet-line')&&/scene quiet\s*\{[^}]*lead\s*=\s*quiet-line/.test(s)},'create did not assign the pattern to quiet');await ready();
  const created=(await state()).source;
  assert.match(created,/scene tone\s*\{[^}]*lead\s*=\s*pulse/,'the sounding scene must remain unchanged');
  assert.match(created,/scene carry\s*\{[^}]*lead\s*=\s*keep/,'the later carry remains explicit in the score');
  const grid=page.locator('#detail-panel-clip .piano-roll[data-pattern-id="quiet-line"]');await grid.waitFor();
  await grid.locator('.roll-cell[data-active=false]').first().click();
  await until(async()=>(await state()).source!==created,'drawing did not save a note');
  await page.screenshot({path:path.join(proof,'silent-scene-created-grid.png')});
  await page.getByRole('button',{name:'Undo',exact:true}).click();await exact(created,'one Undo did not remove the drawn note');await ready();
  await page.getByRole('button',{name:'Undo',exact:true}).click();await exact(fixture,'one Undo did not remove the create-and-assign edit');await ready();

  // A score changed on disk after the create form opened must not be overwritten.
  await page.goto(new URL('/studio',base).href);await ready();await cell('quiet').first().click();await create.waitFor({state:'visible'});
  await create.locator('[name=newName]').fill('stale-line');
  const external=fixture+'// concurrent external score edit\n';fs.writeFileSync(score,external);
  const staleResponse=page.waitForResponse(response=>response.request().method()==='POST'&&new URL(response.url()).pathname==='/__actions/pattern');
  await create.getByRole('button',{name:'Create pattern',exact:true}).click();
  const refusal=await staleResponse;assert.equal(refusal.status(),409,'stale create must return a conflict');
  assert.equal(fs.readFileSync(score,'utf8'),external,'stale create overwrote external text');
  assert.ok(!fs.readFileSync(score,'utf8').includes('pattern stale-line'));
  assert.deepEqual(errors,[]);
  fs.writeFileSync(path.join(proof,'silent-scene-create.json'),JSON.stringify({fixture:'air tone air quiet carry quiet',ambiguousKeepRefused:true,repeatedOffEligible:true,openingWrites:0,track:'lead',scene:'quiet',drawUndo:true,creationUndo:true,dirtyRefused:true,staleRefused:true,viewports:['1600x1000','390x844']},null,2)+'\n');
});
