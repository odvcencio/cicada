(() => {
  const audioFault = code => new Error(code === 20 ? "Polyphonic tracks require handle-aware live commands; legacy NoteOn/NoteOff are unsupported" : `Audio fault ${code}`);
  class CicadaBrowserAudio {
    constructor() {
      this.backend = 'browser';
      this.context = null;
      this.node = null;
      this.modulePromises = new Map();
      this.moduleKind = '';
      this.startPromise = null;
      this.stagePromise = Promise.resolve();
      this.readyPromise = null;
      this.revision = '';
      this.lastStageDurationMs = 0;
      this.stageWaiters = new Map();
      this.metricWaiters = [];
      this.messages = new Set();
      this.errors = new Set();
      this.states = new Set();
      this.meters = new Set();
      this.pendingMeters = new Map();
      this.meterFrame = 0;
      this.playing = false;
      this.clock = false;
      this.transportTick = 0;
      this.transportTime = 0;
      this.outputTimeline = { available: false, samples: 0, misses: 0, maxLagMs: 0, maxExcessMs: 0, lastMiss: null };
      this.outputTimelineTimer = 0;
      this.callbackDurations = new Uint32Array(256);
      this.callbackSamples = 0;
      this.callbackDurationExceedances = 0;
      this.callbackGapExceedances = 0;
      this.maxCallbackDurationMs = 0;
    }

    async startAudio(capture = false) {
      if (this.startPromise) await this.startPromise;
      if (this.context) {
        await this.context.resume();
        if (capture) await this.stageCurrentScore('', true);
        return { sampleRate: this.context.sampleRate, clock: this.clock };
      }
      const pending = this.startContext(capture);
      this.startPromise = pending;
      try { return await pending; }
      finally { if (this.startPromise === pending) this.startPromise = null; }
    }

    async startContext(capture) {
      const Context = window.AudioContext || window.webkitAudioContext;
      if (!Context) throw new Error('AudioWorklet is not available in this browser');
      this.context = new Context({ sampleRate: 48000, latencyHint: 'interactive' });
      try {
        const imageResponse = await fetch(`/api/kernel-image?rate=${this.context.sampleRate}${capture?'&capture=1':''}`, { cache: 'no-store' });
        if (!imageResponse.ok) throw new Error((await imageResponse.text()) || 'Cannot load the score image');
        const revision = imageResponse.headers.get('X-Cicada-Revision') || '';
        const image = await imageResponse.arrayBuffer();
        const kind = this.imageModuleKind(image);
        const module = await this.loadModule(kind);
        this.bpmMilli = new DataView(image).getUint32(12, true);
        await this.context.audioWorklet.addModule('/audio/cicada-capture.js');
        await this.context.audioWorklet.addModule('/audio/cicada-capture-processor.js');
        const prepared = await this.createNode(module, image, revision);
        this.node = prepared.node;
        this.readyPromise = Promise.resolve(prepared.ready);
        this.moduleKind = kind;
        this.revision = prepared.ready.r;
        this.clock = prepared.ready.c;
        await this.context.resume();
        this.node.port.postMessage({ t: 'q', l: this.contextLatencyMs() });
        if (!this.outputTimelineTimer) this.outputTimelineTimer = setInterval(() => this.sampleOutputTimeline(), 5);
      } catch (error) {
        // Leave no half-started context behind, so a later Start retries from scratch.
        const failed = this.context;
        if (this.node) this.disposeNode(this.node);
        this.context = null;
        this.node = null;
        this.readyPromise = null;
        this.moduleKind = '';
        if (failed && failed.close) failed.close().catch(() => {});
        throw error;
      }
      return { sampleRate: this.context.sampleRate, clock: this.clock };
    }

    imageModuleKind(image) {
      const bytes = new Uint8Array(image);
      if (bytes.length < 32 || bytes[0] !== 67 || bytes[1] !== 73 || bytes[2] !== 67 || bytes[3] !== 49) throw new Error('Score image header is invalid');
      // Image versions 13/14 carry required capabilities at byte offset 30.
      return new DataView(image).getUint16(30, true) & 32 ? 'keys' : 'core';
    }

    loadModule(kind) {
      if (!this.modulePromises.has(kind)) {
        const pending = fetch(`/api/kernel.wasm${kind === 'keys' ? '?keys=1' : ''}`, { cache: 'no-store' }).then(async response => {
          if (!response.ok) throw new Error((await response.text()) || 'Cannot load the audio kernel');
          return WebAssembly.compile(await response.arrayBuffer());
        }).catch(error => {
          if (this.modulePromises.get(kind) === pending) this.modulePromises.delete(kind);
          throw error;
        });
        this.modulePromises.set(kind, pending);
      }
      return this.modulePromises.get(kind);
    }

    async createNode(module, image, revision) {
      const node = new AudioWorkletNode(this.context, 'cicada', {
        numberOfInputs: 1, numberOfOutputs: 1, channelCount: 2, channelCountMode: 'max', channelInterpretation: 'discrete', outputChannelCount: [2],
        processorOptions: { m: module, i: image, r: revision, l: this.contextLatencyMs() }
      });
      let timeout;
      const ready = new Promise((resolve, reject) => {
        timeout = setTimeout(() => reject(new Error('AudioWorklet did not initialize')), 10000);
        node.port.onmessage = event => {
          const data = event.data;
          if (data.t === 'r') { clearTimeout(timeout); resolve(data); }
          else if (data.t === 'e' && node !== this.node) { clearTimeout(timeout); reject(new Error(data.e)); }
          if (node === this.node) this.receive(data, node);
        };
        node.port.onmessageerror = () => { clearTimeout(timeout); reject(new Error('AudioWorklet message could not be decoded')); };
      });
      node.connect(this.context.destination);
      try { return { node, ready: await ready }; }
      catch (error) { clearTimeout(timeout); this.disposeNode(node); throw error; }
    }

    disposeNode(node) {
      try { this.sendCommands([{ op: 2 }], node); } catch (_) {}
      node.disconnect();
      node.port.onmessage = node.port.onmessageerror = null;
      node.port.close?.();
    }

    currentTick() {
      const seconds = this.playing && this.context ? Math.max(0, this.context.currentTime - this.transportTime) : 0;
      return Math.max(0, Math.round(this.transportTick + seconds * (this.bpmMilli || 120000) * 960 / 60000));
    }

    anchorTick(tick) {
      this.transportTick = Math.max(0, tick);
      this.transportTime = this.context?.currentTime || 0;
    }

    async replaceModule(kind, image, revision) {
      const module = await this.loadModule(kind);
      const previous = this.node;
      const prepared = await this.createNode(module, image, revision);
      try {
        const capture = this.captureSession;
        if (capture && ['armed', 'recording', 'saving'].includes(capture.status.state)) {
          capture.status.error = 'Capture stopped because the score changed audio modules. Arm the microphone again to record.';
          capture.notify();
          await capture.stop();
        }
        const tick = this.currentTick(), playing = this.playing;
        this.node = prepared.node;
        this.readyPromise = Promise.resolve(prepared.ready);
        this.moduleKind = kind;
        this.revision = prepared.ready.r;
        this.clock = prepared.ready.c;
        this.bpmMilli = new DataView(image).getUint32(12, true);
        this.anchorTick(tick);
        this.pendingMeters.clear();
        this.callbackDurations.fill(0);
        this.callbackSamples = this.callbackDurationExceedances = this.callbackGapExceedances = this.maxCallbackDurationMs = 0;
        this.sendCommands([{ op: 3, arg0: Math.floor(tick / 3840), arg1: tick % 3840 }, ...(playing ? [{ op: 1 }] : [])]);
        this.disposeNode(previous);
        for (let i = 0; i < this.metricWaiters.length; i++) this.node.port.postMessage({ t: 'q', l: this.contextLatencyMs() });
        this.notifyState();
      } catch (error) { this.disposeNode(prepared.node); throw error; }
      // Replacing an instance resets held notes and effect tails. The existing
      // context clock continues; transport resumes at its estimated latest tick.
    }

    receive(data, node = this.node) {
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
            if (message.kind === 1) this.anchorTick(message.tick);
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
          node.port.postMessage({ t: 'b', bytes: data.bytes }, [data.bytes]);
        }
      } else if (data.t === 's') {
        const tick = this.currentTick();
        this.playing = data.p;
        this.anchorTick(tick);
        this.notifyState();
      } else if (data.t === 'f') {
        this.playing = false;
        this.notifyState();
        const message = { kind: 7, track: 255, a: data.a, b: 0, tick: 0 };
        for (const callback of this.messages) callback(message);
        this.raise(audioFault(data.a));
      } else if (data.t === 'r') {
        this.clock = data.c;
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
        if (waiter) waiter({...data, q: 128000 / this.context.sampleRate, l: this.contextLatencyMs(), cp: this.clock ? .1 : 1, underruns: data.u, clock: this.clock, memoryBytes: data.m,
          callbackP99Ms: this.callbackP99Ms(),
          callbackSamples: this.callbackSamples,
          callbackDurationExceedances: this.callbackDurationExceedances,
          callbackGapExceedances: this.callbackGapExceedances,
          maxCallbackDurationMs: this.maxCallbackDurationMs,
          outputTimeline: {...this.outputTimeline, latencyMs: this.contextLatencyMs(),
            quantumMs: 128000 / this.context.sampleRate,
            limitMs: this.contextLatencyMs() + 128000 / this.context.sampleRate}});
      }
    }

    contextLatencyMs() {
      if (!this.context) return 0;
      return ((this.context.baseLatency || 0) + (this.context.outputLatency || 0)) * 1000;
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
      if (excess > 128000 / context.sampleRate) {
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
    onMeters(callback) { this.meters.add(callback); return () => this.meters.delete(callback); }

    sendCommands(records, node = this.node) {
      if (!node) throw new Error('Select Browser mode and start audio first');
      const bytes = new Uint8Array(records.length * 24), view = new DataView(bytes.buffer);
      records.forEach((record, index) => {
        const at = index * 24;
        view.setUint8(at, record.op); view.setUint8(at + 1, record.track ?? 255);
        view.setUint16(at + 2, record.index || 0, true);
        view.setUint32(at + 4, record.arg0 || 0, true);
        view.setUint32(at + 8, record.arg1 || 0, true);
        view.setBigInt64(at + 16, BigInt(record.tick || 0), true);
        if (node === this.node && record.op === 3) this.anchorTick((record.arg0 || 0) * 3840 + (record.arg1 || 0));
      });
      node.port.postMessage({ t: 'c', bytes }, [bytes.buffer]);
    }

    play() {
      if (this.playing) return;
      this.sendCommands([{ op: 3, arg0: 0 }, { op: 1 }]);
    }
    stop(force = false) { if (this.playing || force) this.sendCommands([{ op: 2 }]); }
    launchScene(index) { this.sendCommands([{ op: 10, index, arg0: 2 }]); }
    selectPattern(track, slot) { this.sendCommands([{ op: 9, track, index: slot, arg0: 2 }]); }
    playFrom(bar) { this.sendCommands([{ op: 3, arg0: Math.max(0, bar - 1) }, { op: 1 }]); }

    stageCurrentScore(revision = '', capture = false) {
      const pending = this.stagePromise.catch(() => {}).then(() => this.prepareScore(revision, capture));
      this.stagePromise = pending;
      return pending;
    }

    async prepareScore(revision, capture) {
      if (!this.node) return false;
      if (revision && revision === this.revision) return true;
      const started = performance.now();
      const response = await fetch(`/api/kernel-image?rate=${this.context.sampleRate}${capture?'&capture=1':''}`, { cache: 'no-store' });
      if (!response.ok) throw new Error((await response.text()) || 'Cannot prepare the edited score');
      const imageRevision = response.headers.get('X-Cicada-Revision') || '';
      if (revision && imageRevision !== revision) throw new Error('Score changed while preparing the browser kernel');
      const image = await response.arrayBuffer();
      const kind = this.imageModuleKind(image);
      if (kind !== this.moduleKind) {
        await this.replaceModule(kind, image, imageRevision);
        this.lastStageDurationMs = performance.now() - started;
        return true;
      }
      this.bpmMilli = new DataView(image).getUint32(12, true);
      const ready = new Promise((resolve, reject) => this.stageWaiters.set(imageRevision, { resolve, reject }));
      this.node.port.postMessage({ t: 'i', i: image, r: imageRevision }, [image]);
      await ready;
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
