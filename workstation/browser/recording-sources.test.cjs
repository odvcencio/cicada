'use strict';
const fs=require('node:fs'),path=require('node:path');
if(process.argv[2]==='--write-fixture'){
 const root=path.resolve(process.argv[3]);fs.mkdirSync(root,{recursive:true});if(fs.existsSync(path.join(root,'main.cicada')))throw new Error('Use a new disposable directory');
 const texts={
  'main.cicada':'// cicada-recording-sources-test: disposable fixture\ncicada 2\ntitle "Keep the complete take"\ntempo 120\nseed 42\nscene main { lead=line keys=answer }\nsong { main*2 }\n',
  'lead.cicada':'instrument glass { voice mono { out=sine(pitch)*env(gate,200ms)*(0.1+pressure*0.1+timbre*0.1) } }\ntrack lead glass { level=-24dB }\n// lead stays here\npattern line notes steps=16 { . . . . . . . . . . . . . . . . } // keep lead comment\n',
  'keys.cicada':'track keys piano { level=-24dB }\n// keys stay here\npattern answer notes steps=16 { . . . . . . . . . . . . . . . . } // keep keys comment\n',
  'cicada.mod':'project recording-sources\ncicada 2\nentry "main.cicada"\nsource "lead.cicada"\nsource "keys.cicada"\n'};
 for(const [file,text]of Object.entries(texts))fs.writeFileSync(path.join(root,file),text.replaceAll('\n','\r\n'));process.stdout.write(JSON.stringify({score:path.join(root,'main.cicada'),lead:path.join(root,'lead.cicada'),keys:path.join(root,'keys.cicada')})+'\n');process.exit(0);
}
const test=require('node:test'),assert=require('node:assert/strict');
test('retained grid and played takes commit to owned project sources',{timeout:300000},async t=>{
 const base=new URL(process.env.CICADA_STUDIO_URL),proof=process.env.CICADA_RECORD_SOURCES_PROOF_DIR,fixture=JSON.parse(fs.readFileSync(process.env.CICADA_RECORD_SOURCES_FIXTURE,'utf8'));assert.equal(base.hostname,'127.0.0.1');assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 fs.writeFileSync(path.join(proof,'fixture.json'),JSON.stringify(fixture,null,2)+'\n');
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']});t.after(()=>browser.close());const page=await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 // Exercise the native stream adapter on Linux CI too. The shared workflow
 // supplies every MIDI packet in memory and intercepts the device endpoint.
 await page.route('**/studio*',async route=>{if(!route.request().isNavigationRequest())return route.continue();const response=await route.fetch(),body=await response.text();assert.ok(/"nativeMIDI":(true|false)/.test(body));await route.fulfill({response,body:body.replaceAll('"nativeMIDI":false','"nativeMIDI":true')});});
 await page.exposeFunction('__cicadaRecordingAudio',(label,data)=>fs.writeFileSync(path.join(proof,label+'.wav'),Buffer.from(data,'base64')));
 await page.exposeFunction('__cicadaRecordingOutsideSave',async()=>{fs.appendFileSync(fixture.keys,'// Outside keys save during the held take.\r\n');const r=await page.request.get(base.origin+'/__workspace?panel=live');assert.ok(r.ok());return (await r.json()).sourceRevision;});
 await page.exposeFunction('__cicadaRecordingViews',async()=>{const notice=page.locator('.note-preview .take-check').filter({hasText:'Project files changed'});await notice.scrollIntoViewIfNeeded();await page.screenshot({path:path.join(proof,'recording-desktop.png')});await page.setViewportSize({width:390,height:844});await page.locator('.shell-phone-nav a').filter({hasText:'Center'}).click();await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth,null,{timeout:10000});await notice.scrollIntoViewIfNeeded();await page.screenshot({path:path.join(proof,'recording-phone.png')});await page.setViewportSize({width:1440,height:1100});});
 if(process.env.CICADA_RECORD_SOURCES_REOPEN==='1'){const previous=JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8')).result;await page.addInitScript(previous=>sessionStorage.setItem('cicada.recording-sources-proof',JSON.stringify({...previous,phase:'reopen'})),previous);}
 await page.addInitScript(()=>{window.__cicadaRecordingResult=result=>{window.__recordingSourcesResult=result;};});await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-recording-sources-workflow.js')});await page.goto(base.href);await page.goto(base.origin+'/studio?tab=session&region=center');await page.waitForFunction(()=>window.__recordingSourcesResult,null,{timeout:280000});const result=await page.evaluate(()=>window.__recordingSourcesResult);
 fs.writeFileSync(path.join(proof,process.env.CICADA_RECORD_SOURCES_REOPEN==='1'?'reopen-report.json':'report.json'),JSON.stringify({result,errors},null,2)+'\n');if(!result.ok){await page.screenshot({path:path.join(proof,'failure.png')});fs.writeFileSync(path.join(proof,'failure.html'),await page.content());}assert.equal(result.ok,true,result.error);assert.deepEqual(errors,[]);
});
