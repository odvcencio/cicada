// NDJSON bridge used by the native/WASM M5 gate. It exercises the shipped API.
import { createInterface } from 'node:readline';
import { GameDirector, decodeMessages } from './index.js';
let client, commands;
for await (const line of createInterface({ input: process.stdin })) {
  const input = JSON.parse(line);
  commands = [];
  if (input.surface) client = new GameDirector(input.surface, bytes => commands.push(Buffer.from(bytes).toString('base64')));
  if (input.messages) for (const message of decodeMessages(Buffer.from(input.messages, 'base64'))) client.handle(message);
  for (const event of input.events ?? []) {
    const tick = BigInt(event.tick ?? '0');
    if (event.kind === 'setup') client.setup();
    else if (event.kind === 'state') client.setState(event.name, tick);
    else if (event.kind === 'macro') client.setMacro(event.name, event.value, tick);
    else if (event.kind === 'stinger') client.triggerStinger(event.name, tick);
    else throw new Error(`Unknown event ${event.kind}`);
  }
  process.stdout.write(JSON.stringify({ commands, state: client.state, layers: client.layerMask }) + '\n');
}
