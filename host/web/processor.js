class CicadaKernel extends AudioWorkletProcessor {
  constructor({ processorOptions: o }) {
    super();
    const port = this.port;
    // Keep render state in constructor-scoped bindings so process reuses it.
    let capture, module, active, previous, pending, bankCopy, playing, deferred, deferredCount, rate, bank, clock, preciseClock, underruns, timingHistogram, durationLimit, gapLimit, lastStart, callbacks, view, viewData, message, transfer, ready, swap, swapView, faultMessage, fadeTotal, fadeLeft, engineSample, anchorSample, anchorTick, bpm, nextBarTick, stallNext;
    const create = async image => {
      const bytes = new Uint8Array(image);
      const instance = await WebAssembly.instantiate(module);
      const x = instance.exports;
      // TinyGo reactor exports require runtime initialization, including the
      // capability query. Reject unsupported payloads before any image upload.
      const capability = (x._initialize(), x.gosx_audio_capabilities?.() & 1);
      if ((bytes[4] | bytes[5] << 8) >= 14 && !capability) throw new Error('Unsupported chord image14');
      if (bank) {
        const bankPtr = x.gosx_audio_bank_alloc(bank.byteLength);
        if (!bankPtr) throw new Error('bank');
        const source = new Uint8Array(bank);
        const destination = new Uint8Array(x.memory.buffer, bankPtr, source.byteLength);
        const chunks = [];
        for (let at = 0; at < source.byteLength; at += 65_536) {
          const end = Math.min(at + 65_536, source.byteLength);
          chunks.push([source.subarray(at, end), destination.subarray(at, end)]);
        }
        await new Promise(resolve => { bankCopy = [chunks, 0, resolve]; });
        if (x.gosx_audio_bank_install(rate) !== 0) throw new Error('bank');
      }
      const ptr = x.gosx_audio_project_alloc(bytes.length);
      if (!ptr) throw new Error('image');
      new Uint8Array(x.memory.buffer, ptr, bytes.length).set(bytes);
      if (x.gosx_audio_init(rate, 128, 2) !== 0) throw new Error('rate');
      if (!bank) {
        const bankPtr = x.gosx_audio_bank_image_ptr(), bankLen = x.gosx_audio_bank_image_len();
        if (!bankPtr || !bankLen) throw new Error('bank');
        bank = new Uint8Array(x.memory.buffer, bankPtr, bankLen).slice().buffer;
      }
      const max = 128;
      const commandBuffer = x.gosx_audio_cmd_cap() * 24;
      if (!deferred) deferred = new Uint8Array(commandBuffer);
      return {
        x, p: capability, b: new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(12, true),
        d: new Uint8Array(x.memory.buffer, x.gosx_audio_cmd_ptr(), commandBuffer),
        m: new Uint8Array(x.memory.buffer, x.gosx_audio_msg_ptr(), 256 * 16),
        l: new Float32Array(x.memory.buffer, x.gosx_audio_out_ptr(), max),
        r: new Float32Array(x.memory.buffer, x.gosx_audio_out_ptr() + max * 4, max)
      };
    };
    // Shared fixed little-endian tick writer: no temporary view or allocation.
    const putTick = (bytes, at, tick) => {
      const low = tick >>> 0, high = Math.floor(tick / 4294967296) >>> 0;
      for (let j = 0; j < 4; j++) { bytes[at + j] = low >>> (j * 8); bytes[at + j + 4] = high >>> (j * 8); }
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
        viewData = new DataView(data.bytes);
        message.bytes = data.bytes;
        transfer[0] = data.bytes;
        ready = true;
        return;
      }
      if (data.t === 'c') return commit(active, data.bytes);
      if (data.t === 'i') {
        pending = true;
        create(data.i).then(next => {
          if (playing) pending = next;
          else promote(next);
          port.postMessage({ t: 't', r: data.r });
        }, error => {
          if (pending === true) pending = null;
          flush();
          port.postMessage({ t: 'x', r: data.r, e: String(error) });
        });
      }
      if (data.t === 'z') stallNext = true;
      if (data.t === 'p') {
        timingHistogram.fill(0);
      }
      if (data.t === 'q') report(data.l);
    };
    const commit = (target, bytes, start = 0, length = bytes.byteLength) => {
      if (!target || length % 24 || length > target.d.length) return fail('cmd');
      const count = length / 24;
      for (let i = 0; i < count; i++) if (bytes[start + i * 24] === 22 && !target.p) return fail('Unsupported chord opcode22');
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
        if (op === 10 || op === 9) {
          if (pending) {
            if (deferredCount >= deferred.byteLength / 24) return fail('cmd');
            const to = deferredCount++ * 24;
            for (let j = 0; j < 24; j++) deferred[to + j] = bytes[at + j];
            continue;
          }
          const tick = nextBarTick;
          for (let j = 0; j < 24; j++) target.d[committed * 24 + j] = bytes[at + j];
          putTick(target.d, committed * 24 + 16, tick);
        } else {
          for (let j = 0; j < 24; j++) target.d[committed * 24 + j] = bytes[at + j];
        }
        committed++;
      }
      if (committed) target.x.gosx_audio_cmd_commit(committed);
      if (target === active) {
        if (seekTick !== null) {
          anchorSample = engineSample;
          anchorTick = seekTick;
          nextBarTick = (Math.floor(seekTick / 3840) + 1) * 3840;
          if (starts) { previous = null; fadeLeft = fadeTotal; }
        }
        if (starts) { playing = true; port.postMessage({ t: 's', p: true }); }
        if (stops) { if (CICADA_CAPTURE && capture && capture.recording) capture.stopping = true; playing = false; port.postMessage({ t: 's', p: false }); if (pending && pending !== true) promote(pending); }
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
      next.x.gosx_audio_cmd_commit(2);
      previous = active;
      if (CICADA_CAPTURE && capture) capture.engineEpoch++;
      active = next;
      bpm = next.b;
      engineSample = 0;
      anchorSample = 0;
      anchorTick = tick;
      nextBarTick = tick + 3840;
      fadeLeft = fadeTotal;
      const targetTick = tick + 3840;
      for (let i = 0; i < deferredCount; i++) {
        const from = i * 24;
        for (let j = 0; j < 24; j++) active.d[from + j] = deferred[from + j];
        putTick(active.d, from + 16, targetTick);
      }
      if (deferredCount) active.x.gosx_audio_cmd_commit(deferredCount);
      deferredCount = 0;
    };
    const drain = (target, active) => {
      if (!target) return false;
      const count = target.x.gosx_audio_msg_drain();
      let fault = false;
      let sent = false;
      for (let i = 0; i < count; i++) if (target.m[i * 16] === 7) fault = true;
      if (active && ready && count) {
          let length = count * 16;
          for (let i = 0; i < length; i++) view[i] = target.m[i];
          message.n = length;
          ready = false;
          port.postMessage(message, transfer);
          sent = true;
      }
      if (fault && !sent) {
        faultMessage.a = target.m[2] | target.m[3] << 8;
        port.postMessage(faultMessage);
      }
      return fault;
    };
    const fail = (message) => {
      playing = false;
      active = null;
      port.postMessage({ t: 'e', e: message });
    };
    const report = l => {
      gapLimit = l + durationLimit;
      port.postMessage({ t: 'q', u: underruns,
        dl: durationLimit, gl: gapLimit, m: active?.x.memory.buffer.byteLength || 0,
        d: timingHistogram.slice() });
    };
    const process = (inputs, outputs) => {
      const start = clock();
      const gap = lastStart ? start - lastStart : 0;
      lastStart = start;
      callbacks++;
      const measuredPlaying = playing;
      const channels = outputs[0];
      const left = channels[0], right = channels[1];
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
          active.x.gosx_audio_render(frames);
          if (previous && fadeLeft > 0) previous.x.gosx_audio_render(frames);
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
        const bucket = Math.min(timingHistogram.length - 3, Math.max(0, elapsed * 4 | 0));
        timingHistogram[bucket]++;
        if (elapsed > durationLimit) timingHistogram[timingHistogram.length - 2]++;
        if (gap > gapLimit) timingHistogram[timingHistogram.length - 1]++;
      }
      if (elapsed > durationLimit || gap > gapLimit) underruns++;
      return true;
    };
    module = o.m;
    rate = sampleRate;
    bank = null;
    bankCopy = null;
    active = null;
    previous = null;
    pending = null;
    playing = false;
    deferred = null;
    deferredCount = 0;
    preciseClock = !!globalThis.performance?.now;
    clock = preciseClock ? performance.now.bind(performance) : Date.now;
    durationLimit = 128000 / rate + (preciseClock ? 0.1 : 1);
    gapLimit = (o.l || 0) + durationLimit;
    underruns = 0;
    timingHistogram = new Uint32Array(256);
    lastStart = 0;
    callbacks = 0;
    const buffer = new ArrayBuffer(4096);
    view = new Uint8Array(buffer);
    viewData = new DataView(buffer);
    message = { t: 'm', bytes: buffer, n: 0 };
    transfer = [buffer];
    ready = true;
    faultMessage = { t: 'f', a: 0 };
    swap = new Uint8Array(48);
    swap[0] = 3; swap[1] = 255;
    swap[24] = 1; swap[25] = 255;
    swapView = new DataView(swap.buffer);
    fadeTotal = rate / 200 | 0;
    fadeLeft = 0;
    engineSample = 0;
    anchorSample = 0;
    anchorTick = 0;
    bpm = 120000;
    nextBarTick = 3840;
    stallNext = false;
    create(o.i).then(next => {
      active = next;
      bpm = next.b;
      port.postMessage({ t: 'r', r: o.r, mem: next.x.memory.buffer.byteLength, c: preciseClock });
    }, error => {
      active = null;
      playing = false;
      port.postMessage({ t: 'e', e: String(error) });
    });
    port.onmessage = event => receive(event.data);
    this.process = process;
  }
}
registerProcessor('cicada', CicadaKernel);
