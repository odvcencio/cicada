export interface Command { Op: number; Track: number; Index?: number; Arg0?: number; Arg1?: number; Pad?: number; Tick?: bigint | number; }
export interface Message { Kind: number; Track: number; A: number; B: number; Tick: bigint; }
export type Landing = '' | 'now' | 'beat' | 'bar' | '2bars' | '4bars' | 'phrase';
export interface Surface {
  version: 1; sample_rate: 44100 | 48000 | 96000; tracks: number; land: Landing; phrase_bars: number;
  macros: { name: string; id: number; smooth_frames: number }[];
  states: { name: string; id: number; scene: number }[];
  stingers: { name: string; track: number; slot: number; quantize: Landing; crossfade_frames: number }[];
  transitions: { from: string; to: string; quantize: Landing; crossfade_frames: number }[];
  setup: Command[];
}
export function encodeCommand(command: Command): Uint8Array;
export function decodeMessages(bytes: Uint8Array): Message[];
export function workletSender(port: Pick<MessagePort, 'postMessage'>): (bytes: Uint8Array) => void;
export class GameDirector {
  constructor(surface: Surface, send: (bytes: Uint8Array) => void);
  setup(): void;
  setState(name: string, tick?: bigint | number): void;
  setMacro(name: string, value: number, tick?: bigint | number): void;
  triggerStinger(name: string, tick?: bigint | number): void;
  handle(message: Message): void;
  readonly state: string;
  readonly layerMask: number;
}

export const CapabilitySpatial: number;
export class SpatialAudio {
  constructor(send: (bytes: Uint8Array) => void, tracks: number, capabilities: number);
  trackPosition(track: number, x: number, y: number, z: number, tick?: bigint | number): void;
  trackStereo(track: number, tick?: bigint | number): void;
  listenerPosition(x: number, y: number, z: number, tick?: bigint | number): void;
  listenerRotation(yaw: number, pitch: number, roll: number, tick?: bigint | number): void;
}
