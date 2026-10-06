// Run after building the exact kernel, Go/WASM bridge and static fixture server.
// This qualifies integration primitives, never the missing GoSX Studio UI.
const {chromium}=require('playwright');
const {spawn}=require('node:child_process');
const path=require('node:path');
const fs=require('node:fs');
const assert=require('node:assert/strict');
(async()=>{
  const repo=path.resolve(__dirname,'../..');
  const evidence=process.env.CICADA_DEMO_EVIDENCE_DIR||path.join(repo,'build/demo-evidence');
  fs.mkdirSync(evidence,{recursive:true});
  const server=spawn(path.join(repo,'build/demo-static-test-server'),[repo,process.env.WASM_EXEC_PATH],{stdio:['ignore','pipe','pipe']});
  let browser;
  try {
    const base=await new Promise((resolve,reject)=>{
      const timeout=setTimeout(()=>reject(new Error('fixture start timeout')),15000);
      server.stdout.on('data',data=>{const url=data.toString().match(/http:\/\/127\.0\.0\.1:\d+/);if(url){clearTimeout(timeout);resolve(url[0]);}});
      server.once('exit',code=>reject(new Error('fixture exited '+code)));
      server.stderr.on('data',data=>process.stderr.write(data));
    });
    browser=await chromium.launch({executablePath:process.env.CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--mute-audio']});
    const page=await browser.newPage();const errors=[];const requests=[];
    page.on('pageerror',error=>errors.push(error.message));
    page.on('request',request=>requests.push(request.url()));
    await page.goto(base);
    await page.waitForFunction(()=>!document.querySelector('#start').disabled);
    await page.click('#start');
    await page.waitForFunction(()=>document.querySelector('#status').textContent.startsWith('Actual AudioWorklet ready'));
    console.log(JSON.stringify({browser:browser.version(),sampleRate:await page.evaluate(()=>qualificationContext.sampleRate),headless:true,acousticListening:false,gosxStudioQualified:false}));
    for(const key of ['a','s','d']){
      await page.click('#play');
      await page.waitForFunction(()=>qualificationPlaying);
      await page.locator('h1').click();await page.keyboard.down(key);
      await page.waitForFunction(()=>qualificationSamples().energy>1e-7);
      const pcm=await page.evaluate(()=>qualificationSamples());assert(pcm.finite);
      console.log(JSON.stringify({key,pcm}));
      if(key==='a'){
        assert.equal(await page.evaluate(()=>cicadaQualification.param('bass.cutoff',1200)), '');
        assert.equal(await page.evaluate(()=>cicadaQualification.param('bass.mute',1)), '');
        await page.waitForTimeout(150);
        assert.equal(await page.evaluate(()=>qualificationSamples().energy),0,'validated live mute must silence actual PCM');
        assert.equal(await page.evaluate(()=>cicadaQualification.param('bass.mute',0)), '');
        await page.waitForFunction(()=>qualificationSamples().energy>1e-7);
        assert.equal(await page.evaluate(()=>cicadaQualification.param('bass.solo',1)), '');
        assert.equal(await page.evaluate(()=>cicadaQualification.param('bass.solo',0)), '');
        assert.match(await page.evaluate(()=>cicadaQualification.param('bass.cutoff',Infinity)),/finite/);
      }
      await page.click('#stop');
      await page.waitForTimeout(250);
      assert.equal(await page.evaluate(()=>qualificationSamples().energy),0,'stop leaves zero output');
      await page.click('#play');await page.locator('h1').click();
      await page.waitForFunction(()=>qualificationPlaying);
      const before=await page.evaluate(()=>qualificationCommands.length);
      await page.keyboard.down(key);
      assert.equal(await page.evaluate(()=>qualificationCommands.length),before,'held repeat after stop must not retrigger');
      await page.keyboard.up(key);await page.keyboard.down(key);
      assert.equal(await page.evaluate(()=>qualificationCommands.length),before+1,'fresh press must play');
      await page.keyboard.up(key);await page.click('#stop');
    }
    await page.evaluate(()=>qualificationResetPromise);
    const refused=await page.evaluate(()=>{const api=cicadaQualification;api.play();api.down('rapid','bass',45,110,false);api.stop();return api.play();});
    assert.match(refused,/fresh worklet kernel image/,'rapid Play must wait for reset');
    await page.evaluate(()=>qualificationResetPromise);
    await page.waitForTimeout(120);
    assert.equal(await page.evaluate(()=>qualificationSamples().energy),0,'rapid stop/play must not revive a queued live note');
    await page.evaluate(()=>{cicadaQualification.up('rapid');cicadaQualification.stop();});
    await page.click('#play');await page.waitForFunction(()=>qualificationPlaying);await page.evaluate(()=>{cicadaQualification.down('midi:1','bass',45,100,false);});
    await page.waitForFunction(()=>qualificationSamples().energy>1e-7);
    await page.evaluate(()=>{const event=new Event('statechange');event.port={type:'input',state:'disconnected'};qualificationMIDI.dispatchEvent(event);});
    await page.waitForTimeout(250);
    assert.equal(await page.evaluate(()=>qualificationSamples().energy),0,'MIDI disconnect must stop');
    for(const kind of ['blur','suspend','hidden']){
      await page.evaluate(()=>qualificationResetPromise);
      await page.evaluate(()=>{cicadaQualification.up('midi:1');cicadaQualification.play();cicadaQualification.down('midi:1','bass',45,100,false);});
      await page.waitForFunction(()=>qualificationSamples().energy>1e-7);
      await page.evaluate(async kind=>{
        if(kind==='blur')window.dispatchEvent(new Event('blur'));
        if(kind==='suspend')await qualificationContext.suspend();
        if(kind==='hidden'){Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'));}
      },kind);
      assert.equal(await page.evaluate(()=>qualificationCommands.at(-1)[0]),2,kind+' posts actual kernel Stop');
      if(kind==='suspend')await page.evaluate(()=>qualificationContext.resume());
      await page.waitForTimeout(250);assert.equal(await page.evaluate(()=>qualificationSamples().energy),0);
    }
    for(const action of ['export.audio','export.midi','export.project','export.stems','export.job','webmcp.export']){
      assert.match(await page.evaluate(action=>cicadaQualification.export(action),action),/CICADA-CAPABILITY/);
    }
    for(const route of ['/api/export','/api/export/midi','/api/project/download','/api/audio/ws','/api/source','/agent/execute','/mcp','/_gosx/rpc/export']){
      for(const method of ['GET','POST'])assert.equal((await page.request.fetch(base+route,{method})).status(),403,method+' '+route);
    }
    assert(!requests.some(url=>new URL(url).pathname.startsWith('/api/')),'playback must not request native APIs');
    assert.deepEqual(errors,[]);assert.equal(await page.evaluate(()=>globalThis.qualificationError||''),'');
    await page.setViewportSize({width:390,height:900});await page.screenshot({path:path.join(evidence,'control-fixture-390.png')});
    await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:path.join(evidence,'control-fixture-1440.png')});
    await page.evaluate(()=>cicadaQualification.close());
    console.log('PASS: actual TinyGo kernel AudioWorklet, Go controller note paths, stop/repeat/repress, rapid Stop/Play reset barrier, simulated MIDI disconnect, blur/hidden/suspend panic, export policy, API refusal. Integration primitives only; no GoSX Studio or physical-device qualification.');
  }finally{if(browser)await browser.close();server.kill('SIGTERM');}
})().catch(error=>{console.error(error);process.exitCode=1;});
