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
      this.status={state:'idle',storage:'unchecked',timing:'unavailable',calibration:'uncalibrated',monitoring:false,settings:null,error:''};
    }
    notify() { for (const f of this.listeners) f({...this.status}); }
    onStatus(f) { this.listeners.add(f); f({...this.status}); return ()=>this.listeners.delete(f); }
    wait(type) {
      return new Promise((resolve,reject)=>{
        const timer=this.env.setTimeout(()=>{this.waiters.delete(type);reject(new Error('Capture worker timed out'));},15000);
        this.waiters.set(type,{resolve,reject,timer});
      });
    }
    receive(data) {
      if (data.t==='fault') {
        this.status.error=data.error; this.status.incomplete=true;
        for (const [type,w] of this.waiters) {
          if (type === 'finished' && !data.fatal) continue;
          this.env.clearTimeout(w.timer); w.reject(new Error(data.error)); this.waiters.delete(type);
        }
        if (data.fatal) {
          this.releaseInput(); this.status.state='stopped'; this.worker?.terminate(); this.worker=null;
        } else if (['armed','recording'].includes(this.status.state)) this.stop().catch(()=>{});
      } else {
        const w=this.waiters.get(data.t);
        if(w) { this.waiters.delete(data.t); this.env.clearTimeout(w.timer); w.resolve(data); }
        if(data.t==='finished') { this.status.error=data.error || this.status.error; this.status.incomplete=data.incomplete || this.status.incomplete; this.take={...this.take,...data}; this.status.state='stopped'; this.status.incomplete=data.incomplete || this.status.incomplete; this.releaseInput(); }
      }
      this.notify();
    }
    async arm(channels = 1) {
      if(this.busy || this.status.state==='armed' || this.status.state==='recording') throw new Error('Capture is already armed');
      if (![1,2].includes(channels)) throw new Error('Choose mono or stereo input');
      this.busy=true; this.status.error=''; this.status.incomplete=false;
      try {
        if (!this.env.navigator?.mediaDevices?.getUserMedia) throw new Error('Microphone capture unavailable; use a secure context');
        await this.audio.startAudio(true);
        if (this.audio.playing) throw new Error('Stop the transport before arming');
        this.stream=await this.env.navigator.mediaDevices.getUserMedia({audio:{channelCount:{ideal:channels},echoCancellation:false,noiseSuppression:false,autoGainControl:false},video:false});
        const track=this.stream.getAudioTracks()[0];
        if (!track) throw new Error('Microphone returned no audio track');
        this.status.settings=inspectSettings(track);
        if (this.status.settings.channelCount && this.status.settings.channelCount !== channels) throw new Error('Input channel count differs from the requested layout');
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
        const result=await opened;
        this.take.mode=this.status.storage=result.mode;
        // Persist a small recovery index only after the store is usable.
        try { this.env.localStorage?.setItem('cicada-last-take',JSON.stringify(this.take)); } catch (_) {}
        this.audio.node.port.postMessage({t:'capture-init',port:channel.port2,channels,epoch:Date.now(),inputLatencyNano:Number.isFinite(latency)?Math.round(latency*1e9):0,inputLatencyValid:false,outputLatencyNano:Math.round(this.audio.contextLatencyMs()*1e6),outputLatencyValid:false},[channel.port2]);
        this.source=this.audio.context.createMediaStreamSource(this.stream);
        this.source.connect(this.audio.node);
        track.onended=()=>{this.status.error='Microphone disconnected';this.status.incomplete=true;this.stop().catch(()=>{});};
        this.status.state='armed'; this.notify();
      } catch(error) {
        if (this.status.storage==='checking') this.status.storage='unavailable';
        this.releaseInput(); this.worker?.terminate(); this.status.state='idle'; this.status.error=error.message; this.notify(); throw error;
      } finally { this.busy=false; }
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
      this.audio.play();
      this.status.state='recording'; this.status.countInFrames=countInFrames; this.notify();
    }
    async stop() {
      if (!['armed','recording'].includes(this.status.state)) return this.take;
      const finished=this.wait('finished');
      this.status.state='saving'; this.notify();
      this.audio.node.port.postMessage({t:'capture-control',op:'stop'});
      this.audio.stop();
      try { await finished; return this.take; }
      catch (error) { this.status.state='stopped'; this.status.error=error.message; this.status.incomplete=true; this.worker?.terminate(); this.worker=null; this.notify(); throw error; }
      finally { this.releaseInput(); }
    }
    releaseInput() {
      this.source?.disconnect(); this.source=null;
      for (const track of this.stream?.getTracks() || []) { track.onended=null; track.stop(); }
      this.stream=null;
    }
    async recover(take = this.take) {
      if (['armed','recording','saving'].includes(this.status.state)) throw new Error('Stop capture before recovering a take');
      if(!take) { try { take=JSON.parse(this.env.localStorage?.getItem('cicada-last-take') || 'null'); } catch (_) {} }
      if(!take) throw new Error('No stored take to recover');
      if(!this.worker) { this.worker=new this.env.Worker('/audio/cicada-capture-worker.js'); this.worker.onmessage=e=>this.receive(e.data); this.worker.onerror=e=>this.receive({t:'fault',fatal:true,error:e.message || 'Capture worker failed'}); }
      const recovered=this.wait('recovered');
      this.worker.postMessage({t:'recover',id:take.id,mode:take.mode});
      return recovered;
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
      error.takeId=result.take;
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
