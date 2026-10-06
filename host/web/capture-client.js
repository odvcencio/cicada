(function(root) {
  'use strict';
  function inspectSettings(track) {
    const settings = track.getSettings ? track.getSettings() : {};
    const processing = {};
    for (const name of ['echoCancellation','noiseSuppression','autoGainControl']) processing[name] = typeof settings[name] === 'boolean' ? settings[name] : 'unreported';
    return {...settings,...processing};
  }
  class BrowserCapture {
    constructor(audio, env = root) {
      this.audio=audio; this.env=env; this.stream=null; this.source=null; this.worker=null; this.take=null;
      this.listeners=new Set(); this.waiters=new Map(); this.busy=false;
      this.inputDevice = ''; this.deviceGeneration = 0; this.armGeneration=0; this.capturePlayed=false;
      audio.onState?.(playing => {
        if (this.status?.state !== 'recording') return;
        if (playing) this.capturePlayed=true;
        else if (this.capturePlayed) this.interrupt('Playback interrupted during recording');
      });
      const interrupt = message => { if (['arming','armed','recording'].includes(this.status.state)) this.interrupt(message); };
      env.addEventListener?.('pagehide', () => interrupt('Page closed or navigated away'));
      env.document?.addEventListener?.('visibilitychange', () => { if (env.document.hidden) interrupt('Recording interrupted while page was hidden'); });
      env.navigator?.mediaDevices?.addEventListener?.('devicechange', async () => {
        const generation = ++this.deviceGeneration;
        if (!this.inputDevice || !['armed','recording'].includes(this.status.state)) return;
        try {
          const devices = await this.listInputs();
          if (generation === this.deviceGeneration && !devices.some(d => d.deviceId === this.inputDevice)) interrupt('Selected audio input disconnected');
        } catch (_) { /* An ended track is still authoritative on browsers that hide the inventory. */ }
      });
      this.status={state:'idle',storage:'unchecked',timing:'unavailable',calibration:'uncalibrated',monitoring:false,settings:null,error:''};
    }
    notify() { for (const f of this.listeners) f({...this.status}); }
    onStatus(f) { this.listeners.add(f); f({...this.status}); return ()=>this.listeners.delete(f); }
    wait(type, expectedID = '') {
      return new Promise((resolve,reject)=>{
        const timer=this.env.setTimeout(()=>{this.waiters.delete(type);reject(new Error('Capture worker timed out'));},15000);
        this.waiters.set(type,{resolve,reject,timer,expectedID});
      });
    }
    receive(data) {
      const waiting=this.waiters.get(data.t);
      if (waiting?.expectedID && data.id !== waiting.expectedID) return;
      if (data.id && ['opened','finished'].includes(data.t) && this.take?.id !== data.id) return;
      if (data.t==='fault') {
        this.status.error=data.error; this.status.incomplete=true;
        for (const [type,w] of this.waiters) {
          if (type === 'finished' && !data.fatal) continue;
          this.env.clearTimeout(w.timer); w.reject(new Error(data.error)); this.waiters.delete(type);
        }
        if (data.fatal) {
          // The failed worker cannot finish a stop handshake. Stop both owners
          // immediately, including Play that has not been acknowledged yet.
          this.audio.node?.port.postMessage({t:'capture-control',op:'stop'});
          if (this.audio.node) this.audio.stop(true);
          this.saveIndex(); this.releaseInput(); this.status.state='stopped'; this.worker?.terminate(); this.worker=null;
        } else if (['armed','recording'].includes(this.status.state)) this.stop().catch(()=>{});
      } else {
        const w=this.waiters.get(data.t);
        if(w) { this.waiters.delete(data.t); this.env.clearTimeout(w.timer); w.resolve(data); }
        if(data.t==='finished') { this.status.error=data.error || this.status.error; this.status.incomplete=data.incomplete || this.status.incomplete; this.take={...this.take,...data}; this.status.state='stopped'; this.status.incomplete=data.incomplete || this.status.incomplete; this.saveIndex(); this.releaseInput(); }
      }
      this.notify();
    }
    async listInputs() {
      if (!this.env.navigator?.mediaDevices?.enumerateDevices) throw new Error('Audio input enumeration unavailable');
      return (await this.env.navigator.mediaDevices.enumerateDevices()).filter(device => device.kind === 'audioinput');
    }
    saveIndex() {
      if (this.take) {
        this.take.incomplete=!!this.status.incomplete;
        try { this.env.localStorage?.setItem('cicada-last-take',JSON.stringify(this.take)); } catch (_) {}
      }
    }
    interrupt(message) {
      if (this.status.state === 'arming') {
        this.armGeneration++; this.status.error=message; this.status.state='idle';
        this.releaseInput(); this.worker?.terminate(); this.worker=null;
        for (const waiter of this.waiters.values()) { this.env.clearTimeout(waiter.timer); waiter.reject(new Error(message)); }
        this.waiters.clear(); this.notify(); return Promise.resolve();
      }
      this.status.error = message; this.status.incomplete = true; this.saveIndex();
      return this.stop().catch(() => {});
    }
    async arm(channels = 1, options = {}) {
      if (typeof options === 'string') options = { deviceId: options };
      const deviceId = options.deviceId || '';
      if(this.busy || this.status.state==='armed' || this.status.state==='recording' || this.status.state==='saving') throw new Error('Capture is already armed');
      if (![1,2].includes(channels)) throw new Error('Choose mono or stereo input');
      let previousTake=this.take;
      if (!previousTake) try { previousTake=JSON.parse(this.env.localStorage?.getItem('cicada-last-take') || 'null'); } catch (_) {}
      this.busy=true; this.status.error=''; this.status.incomplete=false;
      const generation=++this.armGeneration;
      const current=()=>{if(generation!==this.armGeneration || this.env.document?.hidden) throw new Error('Microphone arming was interrupted');};
      this.status.state='arming'; this.notify();
      try {
        if (!this.env.navigator?.mediaDevices?.getUserMedia) throw new Error('Microphone capture unavailable; use a secure context');
        await this.audio.startAudio(true); current();
        if (this.audio.playing) throw new Error('Stop the transport before arming');
        if (typeof deviceId !== 'string') throw new Error('Input device ID must be a string');
        this.inputDevice = deviceId;
        this.stream=await this.env.navigator.mediaDevices.getUserMedia({audio:{...(deviceId ? {deviceId:{exact:deviceId}} : {}),channelCount:{ideal:channels},echoCancellation:false,noiseSuppression:false,autoGainControl:false},video:false}); current();
        const track=this.stream.getAudioTracks()[0];
        if (!track) throw new Error('Microphone returned no audio track');
        this.status.settings=inspectSettings(track);
        this.inputDevice=this.status.settings.deviceId || deviceId;
        // A device may deliver another layout than requested, such as a
        // two-microphone webcam for a mono take. The explicit-mode node below
        // mixes it to the requested layout; the take records the source count.
        const delivered=this.status.settings.channelCount;
        this.status.settings.mixedFrom=delivered && delivered !== channels ? delivered : 0;
        const latency=this.status.settings.latency;
        // Track latency alone is not a duplex first-frame measurement. Keep
        // timing confidence unavailable until a qualified mapping is provided.
        this.status.timing='unavailable'; this.status.calibration='uncalibrated';
        this.worker?.terminate();
        this.worker=new this.env.Worker('/audio/cicada-capture-worker.js');
        this.worker.onmessage=e=>this.receive(e.data);
        this.worker.onerror=e=>this.receive({t:'fault',fatal:true,error:e.message || 'Capture worker failed'});
        this.worker.onmessageerror=()=>this.receive({t:'fault',fatal:true,error:'Capture worker message could not be decoded'});
        const channel=new this.env.MessageChannel();
        const id='take-'+this.env.crypto.randomUUID();
        this.take={id,channels,sampleRate:this.audio.context.sampleRate};
        const metadata={...this.take,settings:this.status.settings,created:new Date().toISOString(),format:'float32-le-interleaved',descriptor:'host/capture.Block',timingConfidence:0,calibrated:false};
        this.status.storage='checking';
        const opened=this.wait('opened');
        this.worker.postMessage({t:'open',id,metadata,port:channel.port1},[channel.port1]);
        const result=await opened; current();
        this.take.mode=this.status.storage=result.mode;
        // Match the admitted layout explicitly; the output remains stereo.
        // A max-mode stereo node otherwise upmixes a mono microphone to two
        // input channels and the capture adapter correctly rejects its blocks.
        this.audio.node.channelCount=channels;
        this.audio.node.channelCountMode='explicit';
        // Persist a small recovery index only after the store is usable.
        try { this.env.localStorage?.setItem('cicada-last-take',JSON.stringify(this.take)); } catch (_) {}
        this.audio.node.port.postMessage({t:'capture-init',port:channel.port2,channels,packets:options.instrument?128:32,epoch:Date.now(),inputLatencyNano:Number.isFinite(latency)?Math.round(latency*1e9):0,inputLatencyValid:false,outputLatencyNano:Math.round(this.audio.contextLatencyMs()*1e6),outputLatencyValid:false},[channel.port2]);
        this.source=this.audio.context.createMediaStreamSource(this.stream);
        this.source.connect(this.audio.node);
        track.onended=()=>this.interrupt('Microphone disconnected');
        track.onmute=()=>this.interrupt('Microphone input muted or interrupted');
        this.contextStateChanged = () => { if (this.audio.context.state !== 'running' && ['armed','recording'].includes(this.status.state)) this.interrupt('Audio context interrupted'); };
        this.audio.context.addEventListener?.('statechange', this.contextStateChanged);
        this.status.state='armed'; this.notify();
      } catch(error) {
        if (this.status.storage==='checking') this.status.storage='unavailable';
        this.releaseInput(); this.worker?.terminate(); this.worker=null; this.take=previousTake; this.status.state='idle'; this.status.incomplete=!!this.take?.incomplete; this.saveIndex(); this.status.error=error.message; this.notify(); throw error;
      } finally { this.busy=false; }
    }
    // Instrument recording needs no score, count-in or accompaniment.
    async recordInstrument() {
      if (this.status.state !== 'armed') throw new Error('Arm the microphone first');
      if (this.audio.playing) throw new Error('Stop the transport before recording');
      this.audio.node.port.postMessage({t:'capture-control',op:'begin',countInFrames:0,packetFrames:2048});
      this.status.state='recording'; this.status.countInFrames=0; this.notify();
    }
    async record() {
      if (this.status.state !== 'armed') throw new Error('Arm the microphone first');
      if (this.audio.playing) throw new Error('Stop the transport before recording');
      await this.audio.stageCurrentScore('',true);
      if (this.status.state !== 'armed' || this.audio.playing) throw new Error('Capture state changed while preparing the score');
      const bpm=this.audio.bpmMilli;
      if (!bpm) throw new Error('Score tempo unavailable');
      const countInFrames=Math.ceil(3840*60000*this.audio.context.sampleRate/(bpm*960));
      this.audio.node.port.postMessage({t:'capture-control',op:'begin',countInFrames});
      this.capturePlayed=false;
      this.audio.play();
      this.capturePlayed=!!this.audio.playing;
      this.status.state='recording'; this.status.countInFrames=countInFrames; this.notify();
    }
    async stop() {
      if (this.status.state === 'arming') { await this.interrupt('Microphone arming canceled'); return this.take; }
      if (!['armed','recording'].includes(this.status.state)) return this.take;
      const finished=this.wait('finished');
      this.status.state='saving'; this.notify();
      this.audio.node.port.postMessage({t:'capture-control',op:'stop'});
      this.audio.stop(true);
      // Stop admission immediately: a hidden/suspended worklet may never send
      // end/finished. Journal finalization must not keep the microphone live.
      this.releaseInput();
      try { await finished; return this.take; }
      catch (error) { this.status.state='stopped'; this.status.error=error.message; this.status.incomplete=true; this.saveIndex(); this.worker?.terminate(); this.worker=null; this.notify(); throw error; }
      finally { this.releaseInput(); }
    }
    releaseInput() {
      this.deviceGeneration++;
      this.audio.context?.removeEventListener?.('statechange', this.contextStateChanged);
      this.contextStateChanged = null;
      this.source?.disconnect(); this.source=null;
      for (const track of this.stream?.getTracks() || []) { track.onended=null; track.onmute=null; track.stop(); }
      this.stream=null;
    }
    async recover(take = this.take) {
      if (this.busy || ['arming','armed','recording','saving','recovering'].includes(this.status.state)) throw new Error('Stop capture before recovering a take');
      if(!take) { try { take=JSON.parse(this.env.localStorage?.getItem('cicada-last-take') || 'null'); } catch (_) {} }
      if(!take) throw new Error('No stored take to recover');
      this.busy=true; this.status.state='recovering'; this.notify();
      try {
        if(!this.worker) { this.worker=new this.env.Worker('/audio/cicada-capture-worker.js'); this.worker.onmessage=e=>this.receive(e.data); this.worker.onerror=e=>this.receive({t:'fault',fatal:true,error:e.message || 'Capture worker failed'}); }
        const recovered=this.wait('recovered',take.id);
        this.worker.postMessage({t:'recover',id:take.id,mode:take.mode});
        const result = await recovered;
        result.incomplete = !!(result.incomplete || take.incomplete || this.take?.id===take.id && this.status.incomplete);
        this.take = {...take,incomplete:result.incomplete};
        this.status.incomplete=result.incomplete; this.saveIndex();
        return result;
      } finally { this.busy=false; this.status.state='stopped'; this.notify(); }
    }
  }
  // The original browser-local audition remains available for stored takes.
  class TakeSampler {
    constructor(context) { this.context=context; this.voice=null; this.buffer=null; this.incomplete=false; }
    load(take) {
      this.stop();
      const channels=take.metadata.channels, rate=take.metadata.sampleRate;
      if (!take.blocks.length) throw new Error('Take has no committed PCM');
      const first=take.blocks[0];
      // Retain raw PCM in storage. Audition starts after negative count-in
      // placement and inserts silence for known gaps, rather than closing them.
      const start=Math.max(0,first.rawFrame-first.placement.engineFrame);
      const length=take.rawFrames-start;
      if(length<=0 || length>rate*60*30) throw new Error('Sampler audition admits at most 30 minutes');
      this.buffer=this.context.createBuffer(channels,length,rate);
      const pcm=new Float32Array(take.pcm);
      for (const block of take.blocks) {
        const from=Math.max(0,start-block.rawFrame);
        for (let c=0;c<channels;c++) {
          const dst=this.buffer.getChannelData(c);
          for (let f=from;f<block.timing.Frames;f++) dst[block.rawFrame+f-start]=pcm[block.offset/4+f*channels+c];
        }
      }
      this.incomplete=take.incomplete;
    }
    play(note=60,rootNote=60,loop=false) {
      if (!this.buffer) throw new Error('Recover a take before auditioning');
      if(![note,rootNote].every(n=>Number.isInteger(n)&&n>=0&&n<=127)) throw new Error('MIDI notes must be 0 to 127');
      this.stop(); const voice=this.context.createBufferSource();
      voice.buffer=this.buffer; voice.loop=loop; voice.playbackRate.value=2**((note-rootNote)/12);
      voice.connect(this.context.destination); this.voice=voice;
      voice.onended=()=>{voice.disconnect();if(this.voice===voice)this.voice=null;}; voice.start();
    }
    stop() { if(this.voice) { this.voice.onended=null;this.voice.stop();this.voice.disconnect();this.voice=null; } }
  }
  async function publishTake(take, target, env = root) {
    const bytes=new Uint8Array(take.pcm);
    if(bytes.length>64*1024*1024) throw new Error('Take exceeds the project import limit; browser PCM is retained');
    let encoded='';
    for(let at=0;at<bytes.length;at+=16384) encoded+=String.fromCharCode(...bytes.subarray(at,at+16384));
    const capture={sampleRate:take.metadata.sampleRate,channels:take.metadata.channels,rawFrames:take.rawFrames,incomplete:take.incomplete,
      blocks:take.blocks.map(({timing,rawFrame,offset,length})=>({timing,rawFrame,offset,length})),pcm:env.btoa(encoded)};
    const response=await env.fetch('/api/takes',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({...target,action:'import',capture})});
    const result=await response.json();
    if(!response.ok) {
      const error=new Error(result.error || 'Cannot publish take; browser PCM is retained');
      // A revision conflict happens after immutable publication. Other failures
      // may leave only a partial native journal; retry from retained browser PCM.
      if(response.status===409) error.takeId=result.take;
      throw error;
    }
    return result;
  }

  // One voice, with pitch conversion rendered by the shared Go sample DSP.
  class PublishedSampler {
    constructor(context,takeId,revision,env=root) { this.context=context;this.takeId=takeId;this.revision=revision;this.env=env;this.voice=null;this.generation=0; }
    async play(note=60,rootNote=60,loop=false) {
      this.stop();
      const generation=this.generation;
      const response=await this.env.fetch('/api/takes',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action:'audition',revision:this.revision,takeId:this.takeId,sample:{root:rootNote,note,loop}})});
      if(!response.ok) throw new Error((await response.json()).error || 'Cannot prepare sampler audition');
      const buffer=await this.context.decodeAudioData(await response.arrayBuffer());
      if(generation!==this.generation) return;
      const voice=this.context.createBufferSource();
      voice.buffer=buffer;voice.loop=loop;voice.playbackRate.value=1;
      voice.connect(this.context.destination);this.voice=voice;
      voice.onended=()=>{voice.disconnect();if(this.voice===voice)this.voice=null;};voice.start();
    }
    stop() { this.generation++;if(this.voice) { this.voice.onended=null;this.voice.stop();this.voice.disconnect();this.voice=null; } }
  }
  const api={inspectSettings,BrowserCapture,TakeSampler,publishTake,PublishedSampler};
  if(typeof module!=='undefined') module.exports=api;
  root.CicadaBrowserCapture=api;
})(globalThis);
