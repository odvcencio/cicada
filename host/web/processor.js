class CicadaKernel extends AudioWorkletProcessor {
  constructor({ processorOptions: o }) {
    super();
    const port = this.port;
    const post = port.postMessage.bind(port);
    const CapabilityChords = 1, CapabilityUnifiedImage = 65536;
    const invalid = message => { throw new Error(message); };
    // Keep render state in constructor-scoped bindings so process reuses it.
    let capture, module = o.m, active, previous, pending, bankCopy, playing = false, deferred, deferredCount = 0, rate = sampleRate, bank, clock, preciseClock, underruns = 0, timingHistogram = new Uint32Array(256), durationLimit, latency, quantumMs, lastStart = 0, callbacks = 0, view, message, transfer, ready = true, swap, swapView, faultMessage = { t: 'f', a: 0 }, fadeTotal = rate / 200 | 0, fadeLeft = 0, engineSample = 0, anchorSample = 0, anchorTick = 0, bpm = 120000, nextBarTick = 3840, stallNext = false;
    const create = async image => {
      const bytes = new Uint8Array(image), header = new DataView(image);
      if (bytes.length < 32 || header.getUint32(0) !== 0x43494331) invalid('Invalid CIC1 image header');
      const version = bytes[4] | bytes[5] << 8;
      if (version === 14) invalid('Ambiguous image14; recompile source');
      if (![8, 9, 10, 11, 12, 13, 15].includes(version)) invalid(`Unsupported image version ${version}`);
      const instance = await WebAssembly.instantiate(module);
      const exports = instance.exports;
      // TinyGo reactor exports require actual runtime initialization, including
      // the capability query. No image/bank upload occurs before negotiation.
      if (typeof exports._initialize !== 'function') invalid('Reactor initializer missing');
      exports._initialize();
      // Short local aliases reduce downloads without changing the export ABI.
      // These are the original exports, never wrappers around realtime calls.
      const x = {};
      for (const name in exports) x[name.replace('gosx_audio_', '')] = exports[name];
      if (x.capabilities !== undefined && typeof x.capabilities !== 'function') invalid('Invalid capability export');
      const capability = x.capabilities ? x.capabilities() : 0;
      if (capability !== (capability >>> 0) || (capability & ~0x327ff)) invalid('Invalid capabilities');
      if (version === 15 && !(capability & CapabilityUnifiedImage)) invalid('image15 requires CapabilityUnifiedImage');
      if (bank) {
        const bankPtr = x.bank_alloc(bank.byteLength);
        if (!bankPtr) invalid('bank');
        const source = new Uint8Array(bank);
        const destination = new Uint8Array(x.memory.buffer, bankPtr, source.byteLength);
        const chunks = [];
        for (let at = 0; at < source.byteLength; at += 65_536) {
          const end = at + 65_536;
          chunks.push([source.subarray(at, end), destination.subarray(at, end)]);
        }
        await new Promise(resolve => { bankCopy = [chunks, 0, resolve]; });
        if (x.bank_install(rate) !== 0) invalid('bank');
      }
      const ptr = x.project_alloc(bytes.length);
      if (!ptr) invalid('image');
      new Uint8Array(x.memory.buffer, ptr, bytes.length).set(bytes);
      if (x.init(rate, 128, 2) !== 0) invalid('rate');
      if (!bank) {
        const bankPtr = x.bank_image_ptr(), bankLen = x.bank_image_len();
        if (!bankPtr || !bankLen) invalid('bank');
        bank = new Uint8Array(x.memory.buffer, bankPtr, bankLen).slice().buffer;
      }
      const max = 128;
      const commandBuffer = x.cmd_cap() * 24;
      if (!deferred) deferred = new Uint8Array(commandBuffer);
      return {
        x, p: capability, b: header.getUint32(12, true),
        d: new Uint8Array(x.memory.buffer, x.cmd_ptr(), commandBuffer),
        m: new Uint8Array(x.memory.buffer, x.msg_ptr(), 256 * 16),
        l: new Float32Array(x.memory.buffer, x.out_ptr(), max),
        r: new Float32Array(x.memory.buffer, x.out_ptr() + max * 4, max)
      };
    };
    // Shared fixed little-endian tick writer: no temporary view or allocation.
    const putTick = (bytes, at, tick) => {
      for (let j = 0; j < 8; j++) { bytes[at + j] = tick; tick = Math.floor(tick / 256); }
    };
    const flush = () => {
      for (let i = 0; i < deferredCount; i++) commit(active, deferred, i * 24, 24);
      deferredCount = 0;
    };
    // Make a staged instance the active one: at a stop, or when nothing is playing.
    const promote = (next) => {
      pending = null;
      if (CICADA_CAPTURE && capture) capture.engineEpoch++;
      active = next;
      bpm = next.b;
      engineSample = anchorSample = 0;
      anchorTick = 0;
      nextBarTick = 3840;
      flush();
    };
    const receive = (data) => {
      if (CICADA_CAPTURE && data.t === 'capture-init') { if (capture) capture.port.close(); capture = new globalThis.CicadaCapture(data, rate, port); return; }
      if (CICADA_CAPTURE && data.t === 'capture-control') { if (capture) capture.receive(data); return; }
      if (data.t === 'b') {
        view = new Uint8Array(data.bytes);
        message.bytes = data.bytes;
        transfer[0] = data.bytes;
        ready = true;
        return;
      }
      if (data.t === 'c') return commit(active, data.bytes);
      if (data.t === 'i') {
        if (pending === true) return reject('Image load in progress', data.r);
        const staged = pending;
        pending = true;
        create(data.i).then(next => {
          if (playing) pending = next;
          else promote(next);
          post({ t: 't', r: data.r });
        }, error => {
          pending = staged;
          if (!pending) flush();
          reject(String(error), data.r);
        });
      }
      if (data.t === 'z') stallNext = true;
      if (data.t === 'p') {
        timingHistogram.fill(0);
      }
      if (data.t === 'q') report(data.l);
    };
    const commit = (target, bytes, start = 0, length = bytes.byteLength) => {
      // Reject the whole batch before writing command memory or changing playback.
      if (!target || length % 24 || length > target.d.length || start + length > bytes.byteLength) return reject('cmd');
      const count = length / 24;
      let queued = 0;
      for (let i = 0; i < count; i++) {
        const op = bytes[start + i * 24];
        if (op === 22 && !(target.p & CapabilityChords)) return reject('Unsupported chord opcode22');
        if (op >= 26 && op <= 28 && !(target.p & 131072)) return reject('spatial');
        if (pending && (op === 10 || op === 9)) queued++;
      }
      if (deferredCount + queued > deferred.byteLength / 24) return reject('cmd');
      let seekTick = null, starts = false, stops = false, committed = 0;
      for (let i = 0; i < count; i++) {
        const at = start + i * 24, op = bytes[at];
        if (target === active && op === 3) {
          const bar = bytes[at + 4] | bytes[at + 5] << 8 | bytes[at + 6] << 16 | bytes[at + 7] << 24;
          const tick = bytes[at + 8] | bytes[at + 9] << 8 | bytes[at + 10] << 16 | bytes[at + 11] << 24;
          seekTick = (bar >>> 0) * 3840 + (tick >>> 0);
        }
        if (target === active && op === 1) starts = true;
        if (target === active && op === 2) stops = true;
        const launch = op === 10 || op === 9;
        const queued = launch && pending;
        const destination = queued ? deferred : target.d;
        const to = (queued ? deferredCount++ : committed++) * 24;
        for (let j = 0; j < 24; j++) destination[to + j] = bytes[at + j];
        if (launch && !queued) putTick(destination, to + 16, nextBarTick);
      }
      if (committed) target.x.cmd_commit(committed);
      if (target === active) {
        if (seekTick !== null) {
          anchorSample = engineSample;
          anchorTick = seekTick;
          nextBarTick = (Math.floor(seekTick / 3840) + 1) * 3840;
          if (starts) { previous = null; fadeLeft = fadeTotal; }
        }
        if (starts) { playing = true; post({ t: 's', p: true }); }
        if (stops) { if (CICADA_CAPTURE && capture && capture.recording) capture.stopping = true; playing = false; post({ t: 's', p: false }); if (pending && pending !== true) promote(pending); }
      }
    };
    const sampleAtTick = (tick) => {
      const delta = Math.max(0, tick - anchorTick);
      return anchorSample + Math.ceil(delta * 60000 * rate / (bpm * 960));
    };
    const swapAtBoundary = (tick) => {
      if (!pending || pending === true) { nextBarTick += 3840; return; }
      const next = pending;
      pending = null;
      swapView.setUint32(4, tick / 3840, true);
      next.d.set(swap, 0);
      next.x.cmd_commit(2);
      previous = active;
      if (CICADA_CAPTURE && capture) capture.engineEpoch++;
      active = next;
      bpm = next.b;
      engineSample = anchorSample = 0;
      anchorTick = tick;
      nextBarTick = tick + 3840;
      fadeLeft = fadeTotal;
      for (let i = 0; i < deferredCount; i++) {
        const from = i * 24;
        for (let j = 0; j < 24; j++) active.d[from + j] = deferred[from + j];
        putTick(active.d, from + 16, nextBarTick);
      }
      if (deferredCount) active.x.cmd_commit(deferredCount);
      deferredCount = 0;
    };
    const drain = (target, active) => {
      if (!target) return false;
      const count = target.x.msg_drain();
      let fault = false;
      let sent = false;
      for (let i = 0; i < count; i++) if (target.m[i * 16] === 7) fault = true;
      if (active && ready && count) {
          let length = count * 16;
          for (let i = 0; i < length; i++) view[i] = target.m[i];
          message.n = length;
          ready = false;
          post(message, transfer);
          sent = true;
      }
      if (fault && !sent) {
        faultMessage.a = target.m[2] | target.m[3] << 8;
        post(faultMessage);
      }
      return fault;
    };
    const reject = (e, r) => post({ t: 'x', e, r });
    const report = l => {
      latency = l;
      post({ t: 'q', u: underruns,
        q: quantumMs, dl: durationLimit, gl: latency + durationLimit, m: active?.x.memory.buffer.byteLength || 0,
        d: timingHistogram });
    };
    const process = (inputs, outputs) => {
      const start = clock();
      const gap = lastStart ? start - lastStart : 0;
      lastStart = start;
      callbacks++;
      const measuredPlaying = playing;
      const left = outputs[0][0], right = outputs[0][1];
      quantumMs = left.length * 1000 / rate;
      durationLimit = quantumMs + (preciseClock ? 0.1 : 1);
      left.fill(0); right.fill(0);
      const lead = CICADA_CAPTURE && capture ? capture.process(inputs, left.length, engineSample - anchorSample + Math.ceil(anchorTick * 60000 * rate / (bpm * 960)), bpm, playing, left, right) : 0;
      if (bankCopy) {
        const c = bankCopy, chunk = c[0][c[1]++];
        chunk[1].set(chunk[0]);
        if (c[1] === c[0].length) { bankCopy = null; c[2](); }
      }
      if (playing) {
        let offset = lead;
        while (offset < left.length) {
          let boundary = sampleAtTick(nextBarTick);
          if (engineSample === boundary) {
            if (pending) swapAtBoundary(nextBarTick);
            else nextBarTick += 3840;
            boundary = sampleAtTick(nextBarTick);
          }
          const frames = Math.min(128, left.length - offset, pending ? Math.max(1, boundary - engineSample) : left.length - offset);
          active.x.render(frames);
          if (previous && fadeLeft > 0) previous.x.render(frames);
          for (let i = 0; i < frames; i++) {
            const n = offset + i;
            let l = active.l[i], r = active.r[i];
            if (fadeLeft > 0) {
              const gain = (fadeTotal - fadeLeft + 1) / fadeTotal;
              if (previous) {
                l = previous.l[i] * (1 - gain) + l * gain;
                r = previous.r[i] * (1 - gain) + r * gain;
              } else { l *= gain; r *= gain; }
              fadeLeft--;
              if (!fadeLeft) previous = null;
            }
            left[n] = l; right[n] = r;
          }
          engineSample += frames;
          offset += frames;
          if (pending && engineSample === boundary) swapAtBoundary(nextBarTick);
          else if (!pending && engineSample >= boundary) nextBarTick += 3840;
        }
        if (!(callbacks & 7) && ready) {
          if (previous) drain(previous, false);
          if (drain(active, true)) { active = null; playing = false; left.fill(0); right.fill(0); }
        }
      }
      if (stallNext) {
        stallNext = false;
        const until = Date.now() + 20;
        while (Date.now() < until) {}
      }
      const elapsed = clock() - start;
      if (measuredPlaying) {
        const bucket = Math.min(253, Math.max(0, elapsed * 4 | 0));
        timingHistogram[bucket]++;
        if (elapsed > durationLimit) timingHistogram[254]++;
        if (gap > latency + durationLimit) timingHistogram[255]++;
      }
      if (elapsed > durationLimit || gap > latency + durationLimit) underruns++;
      return true;
    };
    preciseClock = !!globalThis.performance?.now;
    clock = preciseClock ? performance.now.bind(performance) : Date.now;
    quantumMs = 128000 / rate;
    durationLimit = quantumMs + (preciseClock ? 0.1 : 1);
    latency = o.l || 0;
    const buffer = new ArrayBuffer(4096);
    view = new Uint8Array(buffer);
    message = { t: 'm', bytes: buffer, n: 0 };
    transfer = [buffer];
    swap = new Uint8Array(48);
    swap[0] = 3; swap[1] = 255;
    swap[24] = 1; swap[25] = 255;
    swapView = new DataView(swap.buffer);
    create(o.i).then(next => {
      active = next;
      bpm = next.b;
      post({ t: 'r', r: o.r, mem: next.x.memory.buffer.byteLength, c: preciseClock, p: next.p });
    }, error => {
      active = null;
      playing = false;
      post({ t: 'e', e: String(error) });
    });
    port.onmessage = event => receive(event.data);
    this.process = process;
  }
}
registerProcessor('cicada', CicadaKernel);
