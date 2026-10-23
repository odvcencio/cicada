'use strict';
const fs=require('node:fs'),path=require('node:path');
if(process.argv[2]==='--write-fixture'){
 const folder=path.resolve(process.argv[3]);fs.mkdirSync(folder,{recursive:true});if(fs.existsSync(path.join(folder,'main.cicada')))throw new Error('Use a new disposable directory');
 const files={
  'main.cicada':'// cicada-grid-sources-test: disposable fixture\ncicada 2\ntitle "Develop the idea"\ntempo 120\nseed 42\ntrack lead acid { level=-24dB cutoff=700Hz }\ntrack keys piano { level=-24dB }\ntrack drums drums { level=-24dB }\nscene main { lead=line keys=harmony drums=beat }\nsong { main*2 }\n',
  'lead.cicada':'// keep the lead source\npattern line acid steps=8 { use motif use motif +12 } // keep lead comment\n',
  'keys.cicada':'// keep the keys source\npattern harmony notes steps=8 { c4 - . . g4 . . . } // keep keys comment\n',
  'drums.cicada':'// keep the drum source\npattern beat drums steps=8 { sd: ..x..... bd: x....... ch: x.x.x.x. } // keep drum comment\n',
  'phrases.cicada':'// the shared phrase stays shared\nphrase motif { c4 . e4 . }\n',
  'cicada.mod':'project grid-sources\ncicada 2\nentry "main.cicada"\nsource "lead.cicada"\nsource "keys.cicada"\nsource "drums.cicada"\nsource "phrases.cicada"\n'};
 for(const [file,text]of Object.entries(files))fs.writeFileSync(path.join(folder,file),text.replaceAll('\n','\r\n'));process.stdout.write(JSON.stringify({score:path.join(folder,'main.cicada'),phrase:path.join(folder,'phrases.cicada')})+'\n');process.exit(0);
}
const test=require('node:test'),assert=require('node:assert/strict');
test('owned grid sources remain editable through drawing, export and fresh reopen',{timeout:300000},async t=>{
 const base=new URL(process.env.CICADA_STUDIO_URL),proof=process.env.CICADA_GRID_SOURCES_PROOF_DIR,fixture=JSON.parse(fs.readFileSync(process.env.CICADA_GRID_SOURCES_FIXTURE,'utf8'));assert.equal(base.hostname,'127.0.0.1');assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});fs.writeFileSync(path.join(proof,'fixture.json'),JSON.stringify(fixture,null,2)+'\n');
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']});t.after(()=>browser.close());const page=await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 let staleProjection=false;await page.exposeFunction('__cicadaGridArmStaleProjection',()=>{staleProjection=true;});
 await page.route('**/__actions/pattern',async route=>{
  if(!staleProjection)return route.continue();staleProjection=false;const response=await route.fetch(),body=await response.json();assert.ok(body.ok);assert.match(body.data.writeSourceRevision,/^[0-9a-f]{64}$/);assert.equal(body.data.writeRevision,body.data.revision);
  fs.appendFileSync(fixture.phrase,'// Outside save between acknowledgment and projection.\r\n');const projection=await page.request.get(base.origin+'/__workspace?panel=code&file=phrases.cicada');assert.ok(projection.ok());body.data.sourceRevision=(await projection.json()).sourceRevision;
  // The server-side race test covers refreshRequired. Suppress that flag here
  // to independently exercise the WASM client's write-source comparison.
  body.data.refreshRequired=false;await route.fulfill({response,json:body});
 });
 await page.exposeFunction('__cicadaGridAudio',(label,data)=>fs.writeFileSync(path.join(proof,label+'.wav'),Buffer.from(data,'base64')));
 await page.exposeFunction('__cicadaGridOutsideSave',()=>{fs.appendFileSync(fixture.phrase,'// Outside phrase save stays intact.\r\n');return fs.readFileSync(fixture.phrase,'utf8').replaceAll('\r\n','\n');});
 await page.exposeFunction('__cicadaGridViews',async()=>{await page.getByRole('separator',{name:'Resize Detail dock'}).press('End');await page.locator('.pattern-workspace .form-status').filter({hasText:'Saved project files or libraries changed'}).scrollIntoViewIfNeeded();await page.screenshot({path:path.join(proof,'grid-desktop.png')});await page.setViewportSize({width:390,height:844});await page.locator('.shell-phone-nav a').filter({hasText:'Detail'}).click();await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth,null,{timeout:10000});await page.locator('.pattern-workspace .form-status').filter({hasText:'Saved project files or libraries changed'}).scrollIntoViewIfNeeded();await page.screenshot({path:path.join(proof,'grid-phone.png')});await page.setViewportSize({width:1440,height:1100});});
 if(process.env.CICADA_GRID_SOURCES_REOPEN==='1'){const previous=JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8')).result;await page.addInitScript(previous=>sessionStorage.setItem('cicada.grid-sources-proof',JSON.stringify({...previous,phase:'reopen'})),previous);}
 await page.addInitScript(()=>{window.__cicadaGridResult=result=>{window.__gridSourcesResult=result;};});await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-grid-sources-workflow.js')});await page.goto(base.href);await page.goto(base.origin+'/studio?dock=clip&pattern=line&region=detail');await page.waitForFunction(()=>window.__gridSourcesResult,null,{timeout:280000});const result=await page.evaluate(()=>window.__gridSourcesResult);
 fs.writeFileSync(path.join(proof,process.env.CICADA_GRID_SOURCES_REOPEN==='1'?'reopen-report.json':'report.json'),JSON.stringify({result,errors},null,2)+'\n');if(!result.ok){await page.screenshot({path:path.join(proof,'failure.png')});fs.writeFileSync(path.join(proof,'failure.html'),await page.content());}assert.equal(result.ok,true,result.error);assert.deepEqual(errors,[]);
});
