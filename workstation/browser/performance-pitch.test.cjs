'use strict';
const fs=require('node:fs'),path=require('node:path');
if(process.argv[2]==='--write-fixture'){
 const root=path.resolve(process.argv[3]);fs.mkdirSync(root,{recursive:true});if(fs.existsSync(path.join(root,'main.cicada')))throw new Error('Use a new disposable fixture directory');
 fs.writeFileSync(path.join(root,'cicada.mod'),'project performance-pitch\ncicada 2\nentry "main.cicada"\nsource "phrase.cicada"\n');
 fs.writeFileSync(path.join(root,'main.cicada'),'// cicada-performance-pitch-test: disposable fixture\ncicada 2\ntitle "A melody answers itself"\ntempo 120\nseed 42\ninstrument glass { voice poly { out=sine(pitch)*env(gate,150ms)*(0.1+pressure*0.05+timbre*0.05) } }\ntrack keys glass { level=-24dB }\narrange { place opening keys phrase { at=@1.1.1 length=2bars } }\n');
 const phrase=`// Keep the phrase in its owning file.
pattern phrase performance length=3840ticks slot=3 transpose=12 seed=99 {
 // The performed articulation stays with the melody.
 gesture-group touch {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=960ticks bend=200ct pressure=0.8 timbre=0.9;
  gesture at=3839ticks bend=-50ct pressure=0.3 timbre=0.4;
 }
 note 60 at=17ticks length=703ticks velocity=103 group=touch {
  gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7 vibrato=12ct rate=7Hz;
  gesture at=703ticks bend=100ct pressure=0.5 timbre=0.6;
 }
 note 64 at=979ticks length=341ticks velocity=87 group=touch;
 note 67 at=1943ticks length=677ticks velocity=111 group=touch;
 note 72 at=1943ticks length=677ticks velocity=78 group=touch;
} // Keep this outside comment.
`;
 fs.writeFileSync(path.join(root,'phrase.cicada'),phrase.replaceAll('\n','\r\n'));process.stdout.write(JSON.stringify({score:path.join(root,'main.cicada'),phrase:path.join(root,'phrase.cicada')})+'\n');process.exit(0);
}
const test=require('node:test'),assert=require('node:assert/strict');
test('develop secondary played melodies by shifting and mirroring their notes',{timeout:240000},async t=>{
 const base=new URL(process.env.CICADA_STUDIO_URL),proof=process.env.CICADA_PITCH_PROOF_DIR;assert.equal(base.hostname,'127.0.0.1');assert.ok(path.isAbsolute(proof));fs.mkdirSync(proof,{recursive:true});
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--mute-audio','--no-sandbox']});t.after(()=>browser.close());const page=await browser.newPage({viewport:{width:1440,height:1100},reducedMotion:'reduce'}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.exposeFunction('__cicadaPitchAudio',(label,base64)=>fs.writeFileSync(path.join(proof,label+'.wav'),Buffer.from(base64,'base64')));
 await page.exposeFunction('__cicadaPitchSubmit',async operation=>{await page.locator('[data-performance-pitch=phrase] form').filter({has:page.locator('[name=pitch_operation][value="'+operation+'"]')}).locator('button[type=submit]').press('Enter');});
 await page.exposeFunction('__cicadaPitchViews',async()=>{const section=page.locator('[data-performance-pitch=phrase]');await section.screenshot({path:path.join(proof,'pitch-desktop.png')});await page.setViewportSize({width:390,height:844});await page.waitForTimeout(150);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await section.screenshot({path:path.join(proof,'pitch-phone.png')});await page.setViewportSize({width:1440,height:1100});});
 if(process.env.CICADA_PITCH_REOPEN==='1'){const previous=JSON.parse(fs.readFileSync(path.join(proof,'report.json'),'utf8')).result;await page.addInitScript(previous=>sessionStorage.setItem('cicada.performance-pitch-proof',JSON.stringify({...previous,phase:'reopen'})),previous);}
 await page.addInitScript(()=>{window.__cicadaPitchResult=result=>{window.__pitchResult=result;};});await page.addInitScript({path:path.resolve(__dirname,'../../desktop/testdata/native-performance-pitch-workflow.js')});await page.goto(base.href);await page.goto(base.origin+'/studio?dock=clip&pattern=phrase&note='+(process.env.CICADA_PITCH_REOPEN==='1'?'1':'0')+'&track=keys&region=detail');await page.waitForFunction(()=>window.__pitchResult,null,{timeout:220000});const result=await page.evaluate(()=>window.__pitchResult);
 fs.writeFileSync(path.join(proof,process.env.CICADA_PITCH_REOPEN==='1'?'reopen-report.json':'report.json'),JSON.stringify({result,errors},null,2)+'\n');if(!result.ok){await page.screenshot({path:path.join(proof,'failure.png')});fs.writeFileSync(path.join(proof,'failure.html'),await page.content());}assert.equal(result.ok,true,result.error);assert.deepEqual(errors,[]);
});
