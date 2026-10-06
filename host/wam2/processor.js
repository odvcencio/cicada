// Injected into the official SDK module scope by addFunctionModule.
export default function registerProcessor(moduleId) {
  const scope = globalThis.webAudioModules.getModuleScope(moduleId);
  const { WamProcessor, WamParameterInfo, WamParameter } = scope;
  if (scope.CicadaProcessor) return;

  class CicadaProcessor extends WamProcessor {
    constructor(options) {
      super(options);
      const { kernel, image, manifest, setup } = options.processorOptions;
      this.manifest = manifest;
      this.playing = false;
      this.tempo = manifest.tempo;
      this.command = new Uint8Array(24);
      this.commandView = new DataView(this.command.buffer);
      this.macroIndex = Object.create(null);
      for (const macro of manifest.macros) this.macroIndex[macro.id] = macro;
      this.eventNotice = { event: null };
      this.eventResponse = { id: 0, response: 'add/event' };
      this.faultNotice = { cicadaFault: 0 };
      this.events = new Array(1024).fill(null);
      this.eventIds = new Uint32Array(1024);
      this.eventCount = 0;
      this.ready = WebAssembly.instantiate(kernel).then(instance => {
        const x = instance.exports;
        x._initialize();
        const header = new DataView(image);
        if (header.getUint16(4, true) === 15 && !(x.gosx_audio_capabilities() & 65536)) throw new Error('Kernel does not support this score image');
        const ptr = x.gosx_audio_project_alloc(image.byteLength);
        if (!ptr) throw new Error('Cannot allocate the score image');
        new Uint8Array(x.memory.buffer, ptr, image.byteLength).set(new Uint8Array(image));
        if (x.gosx_audio_init(sampleRate, 128, 2) !== 0) throw new Error('Cannot initialize the score');
        this.x = x;
        this.commands = new Uint8Array(x.memory.buffer, x.gosx_audio_cmd_ptr(), x.gosx_audio_cmd_cap() * 24);
        this.left = new Float32Array(x.memory.buffer, x.gosx_audio_out_ptr(), 128);
        this.right = new Float32Array(x.memory.buffer, x.gosx_audio_out_ptr() + 512, 128);
        this.messages = new Uint8Array(x.memory.buffer, x.gosx_audio_msg_ptr(), 256 * 16);
        this.commands.set(setup);
        if (setup.length) x.gosx_audio_cmd_commit(setup.length / 24);
      });
      // Keep initialization failures handled until the node requests readiness.
      this.ready.catch(() => {});
    }

    async _onMessage(message) {
      const data = message.data;
      if (data.request === 'add/event') {
        if (!this.enqueue(data.content.event, data.id)) {
          this.eventResponse.id = data.id;
          this.port.postMessage(this.eventResponse);
        }
        return;
      }
      if (data.request === 'remove/events') {
        const ids = Array.from(this.eventIds.subarray(0, this.eventCount));
        this.clearEvents();
        this.port.postMessage({ id: data.id, response: data.request, content: ids });
        return;
      }
      if (message.data.request === 'initialize/processor') {
        try { await this.ready; } catch (error) {
          this.port.postMessage({ id: message.data.id, response: message.data.request, content: { error: String(error) } });
          return;
        }
      }
      return super._onMessage(message);
    }

    enqueue(event, id = 0) {
      if (this.eventCount === this.events.length || !Number.isFinite(event.time ?? 0)) return false;
      let at = this.eventCount++;
      const time = event.time || 0;
      while (at > 0 && (this.events[at-1].time || 0) > time) {
        this.events[at] = this.events[at-1];
        this.eventIds[at] = this.eventIds[at-1];
        at--;
      }
      this.events[at] = event;
      this.eventIds[at] = id;
      return true;
    }

    scheduleEvents(...events) { for (const event of events) this.enqueue(event); }
    clearEvents() { this.events.fill(null); this.eventIds.fill(0); this.eventCount = 0; }

    _generateWamParameterInfo() {
      const info = Object.create(null);
      for (const macro of this.manifest.macros) {
        info[macro.id] = new WamParameterInfo(macro.id, {
          label: macro.id, type: 'float', defaultValue: macro.defaultValue,
          minValue: 0, maxValue: 1, units: '',
        });
      }
      return info;
    }

    _initialize() {
      this._parameterInfo = this._generateWamParameterInfo();
      this._parameterState = Object.create(null);
      for (const macro of this.manifest.macros) this._parameterState[macro.id] = new WamParameter(this._parameterInfo[macro.id]);
    }

    _getParameterValues(normalized, ids = []) {
      if (!ids.length) ids = Object.keys(this._parameterState);
      const values = Object.create(null);
      for (const id of ids) {
        const p = this._parameterState[id];
        if (p) values[id] = { id, value: normalized ? p.normalizedValue : p.value, normalized };
      }
      return values;
    }

    send(op, track = 255, index = 0, arg0 = 0, arg1 = 0, float = false) {
      this.command.fill(0);
      const v = this.commandView;
      v.setUint8(0, op); v.setUint8(1, track); v.setUint16(2, index, true);
      if (float) v.setFloat32(4, arg0, true); else v.setUint32(4, arg0, true);
      v.setUint32(8, arg1, true);
      this.commands.set(this.command);
      this.x.gosx_audio_cmd_commit(1);
    }

    _setParameterValue(data, interpolate) {
      const macro = this.macroIndex[data.id];
      if (!macro || !Number.isFinite(data.value) || data.value < 0 || data.value > 1) return;
      this._parameterState[data.id].value = data.value;
      this.send(18, 255, macro.index, data.value, interpolate ? Math.round(macro.smoothMs * sampleRate / 1000) : 0, true);
    }

    _onMidi(data) {
      const bytes = data.bytes;
      if (!bytes || bytes.length < 2) return;
      const status = bytes[0] & 240, channel = bytes[0] & 15, note = bytes[1], velocity = bytes[2];
      if (note > 127 || note < 0) return;
      if (this.manifest.midiPiano && (status === 144 || status === 128) && (note < 21 || note > 108)) return;
      const track = this.manifest.midiTrack;
      const id = this.manifest.midiDrums ? this.manifest.drumNotes.indexOf(note) : this.manifest.midiPiano ? note : 1 + channel * 128 + note;
      if (id < 0) return;
      if (status === 144 && velocity > 0 && velocity < 128) this.send(12, track, id, note | velocity << 8);
      else if (status === 128 || status === 144 && velocity === 0) this.send(13, track, id);
      else if (status === 176 && (note === 120 || note === 123)) this.releaseNotes();
    }

    releaseNotes() {
      if (this.manifest.midiDrums) {
        for (let i = 0; i < this.manifest.drumNotes.length; i++) this.send(13, this.manifest.midiTrack, i);
      } else this.send(13, this.manifest.midiTrack, 65535);
    }

    _onTransport(data) {
      if (Number.isFinite(data.tempo) && data.tempo >= 20 && data.tempo <= 300) {
        this.tempo = data.tempo;
        this.send(4, 255, 0, Math.round(data.tempo * 1000));
      }
      if (typeof data.playing === 'boolean' && data.playing !== this.playing) {
        this.playing = data.playing;
        this.send(this.playing ? 1 : 2);
      }
    }

    _getState() {
      return { version: 1, scoreId: this.manifest.scoreId, playing: this.playing, tempo: this.tempo, parameterValues: this._getParameterValues(false) };
    }

    _setState(state) {
      this.clearEvents();
      this.send(2);
      this.releaseNotes();
      this.send(3);
      this.playing = false;
      super._setState(state);
      this._onTransport(state);
    }

    render(start, end, left, right) {
      while (start < end) {
        const frames = Math.min(128, end - start);
        this.x.gosx_audio_render(frames);
        for (let i = 0; i < frames; i++) { left[start+i] = this.left[i]; right[start+i] = this.right[i]; }
        const count = this.x.gosx_audio_msg_drain();
        for (let i = 0; i < count; i++) {
          const at = i * 16;
          if (this.messages[at] === 7) {
            this.faultNotice.cicadaFault = this.messages[at+2] | this.messages[at+3] << 8;
            this.port.postMessage(this.faultNotice);
            this.playing = false;
          }
        }
        start += frames;
      }
    }

    // Avoid the SDK's per-quantum slice arrays and parameter-key enumeration.
    // Events and commands split rendering at the sample where they occur.
    process(inputs, outputs) {
      if (this._destroyed) return false;
      const left = outputs[0]?.[0], right = outputs[0]?.[1];
      if (!left || !right) return true;
      left.fill(0); right.fill(0);
      if (!this._initialized) return true;
      let offset = 0;
      while (this.eventCount) {
        const event = this.events[0];
        const at = Math.max(offset, Math.round(((event.time || 0) - currentTime) * sampleRate));
        if (at >= left.length) break;
        this.render(offset, at, left, right);
        this._processEvent(event);
        if (this.eventIds[0]) {
          this.eventResponse.id = this.eventIds[0];
          this.port.postMessage(this.eventResponse);
        } else {
          this.eventNotice.event = event;
          this.port.postMessage(this.eventNotice);
        }
        this.eventCount--;
        for (let i = 0; i < this.eventCount; i++) {
          this.events[i] = this.events[i+1];
          this.eventIds[i] = this.eventIds[i+1];
        }
        this.events[this.eventCount] = null;
        offset = at;
      }
      this.render(offset, left.length, left, right);
      return true;
    }
  }
  scope.CicadaProcessor = CicadaProcessor;
  registerProcessorGlobal(moduleId, CicadaProcessor);

  function registerProcessorGlobal(id, processor) { globalThis.registerProcessor(id, processor); }
}
