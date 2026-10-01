// Browser adapter for host/capture.Block. PascalCase fields deliberately match
// encoding/json's existing Block contract; timestamps stay invalid because Web
// Audio does not expose a microphone first-frame timestamp. No RTT correction.
class CicadaCapture {
  constructor(data, rate, control) {
    this.port = data.port;
    this.control = control;
    this.rate = rate;
    this.channels = data.channels;
    this.maxFrames = 2048;
    this.epoch = data.epoch;
    this.recording = false;
    this.stopping = false;
    this.remaining = 0;
    this.countInCursor = 0;
    this.gap = 0;
    this.deviceFrame = 0;
    this.engineEpoch = 1;
    this.lastEpoch = 1;
    this.pool = new Array(32);
    this.count = 32;
    for (let i = 0; i < 32; i++) {
      const bytes = new ArrayBuffer(this.maxFrames * this.channels * 4);
      this.pool[i] = {t:'pcm', bytes, pcm:new Float32Array(bytes), transfer:[bytes], timing:{
        EngineEpoch:1, DeviceEpoch:this.epoch, EngineFrame:0, DeviceFrame:0,
        InputPosition:{Position:0,Frequency:rate,Valid:false}, OutputPosition:{Position:0,Frequency:rate,Valid:false},
        InputTime:{Nano:0,Valid:false,Domain:0,Reference:0}, OutputTime:{Nano:0,Valid:false,Domain:0,Reference:0},
        SampleRate:rate, Period:0, Frames:0, InputLatencyNano:data.inputLatencyNano || 0,
        OutputLatencyNano:data.outputLatencyNano || 0, InputLatencyValid:!!data.inputLatencyValid,
        OutputLatencyValid:!!data.outputLatencyValid, Calibration:{DeviceEpoch:this.epoch,RemainingFrames:0,Valid:false},
        Layout:this.channels, Flags:0, DeviceDropouts:0, GapFrames:0
      }};
    }
    this.end = {t:'end', gapFrames:0};
    this.ack = {t:'capture-stopped'};
    this.started = {t:'capture-started'};
    this.port.onmessage = event => {
      // Recycling is on the control path, never process(). The pool's storage
      // and envelopes stay bounded even when a writer stalls.
      const packet = event.data;
      if (packet.t === 'recycle' && this.count < 32) {
        packet.t = 'pcm';
        this.pool[this.count++] = packet;
      }
    };
  }
  receive(data) {
    if (data.op === 'begin' && !this.recording) {
      this.remaining = data.countInFrames;
      this.countInCursor = 0;
      this.recording = true;
      this.control.postMessage(this.started);
    }
    if (data.op === 'stop') this.stopping = true;
  }
  process(inputs, frames, engineFrame, bpm, playing, left, right) {
    const deviceFrame = this.deviceFrame;
    this.deviceFrame += frames;
    if (this.stopping) {
      this.stopping = false;
      this.recording = false;
      this.end.gapFrames = this.gap;
      this.gap = 0;
      this.port.postMessage(this.end);
      this.control.postMessage(this.ack);
    }
    if (!this.recording) return 0;
    const lead = Math.min(frames, this.remaining);
    const input = inputs[0];
    if (!input || input.length !== this.channels || frames > this.maxFrames || input[0].length !== frames || (this.channels === 2 && input[1].length !== frames)) {
      this.gap += frames;
    } else if (!this.count) {
      this.gap += frames;
    } else {
      const packet = this.pool[--this.count];
      this.pool[this.count] = null;
      const timing = packet.timing;
      timing.EngineEpoch = this.engineEpoch;
      timing.EngineFrame = engineFrame - this.remaining;
      timing.DeviceFrame = deviceFrame;
      timing.Period = timing.Frames = frames;
      timing.GapFrames = this.gap;
      timing.Flags = (this.gap ? 2 : 0) | (this.engineEpoch !== this.lastEpoch ? 1 : 0);
      this.lastEpoch = this.engineEpoch;
      this.gap = 0;
      // Interleaved raw PCM, including count-in preroll. Never monitor input.
      for (let f = 0; f < frames; f++) {
        for (let c = 0; c < this.channels; c++) packet.pcm[f * this.channels + c] = input[c][f];
      }
      this.port.postMessage(packet, packet.transfer);
    }
    if (left && right && playing && lead) {
      for (let beat = 0; beat < 4; beat++) {
        const start = Math.ceil(beat * 60000 * this.rate / bpm) - this.countInCursor;
        for (let i = Math.max(0, start); i < Math.min(lead, start + 32); i++) {
          const level = ((i - start) & 1 ? -1 : 1) * (beat === 0 ? .2 : .12);
          left[i] = right[i] = level;
        }
      }
    }
    this.countInCursor += lead;
    this.remaining -= lead;
    return lead;
  }
}
globalThis.CicadaCapture = CicadaCapture;
