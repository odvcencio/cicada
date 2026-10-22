// Use a disposable, freshly launched macOS Studio with --audio null.
// CICADA_STUDIO_URL=<startup URL> CICADA_SHARED_CAPTURE_PROOF_DIR=<absolute dir>
// Run each kind against a fresh disposable server. Set CICADA_CAPTURE_KIND=channel
// for ordinary channels; omit it for MPE.
// node --test workstation/browser/shared-capture.test.cjs
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const channelCapture = process.env.CICADA_CAPTURE_KIND === 'channel';
test((channelCapture ? 'Ordinary channel' : 'MPE')+' capture retains independent notes and shared release tails on desktop and phone',{timeout:180_000},async t => {
  const startup = new URL(process.env.CICADA_STUDIO_URL);
  assert.equal(startup.hostname,'127.0.0.1');
  const proofRoot = process.env.CICADA_SHARED_CAPTURE_PROOF_DIR;
  const proof = proofRoot && path.join(proofRoot,channelCapture ? 'channel' : 'mpe');
  assert.ok(proof && path.isAbsolute(proof),'use an absolute disposable proof directory');
  fs.mkdirSync(proof,{recursive:true});
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const browser = await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH});
  t.after(() => browser.close());
  const context = await browser.newContext();
  const bootstrap = await context.newPage();
  await bootstrap.goto(startup.href);
  await bootstrap.close();
  const workflow = 'window.__cicadaChannelCapture='+channelCapture+';\n'+fs.readFileSync(path.join(__dirname,'../../desktop/testdata/native-shared-capture-workflow.js'),'utf8');
  const reports = [];
  for (const [name,viewport] of [['desktop',{width:1440,height:1000}],['phone',{width:390,height:900}]]) {
    const page = await context.newPage();
    await page.setViewportSize(viewport);
    const errors = [];
    page.on('pageerror',error => errors.push(error.message));
    await page.exposeFunction('__cicadaCaptureReview',async () => {
      if (name === 'phone') await page.locator('.shell-phone-nav a').filter({hasText:'Center'}).click();
      await page.locator('.take-shared-gestures summary').scrollIntoViewIfNeeded();
      await page.screenshot({path:path.join(proof,name+'-shared-review.png')});
    });
    await page.exposeFunction('__cicadaCapturePattern',async () => {
      if (name === 'phone') await page.locator('.shell-phone-nav a').filter({hasText:'Detail'}).click();
      else {
        const handle = await page.getByRole('separator',{name:'Resize Detail dock',exact:true}).boundingBox();
        await page.mouse.move(handle.x+handle.width/2,handle.y+3);
        await page.mouse.down();
        await page.mouse.move(handle.x+handle.width/2,handle.y-400,{steps:8});
        await page.mouse.up();
      }
      if (name === 'phone') await page.locator('#shared-gestures h3').evaluate(node => node.scrollIntoView({block:'start'}));
      else await page.locator('#shared-gestures').scrollIntoViewIfNeeded();
      await page.screenshot({path:path.join(proof,name+'-captured-lanes.png')});
    });
    try {
      await page.addInitScript(workflow);
      await page.goto(new URL('/studio',startup).href);
      await page.waitForFunction(() => window.__cicadaCaptureSmokeResult,null,{timeout:75_000});
      const result = await page.evaluate(() => window.__cicadaCaptureSmokeResult);
      if (!result?.ok) await page.screenshot({path:path.join(proof,name+'-failure.png')});
      assert.ok(result?.ok,JSON.stringify(result));
      assert.deepEqual(errors,[]);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),true,'no page overflow');
      reports.push({viewport,...result,errors});
    } finally { await page.close(); }
  }
  // Exercise the Web MIDI adapter with in-memory ports. This fixture changes
  // only the platform capability in the served engine props, never the app.
  const web = await context.newPage();
  await web.setViewportSize({width:1440,height:1000});
  const webErrors = [];
  web.on('pageerror',error => webErrors.push(error.message));
  const liveResponses = [];
  web.on('response',response => {
    if (new URL(response.url()).pathname !== '/__actions/live') return;
    const values = response.request().postDataJSON();
    if (values.type === 'input-batch') liveResponses.push({status:response.status(),messages:JSON.parse(values.messages)});
  });
  let patched = 0, nativeRequests = 0;
  await web.route('**/studio*',async route => {
    if (!route.request().isNavigationRequest()) return route.continue();
    const response = await route.fetch();
    const body = await response.text();
    assert.ok(body.includes('"nativeMIDI":true'),'the fixture must find the native capability before choosing Web MIDI');
    patched++;
    await route.fulfill({response,body:body.replaceAll('"nativeMIDI":true','"nativeMIDI":false')});
  });
  await web.route('**/api/midi/input',route => {nativeRequests++;return route.abort();});
  await web.addInitScript(() => {
    const access = new EventTarget();
    access.inputs = new Map();
    const add = id => {
      const port = new EventTarget();
      Object.assign(port,{id,name:'Synthetic '+id,state:'connected'});
      access.inputs.set(id,port);
      access.dispatchEvent(new Event('statechange'));
    };
    add('a'); add('b');
    window.__webMIDI = {add,
      remove(id) {access.inputs.delete(id);access.dispatchEvent(new Event('statechange'));},
      send(id,...data) {
        const event = new Event('midimessage');
        Object.defineProperty(event,'data',{value:new Uint8Array(data)});
        access.inputs.get(id).dispatchEvent(event);
      }
    };
    Object.defineProperty(navigator,'requestMIDIAccess',{configurable:true,value:() => Promise.resolve(access)});
  });
  const webDraft = () => web.evaluate(() => JSON.parse(sessionStorage.getItem('cicada.note-take') || '{}').takes?.[0]);
  const until = async (check,label) => {
    const deadline=Date.now()+15_000;
    while(Date.now()<deadline) {if(await check())return;await new Promise(resolve=>setTimeout(resolve,50));}
    assert.fail(label);
  };
  try {
    await web.goto(new URL('/studio?tab=session',startup).href);
    await web.locator('#studio-shell[data-shell-actions=ready]').waitFor();
    const dock = await web.getByRole('separator',{name:'Resize Detail dock',exact:true}).boundingBox();
    await web.mouse.move(dock.x+dock.width/2,dock.y+3);
    await web.mouse.down();
    await web.mouse.move(dock.x+dock.width/2,dock.y+363,{steps:8});
    await web.mouse.up();
    await web.locator('[data-live-midimode]').selectOption(channelCapture ? 'auto' : 'lower');
    await web.locator('[data-live-pattern]').selectOption('phrase');
    await web.locator('#transport-form button[value=play]').click();
    await web.waitForFunction(() => !document.querySelector('[data-live-control=record]').disabled);
    await web.locator('[data-live-control=midi]').click();
    await web.waitForFunction(() => /MIDI enabled.*2 inputs/.test(document.querySelector('[data-live-status]').textContent));
    await web.locator('[data-live-acid]').selectOption('plain');
    await web.evaluate(channelCapture => {
      __webMIDI.send('b',channelCapture ? 0x92 : 0x91,70,80);
      __webMIDI.send('b',channelCapture ? 0xd2 : 0xd0,13);
    },channelCapture);
    await web.waitForFunction(() => /plays notes without gesture monitoring/.test(document.querySelector('[data-live-status]').textContent));
    await web.evaluate(channelCapture => __webMIDI.send('b',channelCapture ? 0x82 : 0x81,70,0),channelCapture);
    await web.locator('[data-live-acid]').selectOption('keys');
    await web.evaluate(channelCapture => __webMIDI.send('b',channelCapture ? 0x92 : 0x91,72,70),channelCapture);
    await until(() => liveResponses.some(response => response.messages.some(message => message.type === 'note-group')),'shared monitoring binds without recording');
    await web.evaluate(channelCapture => __webMIDI.send('b',channelCapture ? 0x82 : 0x81,72,0),channelCapture);
    await until(() => liveResponses.some(response => response.messages.some(message => message.type === 'note' && message.on === 'false')),'unrecorded note releases');
    await web.evaluate(channelCapture => __webMIDI.send('b',channelCapture ? 0xd2 : 0xd0,10),channelCapture);
    await until(() => liveResponses.some(response => response.messages.some(message => message.type === 'group-expression' && Number(message.pressure) === 10/127)),'shared tail monitoring works without recording');
    await web.evaluate(channelCapture => {__webMIDI.remove('a');__webMIDI.remove('b');},channelCapture);
    await new Promise(resolve=>setTimeout(resolve,6100)); // Exceed the live lease timeout with no connected input or held gate.
    await web.evaluate(channelCapture => {__webMIDI.add('a');__webMIDI.add('b');__webMIDI.send('b',channelCapture ? 0x92 : 0x91,74,70);},channelCapture);
    await until(() => liveResponses.filter(response => response.status === 200).flatMap(response => response.messages).filter(message => message.type === 'note-group').length === 2,'input resumes after a disconnected musical rest');
    await web.evaluate(channelCapture => __webMIDI.send('b',channelCapture ? 0x82 : 0x81,74,0),channelCapture);
    await web.locator('[data-live-control=record]').click();
    await web.evaluate(channelCapture => {__webMIDI.send('a',channelCapture ? 0x92 : 0x91,60,100);__webMIDI.send('b',channelCapture ? 0x92 : 0x91,64,90);},channelCapture);
    await until(async () => (await webDraft())?.notes?.length===2,'both Web MIDI inputs record');
    await web.evaluate(channelCapture => __webMIDI.remove('a'),channelCapture);
    await until(async () => {
      const take=await webDraft();
      return !take.notes[0].endEstimated && take.notes[1].endEstimated;
    },'disconnect releases only that input and keeps the other gate active');
    await web.evaluate(channelCapture => {__webMIDI.send('b',channelCapture ? 0xd2 : 0xd0,90);__webMIDI.add('a');},channelCapture);
    await web.evaluate(channelCapture => {__webMIDI.send('a',channelCapture ? 0x92 : 0x91,67,80);__webMIDI.send('a',channelCapture ? 0x82 : 0x81,67,0);},channelCapture);
    await until(async () => {
      const take=await webDraft();
      return take.notes.length===3 && take.notes[1].endEstimated && take.groups[1].points.length===2;
    },'reconnect preserves the other input and its shared lane');
    await web.evaluate(channelCapture => __webMIDI.send('b',channelCapture ? 0x82 : 0x81,64,0),channelCapture);
    await new Promise(resolve=>setTimeout(resolve,100));
    await web.evaluate(channelCapture => __webMIDI.send('b',channelCapture ? 0xd2 : 0xd0,64),channelCapture);
    await until(async () => (await webDraft()).groups[1].points.length===3,'Web MIDI retains shared movement after release');
    const take=await webDraft();
    await until(() => liveResponses.some(response => response.status === 200 && response.messages.some(message => message.type === 'group-expression' && message.groupId === String(take.groups[1].id) && Number(message.pressure) === 64/127)),'released Web MIDI master reaches the live service');
    assert.ok(liveResponses.every(response => response.status === 200),'all shared live commands accepted');
    if (channelCapture) assert.ok(take.groups.every(group => group.kind === 'channel' && group.channel === 2));
    assert.equal(new Set(take.groups.map(group=>group.id)).size,3);
    assert.equal(take.notes[0].group,1);assert.equal(take.notes[1].group,2);assert.equal(take.notes[2].group,3);
    assert.ok(take.groups[1].points.at(-1).tick>take.notes[1].endTick);
    assert.equal(nativeRequests,0,'Web MIDI never opens a native input or permission');
    assert.deepEqual(webErrors,[]);
    await web.locator('[data-live-control=panic]').click();
    await web.locator('#transport-form button[value=stop]').click();
    reports.push({adapter:'Web MIDI',patched,disconnectIsolated:true,reconnectIsolated:true,sharedTailRetained:true,sharedTailMonitored:true,monitoringWithoutRecording:true,unsupportedSoundExplained:true,disconnectedRestResumed:true,nativeRequests,errors:webErrors});
  } finally {await web.close();}
  fs.writeFileSync(path.join(proof,'browser-report.json'),JSON.stringify(reports,null,2)+'\n');
});
