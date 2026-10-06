(() => {
  const CapabilityChords = 1, CapabilityUnifiedImage = 65536;
  // Match the worklet allowlist, including when an older cached worklet runs.
  const imageInfo = (image, capabilities) => {
    const bytes = new Uint8Array(image);
    if (bytes.length < 32 || bytes[0] !== 67 || bytes[1] !== 73 || bytes[2] !== 67 || bytes[3] !== 49) throw new Error('Invalid CIC1 image header');
    const version = bytes[4] | bytes[5] << 8;
    if (version === 14) throw new Error('Ambiguous development image14; recompile source for unified image15');
    if (![8, 9, 10, 11, 12, 13, 15].includes(version)) throw new Error(`Unsupported image version ${version}`);
    if (version === 15 && capabilities !== undefined && !(capabilities & CapabilityUnifiedImage)) throw new Error('Unified image15 requires CapabilityUnifiedImage (bit16); update the audio kernel');
    return new DataView(image).getUint32(12, true);
  };
  const audioFault = code => new Error(code === 20 ? "Polyphonic tracks require handle-aware live commands; legacy NoteOn/NoteOff are unsupported" : `Audio fault ${code}`);
  class CicadaBrowserAudio {
    constructor() {
      this.backend = 'browser';
      this.context = null;
      this.node = null;
      this.modulePromise = null;
      this.readyPromise = null;
      this.revision = '';
      this.lastStageDurationMs = 0;
      this.stageWaiters = new Map();
      this.metricWaiters = [];
      this.messages = new Set();
      this.errors = new Set();
      this.states = new Set();
      this.beforePlay = new Set();
      this.meters = new Set();
      this.pendingMeters = new Map();
      this.meterFrame = 0;
      this.playing = false;
      this.clock = false;
      this.capabilities = 0;
      this.outputTimeline = { available: false, samples: 0, misses: 0, maxLagMs: 0, maxExcessMs: 0, lastMiss: null };
      this.outputTimelineTimer = 0;
      this.callbackDurations = new Uint32Array(256);
      this.callbackSamples = 0;
      this.callbackDurationExceedances = 0;
      this.callbackGapExceedances = 0;
      this.maxCallbackDurationMs = 0;
    }

    async startAudio(capture = false) {
      if (this.context) {
        await this.context.resume();
        return { sampleRate: this.context.sampleRate, clock: this.clock };
      }
      const Context = window.AudioContext || window.webkitAudioContext;
      if (!Context) throw new Error('AudioWorklet is not available in this browser');
      this.context = new Context({ sampleRate: 48000, latencyHint: 'interactive', renderSizeHint: 'hardware' });
      try {
        const [module, imageResponse] = await Promise.all([
          this.modulePromise || (this.modulePromise = fetch('/api/kernel.wasm', { cache: 'no-store' }).then(r => {
            if (!r.ok) throw new Error('Cannot load the audio kernel');
            return r.arrayBuffer();
          }).then(bytes => WebAssembly.compile(bytes))),
          fetch(`/api/kernel-image?rate=${this.context.sampleRate}${capture?'&capture=1':''}`, { cache: 'no-store' })
        ]);
        if (!imageResponse.ok) throw new Error((await imageResponse.text()) || 'Cannot load the score image');
        const revision = imageResponse.headers.get('X-Cicada-Revision') || '';
        const image = await imageResponse.arrayBuffer();
        const bpmMilli = imageInfo(image);
        await this.context.audioWorklet.addModule('/audio/cicada-capture.js');
        await this.context.audioWorklet.addModule('/audio/cicada-capture-processor.js');
        this.node = new AudioWorkletNode(this.context, 'cicada', {
          numberOfInputs: 1, numberOfOutputs: 1, channelCount: 2, channelCountMode: 'max', channelInterpretation: 'discrete', outputChannelCount: [2],
          processorOptions: { m: module, i: image, r: revision, l: this.contextLatencyMs() }
        });
        this.readyPromise = new Promise((resolve, reject) => {
          const timeout = setTimeout(() => reject(new Error('AudioWorklet did not initialize')), 10000);
          this.node.port.onmessage = event => {
            const data = event.data;
            if (data.t === 'r') {
              clearTimeout(timeout);
              this.revision = data.r;
              this.clock = data.c;
              this.capabilities = data.p || 0;
              try {
                imageInfo(image, this.capabilities);
                this.bpmMilli = bpmMilli;
                resolve(data);
              } catch (error) { reject(error); }
            } else if (data.t === 'e') {
              clearTimeout(timeout);
              reject(new Error(data.e));
            }
            this.receive(data);
          };
          this.node.port.onmessageerror = () => { clearTimeout(timeout); reject(new Error('AudioWorklet message could not be decoded')); };
        });
        this.node.connect(this.context.destination);
        await this.readyPromise;
        await this.context.resume();
        this.node.port.postMessage({ t: 'q', l: this.contextLatencyMs() });
        if (!this.outputTimelineTimer) this.outputTimelineTimer = setInterval(() => this.sampleOutputTimeline(), 5);
      } catch (error) {
        // Leave no half-started context behind, so a later Start retries from scratch.
        const failed = this.context;
        this.context = null;
        this.node = null;
        this.readyPromise = null;
        this.modulePromise = null;
        this.capabilities = 0;
        if (failed && failed.close) failed.close().catch(() => {});
        throw error;
      }
      return { sampleRate: this.context.sampleRate, clock: this.clock };
    }

    receive(data) {
      if (data.t === 'm') {
        try {
          const view = new DataView(data.bytes, 0, data.n);
          if (data.n % 16) throw new Error('AudioWorklet returned a partial message');
          for (let offset = 0; offset < data.n; offset += 16) {
            const message = {
              kind: view.getUint8(offset), track: view.getUint8(offset + 1),
              a: view.getUint16(offset + 2, true), b: view.getUint32(offset + 4, true),
              tick: Number(view.getBigInt64(offset + 8, true))
            };
            for (const callback of this.messages) callback(message);
            if (message.kind === 2) {
              this.pendingMeters.set(`${message.track}:${message.a}`, message);
              if (!this.meterFrame) this.meterFrame = requestAnimationFrame(() => {
                this.meterFrame = 0;
                const meters = [...this.pendingMeters.values()];
                this.pendingMeters.clear();
                for (const callback of this.meters) callback(meters);
              });
            }
            if (message.kind === 7) {
              this.playing = false;
              this.notifyState();
              this.raise(audioFault(message.a));
            }
          }
        } catch (error) {
          this.raise(error);
        } finally {
          this.node.port.postMessage({ t: 'b', bytes: data.bytes }, [data.bytes]);
        }
      } else if (data.t === 's') {
        this.playing = data.p;
        this.notifyState();
      } else if (data.t === 'f') {
        this.playing = false;
        this.notifyState();
        const message = { kind: 7, track: 255, a: data.a, b: 0, tick: 0 };
        for (const callback of this.messages) callback(message);
        this.raise(audioFault(data.a));
      } else if (data.t === 'r') {
        this.clock = data.c;
        this.capabilities = data.p || 0;
      } else if (data.t === 't') {
        this.revision = data.r;
        const waiter = this.stageWaiters.get(data.r);
        if (waiter) { this.stageWaiters.delete(data.r); waiter.resolve(data); }
      } else if (data.t === 'x' || data.t === 'e') {
        const waiter = this.stageWaiters.get(data.r);
        if (waiter) { this.stageWaiters.delete(data.r); waiter.reject(new Error(data.e)); }
        else this.raise(new Error(data.e));
      } else if (data.t === 'q') {
        if (data.d && data.d.length === this.callbackDurations.length) {
          this.callbackDurations.set(data.d);
          this.callbackSamples = this.callbackDurationExceedances = this.callbackGapExceedances = this.maxCallbackDurationMs = 0;
          for (let i = 0; i < data.d.length - 2; i++) {
            this.callbackSamples += data.d[i];
            if (data.d[i]) this.maxCallbackDurationMs = (i + 1) / 4;
          }
          this.callbackDurationExceedances = data.d[data.d.length - 2];
          this.callbackGapExceedances = data.d[data.d.length - 1];
        }
        const waiter = this.metricWaiters.shift();
        if (waiter) waiter({...data, q: data.q || this.quantumMs(), l: this.contextLatencyMs(), cp: this.clock ? .1 : 1, underruns: data.u, clock: this.clock, memoryBytes: data.m,
          callbackP99Ms: this.callbackP99Ms(),
          callbackSamples: this.callbackSamples,
          callbackDurationExceedances: this.callbackDurationExceedances,
          callbackGapExceedances: this.callbackGapExceedances,
          maxCallbackDurationMs: this.maxCallbackDurationMs,
          outputTimeline: {...this.outputTimeline, latencyMs: this.contextLatencyMs(),
            quantumMs: data.q || this.quantumMs(),
            limitMs: this.contextLatencyMs() + (data.q || this.quantumMs())}});
      }
    }

    contextLatencyMs() {
      if (!this.context) return 0;
      return ((this.context.baseLatency || 0) + (this.context.outputLatency || 0)) * 1000;
    }

    quantumMs() {
      return (this.context.renderQuantumSize || 128) * 1000 / this.context.sampleRate;
    }

    callbackP99Ms() {
      const rank = Math.ceil(this.callbackSamples * .99);
      let seen = 0;
      for (let i = 0; i < this.callbackDurations.length - 2; i++) {
        seen += this.callbackDurations[i];
        if (seen >= rank) return (i + 1) / 4;
      }
      return 0;
    }

    sampleOutputTimeline() {
      const context = this.context;
      if (!context || context.state !== 'running' || !this.playing || typeof context.getOutputTimestamp !== 'function') return;
      const timestamp = context.getOutputTimestamp();
      if (!timestamp || !timestamp.contextTime || !timestamp.performanceTime) return;
      const now = performance.now();
      const sampleAgeMs = Math.max(0, now - timestamp.performanceTime);
      const timestampContextTimeNow = timestamp.contextTime + sampleAgeMs / 1000;
      const lag = Math.max(0, (context.currentTime - timestampContextTimeNow) * 1000);
      const excess = Math.max(0, lag - this.contextLatencyMs());
      const timeline = this.outputTimeline;
      timeline.available = true;
      timeline.samples++;
      if (lag > timeline.maxLagMs) timeline.maxLagMs = lag;
      if (excess > timeline.maxExcessMs) timeline.maxExcessMs = excess;
      if (excess > this.quantumMs()) {
        timeline.misses++;
        timeline.lastMiss = { now, sampleAgeMs, contextTime: context.currentTime,
          timestampContextTime: timestamp.contextTime, timestampPerformanceTime: timestamp.performanceTime,
          lagMs: lag, excessMs: excess };
      }
    }

    raise(error) { for (const callback of this.errors) callback(error); }
    notifyState() { for (const callback of this.states) callback(this.playing); }
    onMessage(callback) { this.messages.add(callback); return () => this.messages.delete(callback); }
    onError(callback) { this.errors.add(callback); return () => this.errors.delete(callback); }
    onState(callback) { this.states.add(callback); return () => this.states.delete(callback); }
    onBeforePlay(callback) { this.beforePlay.add(callback); return () => this.beforePlay.delete(callback); }
    onMeters(callback) { this.meters.add(callback); return () => this.meters.delete(callback); }

    sendCommands(records) {
      if (!this.node || !this.readyPromise) throw new Error('Select Browser mode and start audio first');
      if (records.some(record => record.op === 22) && !(this.capabilities & CapabilityChords)) throw new Error('Unsupported chord opcode22');
      const bytes = new Uint8Array(records.length * 24), view = new DataView(bytes.buffer);
      records.forEach((record, index) => {
        const at = index * 24;
        view.setUint8(at, record.op); view.setUint8(at + 1, record.track ?? 255);
        view.setUint16(at + 2, record.index || 0, true);
        view.setUint32(at + 4, record.arg0 || 0, true);
        view.setUint32(at + 8, record.arg1 || 0, true);
        view.setBigInt64(at + 16, BigInt(record.tick || 0), true);
      });
      this.node.port.postMessage({ t: 'c', bytes }, [bytes.buffer]);
    }

    play() {
      if (this.playing) return;
      for (const callback of this.beforePlay) callback();
      this.sendCommands([{ op: 3, arg0: 0 }, { op: 1 }]);
    }
    stop(force = false) { if (this.playing || force) this.sendCommands([{ op: 2 }]); }
    launchScene(index) { this.sendCommands([{ op: 10, index, arg0: 2 }]); }
    selectPattern(track, slot) { this.sendCommands([{ op: 9, track, index: slot, arg0: 2 }]); }
    playFrom(bar) { for (const callback of this.beforePlay) callback(); this.sendCommands([{ op: 3, arg0: Math.max(0, bar - 1) }, { op: 1 }]); }

    async stageCurrentScore(revision = '', capture = false) {
      if (!this.node) return false;
      if (revision && revision === this.revision) return true;
      const started = performance.now();
      const response = await fetch(`/api/kernel-image?rate=${this.context.sampleRate}${capture?'&capture=1':''}`, { cache: 'no-store' });
      if (!response.ok) throw new Error((await response.text()) || 'Cannot prepare the edited score');
      const imageRevision = response.headers.get('X-Cicada-Revision') || '';
      if (revision && imageRevision !== revision) throw new Error('Score changed while preparing the browser kernel');
      const image = await response.arrayBuffer();
      const bpmMilli = imageInfo(image, this.capabilities);
      const ready = new Promise((resolve, reject) => this.stageWaiters.set(imageRevision, { resolve, reject }));
      this.node.port.postMessage({ t: 'i', i: image, r: imageRevision }, [image]);
      await ready;
      this.bpmMilli = bpmMilli;
      this.lastStageDurationMs = performance.now() - started;
      return true;
    }

    async params() {
      const response = await fetch('/api/params', { cache: 'no-store' });
      if (!response.ok) throw new Error('Parameter registry is not available in this Browser audio build');
      return response.json();
    }
    setParam() { throw new Error('Live parameters are not enabled in Browser audio yet'); }
    setMute() { throw new Error('Mute controls are not enabled in Browser audio yet'); }
    setSolo() { throw new Error('Solo controls are not enabled in Browser audio yet'); }

    requestMetrics() {
      if (!this.node) return Promise.reject(new Error('AudioWorklet is not running'));
      return new Promise(resolve => {
        this.metricWaiters.push(resolve);
        this.node.port.postMessage({ t: 'q', l: this.contextLatencyMs() });
      });
    }
    injectStall() { if (this.node) this.node.port.postMessage({ t: 'z' }); }
  }

  window.cicadaBrowserAudio = new CicadaBrowserAudio();
})();
