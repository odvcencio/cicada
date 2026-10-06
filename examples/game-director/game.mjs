import { GameDirector, decodeMessages, encodeCommand, workletSender } from '../../sdk/js/index.js';

// Pass the compiled kernel bytes, kernel project image, and JSON manifest.
// Call from a user gesture so the browser can resume its AudioContext.
export async function startGameMusic(context, wasmBytes, image, surface, processorURL) {
  if (context.sampleRate !== surface.sample_rate) throw new Error('Manifest sample rate must match AudioContext');
  await context.audioWorklet.addModule(processorURL);
  const module = await WebAssembly.compile(wasmBytes);
  const node = new AudioWorkletNode(context, 'cicada', {
    numberOfInputs: 0, numberOfOutputs: 1, outputChannelCount: [2],
    processorOptions: { m: module, i: image, r: 1 }
  });
  const send = workletSender(node.port), game = new GameDirector(surface, send);
  const ready = new Promise((resolve, reject) => {
    node.port.onmessage = ({ data }) => {
      if (data.t === 'r') resolve();
      if (data.t === 'e' || data.t === 'f') reject(new Error(`Music engine failed: ${data.e ?? data.a}`));
      if (data.t === 'm') {
        for (const message of decodeMessages(new Uint8Array(data.bytes, 0, data.n))) {
          game.handle(message);
          if ([6, 7, 8].includes(message.Kind)) console.error('Music control event', message);
        }
        // Return the worklet's reusable message buffer after reading it.
        node.port.postMessage({ t: 'b', bytes: data.bytes }, [data.bytes]);
      }
    };
  });
  await ready;
  node.connect(context.destination);
  game.setup(); game.setState('explore');
  send(encodeCommand({ Op: 1, Track: 255 }));
  await context.resume();
  return {
    enterCombat: () => { game.setMacro('intensity', 0.9); game.setState('combat'); },
    leaveCombat: () => { game.setMacro('intensity', 0.1); game.setState('explore'); },
    collectPickup: () => game.triggerStinger('pickup'),
    stop: () => { send(encodeCommand({ Op: 2, Track: 255 })); node.disconnect(); node.port.close(); },
    game
  };
}
