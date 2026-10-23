class CicadaKernel extends AudioWorkletProcessor {
  constructor({ processorOptions: o }) {
    super();
    const port = this.port;
    const post = port.postMessage.bind(port);
    // The kernel drains up to engine.MessageCapacity (512) 16-byte messages; a Go test keeps this equal.
    const MESSAGE_BYTES = 512 * 16;
    const invalid = message => { throw new Error(message); };
    // Keep render state in constructor-scoped bindings so process reuses it.
    let capture, active, previous, pending, bankCopy, playing = false, deferred, deferredCount = 0, rate = sampleRate, bank, clock, preciseClock, underruns = 0, timingHistogram = new Uint32Array(256), durationLimit, latency, quantumMs, lastStart = 0, callbacks = 0, view, message, transfer, ready = true, swap, swapView, faultMessage = { t: 'f', a: 0 }, stoppedMessage = { t: 's', p: false }, fadeTotal = rate / 200 | 0, fadeLeft = 0, engineSample = 0, anchorSample = 0, anchorTick = 0, bpm = 120000, nextBarTick = 3840, stallNext = false, memoryPeak = 0;
    const instances = [];
    let memoryCount = 0;
    const memoryBytes = () => {
      let total = 0;
      memoryCount = 0;
      for (const memory of instances) if (memory) { memoryCount++; total += memory.buffer.byteLength; }
      if (total > memoryPeak) memoryPeak = total;
      return total;
    };
    // Reuse slots: deleting from a Set can resize its backing store on render.
    const release = target => { if (target) instances[target.s] = null; };
    const create = async image => {
      const bytes = new Uint8Array(image), header = new DataView(image);
      if (bytes.length < 32 || header.getUint32(0) !== 0x43494331) invalid('Invalid CIC1 header');
      const version = bytes[4] | bytes[5] << 8;
      if (version === 14) invalid('Ambiguous image14; recompile');
      if (version < 8 || version > 18) invalid(`Unsupported image ${version}`);
      const instance = await WebAssembly.instantiate(o.m);
      const exports = instance.exports;
      let slot = 0;
      while (instances[slot]) slot++;
      instances[slot] = exports.memory;
      memoryBytes();
      try {
        // TinyGo reactor exports require actual runtime initialization, including
        // the capability query. No image/bank upload occurs before negotiation.
        if (typeof exports._initialize !== 'function') invalid('Missing audio init');
        exports._initialize();
        // Short local aliases reduce downloads without changing the export ABI.
        // These are the original exports, never wrappers around realtime calls.
        const x = {};
        for (const name in exports) x[name.replace('gosx_audio_', '')] = exports[name];
        const query = x.capabilities;
        if (query !== undefined && typeof query !== 'function') invalid('Invalid capability');
        const capability = query ? query() : 0;
        if ((capability & 0x7fefff) !== capability) invalid('Invalid capabilities');
        // Required feature bits for image versions 15 through 18. Initialization
        // may allocate these small tables; render callbacks never enter this path.
        for (let index = 0; index <= version - 15; index++) {
          if (!(capability & (1 << [16,18,19,21][index]))) invalid(`image${version} needs Capability${['UnifiedImage', 'Performance', 'Gestures', 'SharedControls'][index]}`);
        }
        if (bank) {
          const bankPtr = x.bank_alloc(bank.byteLength);
          if (!bankPtr) invalid('bank');
          const source = new Uint8Array(bank);
          const destination = new Uint8Array(x.memory.buffer, bankPtr, source.length);
          const chunks = [];
          for (let at = 0; at < source.length; at += 65_536) {
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
          bank = x.memory.buffer.slice(bankPtr, bankPtr + bankLen);
        }
        const max = 128, out = x.out_ptr();
        const commandBuffer = x.cmd_cap() * 24;
        if (!deferred) deferred = new Uint8Array(commandBuffer);
        memoryBytes();
        return {
          x, s: slot, p: capability, b: header.getUint32(12, true),
          d: new Uint8Array(x.memory.buffer, x.cmd_ptr(), commandBuffer),
          m: new Uint8Array(x.memory.buffer, x.msg_ptr(), MESSAGE_BYTES),
          l: new Float32Array(x.memory.buffer, out, max),
          r: new Float32Array(x.memory.buffer, out + max * 4, max)
        };
      } catch (error) { instances[slot] = null; throw error; }
    };
    // Shared fixed little-endian tick writer: no temporary view or allocation.
    const putTick = (bytes, at, tick) => {
      for (let j = 0; j < 8; j++) { bytes[at + j] = tick; tick = Math.floor(tick / 256); }
    };
    const flush = () => {
      for (let i = 0; i < deferredCount; i++) commit(active, deferred, i * 24, 24);
      deferredCount = 0;
    };
    // Promote before transport commands so their seek wins over reset anchors.
    const promote = (next, tick = 0) => {
      release(previous);
      previous = tick ? active : null;
      if (!tick) release(active);
      pending = null;
      if (CICADA_CAPTURE && capture) capture.engineEpoch++;
      active = next;
      bpm = next.b;
      engineSample = anchorSample = 0;
      anchorTick = tick;
      nextBarTick = tick + 3840;
      if (tick) fadeLeft = fadeTotal;
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
        if (pending === true) return reject('Image loading', data.r);
        const staged = pending;
        pending = true;
        create(data.i).then(next => {
          release(staged);
          if (playing) pending = next;
          else promote(next);
          post({ t: 't', r: data.r });
        }, error => {
          pending = staged;
          if (!pending) flush();
          reject(String(error), data.r);
        });
      }
      if (typeof CICADA_TEST !== 'undefined' && CICADA_TEST && data.t === 'z') stallNext = true;
      if (data.t === 'p') {
        timingHistogram.fill(0);
      }
      if (data.t === 'q') { latency = data.l; report(); }
    };
    const commit = (target, bytes, start = 0, length = bytes.length) => {
      // Reject the whole batch before writing command memory or changing playback.
      if (!target) return reject('Reload audio');
      if (length % 24 || start + length > bytes.length) return reject();
      let seekTick = null, starts = false, stops = false, committed = 0, queued = 0;
      for (let at = start; at < start + length; at += 24) {
        const op = bytes[at];
        if (op === 3) {
          let bar = 0, tick = 0;
          for (let j = 3; j >= 0; j--) { bar = bar * 256 + bytes[at + 4 + j]; tick = tick * 256 + bytes[at + 8 + j]; }
          seekTick = bar * 3840 + tick;
        }
        if (op === 1) starts = true;
        if (op === 2) stops = true;
      }
      const promotes = pending?.x && (stops || starts && seekTick !== null);
      if (promotes) target = pending;
      if (length > target.d.length) return reject();
      for (let at = start; at < start + length; at += 24) {
        const op = bytes[at];
        if ((op === 32 || op === 33) && !(target.p & 4194304)) return reject('Unsupported live shared controls');
        if (op === 22 && !(target.p & 1)) return reject('Unsupported chord opcode22');
        if (op >= 26 && op <= 28 && !(target.p & 131072)) return reject('spatial');
        if (pending && !promotes && (op === 10 || op === 9)) queued++;
      }
      if (deferredCount + queued > deferred.length / 24) return reject();
      if (promotes) promote(target);
      for (let at = start; at < start + length; at += 24) {
        const op = bytes[at];
        const launch = op === 10 || op === 9;
        const queued = launch && pending;
        const destination = queued ? deferred : target.d;
        const to = (queued ? deferredCount++ : committed++) * 24;
        for (let j = 0; j < 24; j++) destination[to + j] = bytes[at + j];
        if (launch && !queued) putTick(destination, to + 16, nextBarTick);
      }
      if (committed) target.x.cmd_commit(committed);
      if (seekTick !== null) {
        anchorSample = engineSample;
        anchorTick = seekTick;
        nextBarTick = (Math.floor(seekTick / 3840) + 1) * 3840;
        if (starts) { release(previous); previous = null; fadeLeft = fadeTotal; }
      }
      if (starts) { playing = true; post({ t: 's', p: true }); }
      if (stops) { if (CICADA_CAPTURE && capture && capture.recording) capture.stopping = true; playing = false; post(stoppedMessage); }
    };
    // The kernel owns the tempo map, so a scene tempo moves the bar boundary.
    const sampleAtTick = tick => anchorSample + active.x.tick_frames(anchorTick, tick);
    const swapAtBoundary = (tick) => {
      if (!pending?.x) { nextBarTick += 3840; return; }
      const next = pending;
      swapView.setUint32(4, tick / 3840, true);
      next.d.set(swap, 0);
      next.x.cmd_commit(2);
      promote(next, tick);
    };
    const drain = (target, active) => {
      if (!target) return false;
      const count = target.x.msg_drain();
      let fault = false;
      for (let i = 0; i < count; i++) {
        const at = i * 16;
        if (target.m[at] === 7) { fault = true; faultMessage.a = target.m[at + 2] | target.m[at + 3] << 8; }
      }
      if (active && count) {
          let length = count * 16;
          // Copy the fixed message page; n bounds the valid records. A sliced
          // typed-array view would allocate inside the render callback.
          view.set(target.m);
          message.n = length;
          ready = false;
          post(message, transfer);
      } else if (fault) {
        post(faultMessage);
      }
      return fault;
    };
    const reject = (e = 'cmd', r) => post({ t: 'x', e, r });
    const report = () => {
      post({ t: 'q', u: underruns,
        q: quantumMs, dl: durationLimit, gl: latency + durationLimit, m: memoryBytes(), mp: memoryPeak, n: memoryCount,
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
          // A tempo increase can put the kernel's bar line behind the playhead.
          while (engineSample >= boundary) {
            if (pending) swapAtBoundary(nextBarTick);
            else nextBarTick += 3840;
            boundary = sampleAtTick(nextBarTick);
          }
          const frames = Math.min(128, left.length - offset, pending ? boundary - engineSample : left.length - offset);
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
              if (!fadeLeft) { release(previous); previous = null; }
            }
            left[n] = l; right[n] = r;
          }
          engineSample += frames;
          offset += frames;
          if (pending && engineSample >= boundary) swapAtBoundary(nextBarTick);
          else if (!pending && engineSample >= boundary) nextBarTick += 3840;
        }
        if (!(callbacks & 7) && ready) {
          if (drain(active, true) || previous && drain(previous, false)) { release(active); release(previous); active = previous = null; playing = false; post(stoppedMessage); left.fill(0); right.fill(0); }
        }
      }
      if (typeof CICADA_TEST !== 'undefined' && CICADA_TEST && stallNext) {
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
    const buffer = new ArrayBuffer(MESSAGE_BYTES);
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
      post({ t: 'e', e: String(error) });
    });
    port.onmessage = event => receive(event.data);
    this.process = process;
  }
}
registerProcessor('cicada', CicadaKernel);
