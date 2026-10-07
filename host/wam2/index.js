import { WebAudioModule, WamNode, addFunctionModule } from './sdk.js';
import registerProcessor from './processor.js';
import { createGui } from './gui.js';

const read = async (base, name, json = true) => {
  const response = await fetch(new URL(name, base));
  if (!response.ok) throw new Error(`Cannot load ${name}: ${response.status}`);
  return json ? response.json() : response.arrayBuffer();
};

export class CicadaWamNode extends WamNode {
  constructor(module, options, manifest) {
    super(module, options);
    this.manifest = manifest;
    this._supportedEventTypes = new Set(['wam-automation', 'wam-midi', 'wam-transport', 'cicada-error']);
  }

  _onMessage(message) {
    if (message.data.cicadaFault !== undefined) {
      this.dispatchEvent(new CustomEvent('cicada-error', { detail: `Audio kernel fault ${message.data.cicadaFault}` }));
      return;
    }
    super._onMessage(message);
  }

  validateValues(values) {
    for (const [id, data] of Object.entries(values)) {
      if (!this.manifest.macros.some(macro => macro.id === id) || data.id !== id || !Number.isFinite(data.value) || data.value < 0 || data.value > 1) {
        throw new RangeError(`Invalid macro value: ${id}`);
      }
    }
  }

  async setParameterValues(values) {
    this.validateValues(values);
    return super.setParameterValues(values);
  }

  async setState(state) {
    if (state?.version !== 1 || state.scoreId !== this.manifest.scoreId || typeof state.playing !== 'boolean' || !Number.isFinite(state.tempo) || state.tempo < 20 || state.tempo > 300 || !state.parameterValues) {
      throw new RangeError('State belongs to a different score or is invalid');
    }
    this.validateValues(state.parameterValues);
    await this.clearEvents();
    return super.setState(state);
  }

  scheduleEvents(...events) {
    for (const event of events) {
      if (event.type === 'wam-automation') this.validateValues({ [event.data.id]: event.data });
      if (event.time !== undefined && !Number.isFinite(event.time)) throw new RangeError('Invalid event time');
    }
    super.scheduleEvents(...events);
  }
}

export default class CicadaWam extends WebAudioModule {
  async initialize(state) {
    if (this.initialized) {
      if (state !== undefined) await this.audioNode.setState(state);
      return this;
    }
    const base = import.meta.url;
    const [descriptor, manifest, bytes] = await Promise.all([
      read(base, 'descriptor.json'), read(base, 'score.json'), read(base, 'kernel.wasm', false),
    ]);
    Object.assign(this._descriptor, descriptor);
    const rate = this.audioContext.sampleRate;
    if (!manifest.images[rate]) throw new RangeError('This instrument supports 44100, 48000, and 96000 Hz');
    const [image, kernel] = await Promise.all([
      read(base, manifest.images[rate], false), WebAssembly.compile(bytes),
    ]);
    const setup = Uint8Array.from(atob(manifest.setup[rate] || ''), c => c.charCodeAt(0));
    await WamNode.addModules(this.audioContext, this.moduleId);
    await addFunctionModule(this.audioContext.audioWorklet, registerProcessor, this.moduleId);
    const node = new CicadaWamNode(this, {
      numberOfInputs: 0, numberOfOutputs: 1, outputChannelCount: [2],
      processorOptions: { kernel, image, manifest, setup, useSab: false },
    }, manifest);
    try {
      let timeout;
      const result = await Promise.race([
        node._initialize().finally(() => clearTimeout(timeout)),
        new Promise((resolve, reject) => { timeout = setTimeout(() => reject(new Error('WAM processor initialization timed out')), 10000); }),
      ]);
      if (result?.error) throw new Error(result.error);
      if (state !== undefined) await node.setState(state);
      this.audioNode = node;
      this.initialized = true;
      return this;
    } catch (error) {
      node.destroy();
      throw error;
    }
  }

  async createAudioNode(state) {
    if (!this.initialized) await this.initialize(state);
    return this.audioNode;
  }

  async createGui() { return createGui(this); }
  destroyGui(gui) { gui?.dispose?.(); gui?.remove(); }
}
