(function (root) {
  'use strict';

  function filterItems(items, kind, query) {
    const text = query.trim().toLowerCase();
    return items.filter(item => (!kind || item.kind === kind) && `${item.path} ${item.name}`.toLowerCase().includes(text));
  }

  // A temporary node uses the same kernel and processor as score playback.
  // The score node and its image stay intact. Nothing enters processor.js.
  function createPreview(options = {}) {
    const audio = options.audio || root.cicadaBrowserAudio;
    const Node = options.Node || root.AudioWorkletNode;
    const later = options.setTimeout || root.setTimeout.bind(root);
    const cancel = options.clearTimeout || root.clearTimeout.bind(root);
    let node = null, timer = null, generation = 0, cancelReady = null;
    function stop() {
      generation++;
      if (cancelReady) { cancelReady(); cancelReady = null; }
      if (timer !== null) cancel(timer);
      timer = null;
      if (node) { node.disconnect(); node.port.close(); node = null; }
    }
    async function play(image) {
      stop();
      const ticket = generation;
      if (audio.playing) throw new Error('Stop playback before previewing');
      await audio.startAudio();
      if (ticket !== generation) return false;
      const preview = new Node(audio.context, 'cicada', {numberOfInputs: 0, numberOfOutputs: 1, outputChannelCount: [2], processorOptions: {m: await audio.modulePromise, i: image, r: 'preview'}});
      if (ticket !== generation) { preview.port.close(); return false; }
      node = preview;
      try { await new Promise((resolve, reject) => {
        cancelReady = resolve;
        timer = later(() => { reject(new Error('Preview audio did not initialize')); stop(); }, 10000);
        preview.port.onmessage = event => {
          const data = event.data;
          if (data.t === 'r') resolve();
          else if (data.t === 'e' || data.t === 'f') { reject(new Error(data.e || 'Preview audio failed')); stop(); }
          else if (data.t === 'm') preview.port.postMessage({t: 'b', bytes: data.bytes}, [data.bytes]);
        };
      }); } catch (error) { if (ticket === generation) stop(); throw error; }
      if (ticket !== generation) return false;
      cancelReady = null;
      cancel(timer);
      preview.connect(audio.context.destination);
      const bytes = new Uint8Array(24); bytes[0] = 1; bytes[1] = 255;
      preview.port.postMessage({t: 'c', bytes}, [bytes.buffer]);
      timer = later(stop, 2000);
      return true;
    }
    return {play, stop};
  }

  function mount(options = {}) {
    const document = options.document || root.document;
    const section = options.section || document.querySelector('[data-studio-library]');
    if (!section) return;
    const fetcher = options.fetcher || root.fetch.bind(root);
    const studio = options.studio || root.cicadaStudio;
    const browser = options.browser || root.cicadaBrowserAudio;
    const preview = options.preview || createPreview({audio: browser});
    const controls = Object.fromEntries(['kind','query','list','track','new-track','preset-name','save','stop','status'].map(name => [name,section.querySelector(`#library-${name}`)]));
    let items = [], busy = false, request = 0;
    const status = message => { controls.status.textContent = message; };
    const payload = extra => ({revision: studio.revision(), ...extra});
    const post = (url, body) => fetcher(url, {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(payload(body))});
    function stop() {
      request++;
      preview.stop();
      return post('/api/library/preview', {action:'stop'}).catch(() => {});
    }
    async function write(endpoint, extra) {
      if (busy || studio.busy()) return false;
      if (studio.dirty()) { status('Save or discard source edits first.'); return false; }
      busy = true;
      try {
        stop();
        const response = await post(endpoint, extra), result = await response.json();
        if (!response.ok) throw new Error(result.error || 'Library edit failed');
        root.dispatchEvent(new root.CustomEvent('cicada:sourcewritten', {detail:result}));
        status('Saved. Undo is available in History.');
        return true;
      } catch (error) { status(error.message); return false; }
      finally { busy = false; }
    }
    function paint() {
      const rows = filterItems(items,controls.kind.value,controls.query.value).map(item => {
        const row = document.createElement('article'); row.className = 'library-row';
        const label = document.createElement('div');
        const name = document.createElement('strong'); name.textContent = item.name;
        const detail = document.createElement('small'); detail.textContent = `${item.kind} · ${item.location} · ${item.path}`;
        label.append(name,detail);
        const audition = document.createElement('button'); audition.type='button'; audition.textContent='Preview'; audition.setAttribute('aria-label',`Preview ${item.name}`);
        audition.addEventListener('click',async () => {
          if (busy || studio.playing() || browser.playing) { status('Stop playback before previewing.'); return; }
          const stopped = stop(); const ticket = request; audition.disabled=true;
          try {
            await stopped;
            if (ticket !== request) return;
            const mode = document.querySelector('#audio-mode').value;
            if (mode==='browser') await browser.startAudio();
            const response = await post('/api/library/preview',{path:item.path,item:item.name,mode,rate:browser.context?.sampleRate || 48000});
            if (!response.ok) throw new Error((await response.json()).error || 'Preview failed');
            if (ticket !== request) return;
            if (mode==='browser') {
              const image = await response.arrayBuffer();
              if (ticket !== request) return;
              await preview.play(image);
            }
            status(`Previewing ${item.name} for one bar.`);
          } catch (error) { if (ticket === request) { preview.stop(); status(error.message); } }
          finally { audition.disabled=false; }
        });
        const insert = document.createElement('button'); insert.type='button'; insert.textContent='Insert'; insert.setAttribute('aria-label',`Insert ${item.name}`);
        insert.addEventListener('click',() => write('/api/library/insert',{path:item.path,item:item.name,track:controls.track.value || controls['new-track'].value.trim()}));
        row.append(label,audition,insert); return row;
      });
      if (!rows.length) { const empty = document.createElement('p'); empty.textContent='No matching library items.'; rows.push(empty); }
      controls.list.replaceChildren(...rows);
    }
    async function refresh() {
      try {
        const response = await fetcher('/api/library',{cache:'no-store'});
        const data = await response.json(); if (!response.ok) throw new Error(data.error || 'Library unavailable');
        items=data.items || [];
        const selected = controls.track.value;
        const choices = [''].concat(data.tracks || []).map(track => { const option=document.createElement('option'); option.value=track; option.textContent=track || 'New track'; return option; });
        controls.track.replaceChildren(...choices); controls.track.value=(data.tracks || []).includes(selected) ? selected : '';
        paint(); if (data.errors?.length) status(data.errors.join('; '));
      } catch (error) { status(error.message); }
    }
    controls.kind.addEventListener('change',paint); controls.query.addEventListener('input',paint);
    controls.stop.addEventListener('click',stop);
    controls.save.addEventListener('click',() => write('/api/library/save',{track:controls.track.value,name:controls['preset-name'].value.trim()}));
    // Capture runs before the existing transport listeners start playback.
    document.addEventListener('click',event => { if (event.target.closest?.('#transport-button, #transport-stop, #transport-home, .song-play')) stop(); },true);
    document.querySelector('#audio-mode')?.addEventListener('change',stop);
    document.addEventListener('keydown',event => { if (event.key===' ' && !/^(INPUT|TEXTAREA|SELECT|BUTTON|A|SUMMARY)$/.test(event.target?.tagName || '') && !event.target?.isContentEditable) stop(); },true);
    browser.onState(playing => { if (playing) stop(); });
    browser.onBeforePlay(stop);
    root.addEventListener('cicada:projectionrefreshed',refresh);
    root.addEventListener('pagehide',stop);
    refresh();
    return {refresh,stop,write};
  }
  const api = {filterItems,createPreview,mount};
  if (typeof module !== 'undefined' && module.exports) module.exports=api;
  root.CicadaStudioLibrary=api;
  if (root.document) {
    if (root.document.readyState==='loading') root.document.addEventListener('DOMContentLoaded',() => mount(),{once:true});
    else mount();
  }
})(typeof window === 'undefined' ? globalThis : window);
