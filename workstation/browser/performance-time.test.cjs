'use strict';
const fs=require('node:fs'),path=require('node:path');
if(process.argv[2]==='--write-fixture'){
 const root=path.resolve(process.argv[3]);fs.mkdirSync(root,{recursive:true});if(fs.existsSync(path.join(root,'main.cicada')))throw new Error('Use a new disposable fixture directory');
 fs.writeFileSync(path.join(root,'cicada.mod'),'project performance-time\ncicada 2\nentry "main.cicada"\nsource "phrase.cicada"\n');
 fs.writeFileSync(path.join(root,'main.cicada'),'// cicada-performance-time-test: disposable fixture\ncicada 2\ntitle "A phrase in four paces"\ntempo 120\nseed 42\ninstrument glass { voice poly { out=sine(pitch)*env(gate,150ms)*(0.1+pressure*0.05+timbre*0.05) } }\ntrack keys glass { level=-24dB }\narrange { place opening keys phrase { at=@1.1.1 length=2bars } }\n');
 const phrase=`// Keep the phrase in its owning file.
pattern phrase performance length=3840ticks slot=3 seed=99 {
 gesture-group touch {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=1ticks bend=10ct pressure=0.1 timbre=0.6;
  gesture at=2ticks bend=20ct pressure=0.2 timbre=0.7;
  gesture at=960ticks bend=200ct pressure=0.8 timbre=0.9;
  gesture at=3839ticks bend=-50ct pressure=0.3 timbre=0.4;
 }
 note 60 at=17ticks length=703ticks velocity=103 group=touch {
  gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7;
  gesture at=1ticks bend=50ct pressure=0.3 timbre=0.8;
  gesture at=2ticks bend=75ct pressure=0.4 timbre=0.9;
  gesture at=703ticks bend=100ct pressure=0.5 timbre=0.6;
 }
 note 64 at=19ticks length=1301ticks velocity=87 group=touch;
} // Keep this outside comment.
`;
 fs.writeFileSync(path.join(root,'phrase.cicada'),phrase.replaceAll('\n','\r\n'));
 process.stdout.write(JSON.stringify({score:path.join(root,'main.cicada'),phrase:path.join(root,'phrase.cicada')})+'\n');process.exit(0);
}
const test=require('node:test'),assert=require('node:assert/strict');
test('reshape a secondary played phrase with both gesture layers',{timeout:240000},async t=>{
 const base=new URL(process.env.CICADA_STUDIO_URL),proof=process.env.CICADA_TIME_PROOF_DIR;assert.equal(base.hostname,'127.0.0.1');assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']});t.after(()=>browser.close());
 const page=await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.exposeFunction('__cicadaTimeAudio',(label,base64)=>fs.writeFileSync(path.join(proof,label+'.wav'),Buffer.from(base64,'base64')));
 await page.exposeFunction('__cicadaTimeSubmit',async ratio=>{await page.locator('[data-performance-time=phrase] [name=scale][value="'+ratio+'"]').press('Enter');});
 await page.exposeFunction('__cicadaTimeViews',async()=>{
  const section=page.locator('[data-performance-time=phrase]');await section.screenshot({path:path.join(proof,'time-desktop.png')});
  await page.setViewportSize({width:390,height:844});await page.waitForTimeout(150);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await section.screenshot({path:path.join(proof,'time-phone.png')});await page.setViewportSize({width:1440,height:1100});
 });
 if(process.env.CICADA_TIME_REOPEN==='1'){const previous=JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8')).result;await page.addInitScript(previous=>sessionStorage.setItem('cicada.performance-time-proof',JSON.stringify({...previous,phase:'reopen'})),previous);}
 await page.addInitScript(()=>{window.__cicadaTimeResult=result=>{window.__timeResult=result;};});await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-performance-time-workflow.js')});
 await page.goto(base.href);await page.goto(base.origin+'/studio?dock=clip&pattern=phrase&note=0&region=detail');await page.waitForFunction(()=>window.__timeResult,null,{timeout:220000});const result=await page.evaluate(()=>window.__timeResult);
 fs.writeFileSync(path.join(proof,process.env.CICADA_TIME_REOPEN==='1'?'reopen-report.json':'report.json'),JSON.stringify({result,errors},null,2)+'\n');
 if(!result.ok){await page.screenshot({path:path.join(proof,'failure.png')});fs.writeFileSync(path.join(proof,'failure.html'),await page.content());}
 assert.equal(result.ok,true,result.error);assert.deepEqual(errors,[]);
});
