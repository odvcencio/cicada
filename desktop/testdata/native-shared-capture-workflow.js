// Run only in the disposable smoke app. MIDI bytes below replace the native
// input response; no controller, microphone, or OS permission is opened.
(() => {
  if (location.hostname !== '127.0.0.1' || window.__cicadaSharedCaptureSmoke) return;
  window.__cicadaSharedCaptureSmoke = true;
  const channelCapture = window.__cicadaChannelCapture === true;
  const mode = channelCapture ? 'auto' : 'lower', prefix = channelCapture ? 'midi' : 'mpe';
  const checkpointKey = 'cicada.shared-capture-proof.'+prefix;
  const retained = JSON.parse(sessionStorage.getItem(checkpointKey) || 'null');
  const checks = retained?.checks || [], errors = [];
  addEventListener('error', event => errors.push(event.message));
  addEventListener('unhandledrejection', event => errors.push(String(event.reason)+'\n'+String(event.reason?.stack || '')));
  const originalFetch = window.fetch;
  const liveMessages = [], liveFailures = [];
  let input, inputRequests = retained?.inputRequests || 0;
  window.fetch = function(...args) {
    const request = args[0], options = args[1] || {};
    const path = new URL(typeof request === 'string' ? request : request.url, location.href).pathname;
    if (path === '/__actions/live') {
      const body = options.body;
      const values = JSON.parse(typeof body === 'string' ? body : new TextDecoder().decode(body));
      const messages = values.type === 'input-batch' ? JSON.parse(values.messages) : [];
      return originalFetch.apply(this,args).then(response => {
        if (!response.ok) liveFailures.push(response.status);
        else liveMessages.push(...messages);
        return response;
      });
    }
    if (path !== '/api/midi/input') return originalFetch.apply(this,args);
    inputRequests++;
    const stream = new ReadableStream({start(controller) { input = controller; }});
    options.signal?.addEventListener('abort',() => { try { input.close(); } catch {} });
    return Promise.resolve(new Response(stream,{headers:{'Content-Type':'application/x-ndjson'}}));
  };
  const pause = () => new Promise(resolve => setTimeout(resolve,50));
  async function wait(check,label) {
    const deadline = performance.now()+25000;
    while (performance.now()<deadline) { if (await check()) return; await pause(); }
    throw new Error('Timed out: '+label);
  }
  async function state(path='state') {
    const response = await fetch('/api/'+path,{cache:'no-store'});
    if (!response.ok) throw new Error(path+': '+response.status);
    return response.json();
  }
  async function saved(check,label) {
    await wait(async () => {
      const current = await state();
      return current.valid && check(current.source.replace(/\r\n?/g,'\n')) && document.querySelector('#undo-form input[name=revision]')?.value === current.revision;
    },label);
    checks.push(label);
    return (await state()).source.replace(/\r\n?/g,'\n');
  }
  function change(select,value) {
    select.value = value;
    select.dispatchEvent(new Event('change',{bubbles:true}));
  }
  function navigate(panel,pattern) {
    document.querySelector(panel === 'patterns' ? '#detail-tab-clip' : '#view-tab-session').click();
    if (panel === 'patterns') {
      const link = Array.from(document.querySelectorAll('#detail-panel-clip a')).find(link => new URL(link.href).searchParams.get('pattern') === pattern);
      if (!link) throw new Error('Captured pattern is absent from the Clip list');
      link.click();
    }
    const region = panel === 'patterns' ? 'detail' : 'center';
    const phone = Array.from(document.querySelectorAll('.shell-phone-nav a')).find(link => new URL(link.href).searchParams.get('region') === region);
    if (phone && phone.getClientRects().length) phone.click();
  }
  (async () => {
    await wait(() => window.__gosx?.ready && document.querySelector('#studio-shell[data-shell-actions=ready]'),'Studio engines');
    const audio = await state('audio/config');
    if (audio.options.inputEnabled || String(audio.inventory.backendName || audio.inventory.backend).toLowerCase() !== 'null') throw new Error('Use disposable null audio with input disabled');
    const fixture = 'title "Shared capture proof"\ntempo 96\ninstrument light { voice poly { out=sine(pitch)*adsr(gate,8ms,130ms,0.6,1800ms)*velocity*(0.04+pressure*0.08) } }\ntrack keys light { level=-12dB }\ntrack plain piano { level=-24dB }\npattern intro notes { . . . . }\npattern phrase notes { . . . . } // original idea\nscene main { keys=intro }\nscene capture { keys=phrase }\nsong { main*8 }\n';
    if (!retained) {
    const editor = document.querySelector('#score textarea');
    editor.value = fixture;
    editor.dispatchEvent(new Event('input',{bubbles:true}));
    editor.focus();
    editor.dispatchEvent(new KeyboardEvent('keydown',{key:'s',code:'KeyS',metaKey:true,bubbles:true,cancelable:true}));
    await saved(source => source === fixture,'disposable expressive score saved');
    editor.blur();
    navigate('live');
    await wait(() => document.querySelector('[data-live-midimode]') && /Press Play/.test(document.querySelector('[data-live-status]').textContent),'Live engine');
    change(document.querySelector('[data-live-midimode]'),mode);
    change(document.querySelector('[data-live-pattern]'),'phrase');
    document.querySelector('#transport-form button[value=play]').click();
    await wait(() => !document.querySelector('[data-live-control=record]').disabled,'recording can start');
    const epoch = (await state('transport')).midiClockEpoch;
    document.querySelector('[data-live-control=record]').click();
    document.querySelector('[data-live-control=midi]').click();
    await wait(() => input,'synthetic native MIDI stream');
    const send = value => input.enqueue(new TextEncoder().encode(JSON.stringify(value)+'\n'));
    const position = tick => ({epoch,tick,valid:true,playing:true});
    let sequence = 0;
    const midi = (tick,...data) => send({type:'message',device:'surface',group:0,length:data.length,data:[...data,...Array(3-data.length).fill(0)],position:position(tick),hostNanos:1000000000+(++sequence)*1000000,timeSource:'source'});
    send({type:'devices',inputs:[{id:'surface',name:'Synthetic keys',epoch:1}],position:position(3840)});
    const member = channelCapture ? 2 : 1, master = channelCapture ? 2 : 0;
    midi(3840,0xd0+master,48);
    midi(3857,0x90+member,60,100);
    midi(3859,0x92,64,90);
    await wait(() => liveMessages.filter(message => message.type === 'note-group').length === 2,'owned shared bindings reach the live service');
    await new Promise(resolve => setTimeout(resolve,100));
    if (channelCapture) {
      midi(3910,0xa2,60,32);
      midi(3930,0xa2,60,64);
    } else {
      midi(3910,0xd1,32);
      midi(3930,0xe1,0,65);
    }
    midi(3977,0xe0+master,127,127);
    midi(4010,0xb0+master,74,127);
    midi(4030,0xb0+master,64,127);
    midi(4100,0x80+member,60,0);
    if (channelCapture) midi(4150,0xa2,60,127);
    else midi(4150,0xe1,0,0);
    midi(4200,0x82,64,0);
    midi(4500,0xb0+master,64,0);
    await wait(() => liveMessages.filter(message => message.type === 'note' && message.on === 'false').length === 2,'both monitored gates release');
    await new Promise(resolve => setTimeout(resolve,100));
    midi(4600,0xd0+master,96);
    await wait(() => liveMessages.some(message => message.type === 'group-expression' && Number(message.pressure) === 96/127),'shared monitoring remains addressable after release');
    checks.push('shared monitoring reaches the live service after both note gates release');
    send({type:'devices',inputs:[],removed:['surface'],position:position(4700)});
    send({type:'devices',inputs:[{id:'surface',name:'Synthetic keys',epoch:2}],position:position(4800)});
    midi(4800,0xd0+master,16);
    midi(4817,0x90+member,67,80);
    midi(4900,0x80+member,67,0);
    midi(5000,0xe0+master,0,0);
    midi(8000,0xe0+master,0,64);
    midi(8000,0xd0+master,0);
    await wait(() => {
      const draft = JSON.parse(sessionStorage.getItem('cicada.note-take') || '{}');
      return draft.takes?.[0]?.groups?.[1]?.points?.at(-1)?.tick === 8000;
    },'shared tail checkpoint');
    const local = JSON.parse(sessionStorage.getItem('cicada.note-take'));
    let checkpoint;
    await wait(async () => {
      const response = await fetch('/api/note-recovery/'+local.id,{cache:'no-store'});
      if (response.status === 404) return false;
      if (!response.ok) throw new Error('Durable checkpoint: '+response.status);
      checkpoint = await response.json();
      return checkpoint.takes?.[0]?.groups?.[1]?.points?.at(-1)?.tick === 8000;
    },'durable shared-tail checkpoint');
    const take = checkpoint.takes[0];
    if (liveFailures.length || liveMessages.filter(message => message.type === 'note-group').some(message => !message.groupId)) throw new Error('Live shared command failed: '+JSON.stringify({liveFailures,liveMessages}));
    if (checkpoint.version !== 4 || !/^[a-f0-9]{64}$/.test(checkpoint.sourceRevision) || checkpoint.sourceRevision !== document.querySelector('#undo-form input[name=source_revision]')?.value) throw new Error('Shared checkpoint lost its source-aware version or project receipt');
    if (channelCapture && take.groups.some(group => group.kind !== 'channel' || group.channel !== 2)) throw new Error('Ordinary channel metadata was lost');
    if (take.pattern !== 'phrase' || take.notes.length !== 3 || take.groups.length !== 2 || take.notes[0].group !== 1 || take.notes[1].group !== 1 || take.notes[2].group !== 2 || take.groups[0].id === take.groups[1].id) throw new Error('Input reconnect mixed shared lanes or destinations: '+JSON.stringify(take));
    if (take.notes[0].endTick !== 4500 || take.notes[1].endTick !== 4500 || take.notes[0].expressions[0].pressure !== 32/127 || take.notes[0].expressions.length !== 2 || take.groups[0].points.at(-1).tick !== 4600 || take.groups[1].points.at(-1).tick !== 8000) throw new Error('Shared capture lost local pressure, frozen sustain state, gates, or released master controls: '+JSON.stringify(take));
    checks.push('native MIDI keeps independent note controls, pedal releases and shared tail movement');
    checks.push('reconnecting the same input creates a separate shared lane');
    checks.push('private checkpoint retains raw same-tick master samples before committing');
    sessionStorage.setItem(checkpointKey,JSON.stringify({checks,inputRequests}));
    document.querySelector('[data-live-control=finish]').click();
    }
    await wait(() => document.querySelector('.note-preview'),'retained take review');
    if (!document.querySelector('.note-preview button[value=played]:not([disabled])')) throw new Error('Played timing refused: '+document.querySelector('.note-preview').textContent);
    if (!document.querySelector('.take-shared-gestures') || !(document.querySelector('.note-preview button[value=commit]')?.disabled)) throw new Error('Shared review missing or lossy grid commit offered');
    if (document.querySelector('[data-live-midimode]').value !== mode || document.querySelector('[data-live-pattern]').value !== 'phrase') throw new Error('Input preferences or recording destination changed during review');
    checks.push('local input preferences survive the review refresh without becoming unsaved score edits');
    document.querySelector('.take-shared-gestures summary').click();
    checks.push('retained review shows shared lanes and refuses a lossy step-grid commit');
    if (typeof window.__cicadaCaptureReview === 'function') await window.__cicadaCaptureReview();
    document.querySelector('#transport-form button[value=stop]').click();
    await wait(async () => !(await state('transport')).playing,'paused before commit');
    document.querySelector('.note-preview button[value=played]').click();
    const captured = await saved(source => source.includes('gesture-group '+prefix+'-1') && source.includes('gesture-group '+prefix+'-2'),'played timing writes both shared lanes');
    if (!captured.includes('length=7680ticks') || !captured.includes('note 60 at=17ticks length=643ticks') || !captured.includes('gesture at=4160ticks')) throw new Error('Committed capture changed original timing or tail extent');
    document.querySelector('#undo-form button').click();
    await saved(source => source === fixture,'one Undo restores the original idea exactly');
    document.querySelector('#redo-form button').click();
    await saved(source => source === captured,'one Redo restores notes and shared controls exactly');
    navigate('patterns','phrase');
    await wait(() => document.querySelector('#shared-gestures') && document.querySelector('[data-sounds-ready=true]'),'editable captured lanes');
    if (!document.querySelector('#shared-gestures').textContent.includes(prefix+'-2')) throw new Error('Captured shared lanes are not editable');
    if (typeof window.__cicadaCapturePattern === 'function') await window.__cicadaCapturePattern();
    checks.push('captured lanes reopen in the shared gesture editor');
    try { input.close(); } catch {}
    if (inputRequests !== 1 || errors.length) throw new Error('Unexpected input request or browser errors: '+JSON.stringify({inputRequests,errors}));
    sessionStorage.removeItem(checkpointKey);
    return {type:'cicada-native-smoke',ok:true,checks};
  })().catch(error => ({type:'cicada-native-smoke',ok:false,checks,error:String(error)+'\n'+(error.stack || '')+'\nLIVE '+document.querySelector('[data-live-status]')?.textContent+'\nREVIEW '+document.querySelector('.note-preview')?.textContent+'\nERRORS '+JSON.stringify(errors)}))
    .then(result => {
      window.__cicadaCaptureSmokeResult = result;
      if (window.chrome?.webview) window.chrome.webview.postMessage(JSON.stringify(result));
    });
})();
