'use strict';
const {spawn} = require('node:child_process');
const readline = require('node:readline');
const path = require('node:path');

// Keep sampling and policy in Go so both browser runners use one implementation.
async function startSoakTiming() {
  const child = spawn('nice', ['-n', '10', 'go', 'run', './scripts/browser-soak-timing'], {
    cwd: path.resolve(__dirname, '../..'), env: {...process.env, GOWORK: 'off'},
    stdio: ['pipe', 'pipe', 'inherit']
  });
  let processError;
  child.on('error', error => { processError = error; });
  child.stdin.on('error', error => { processError = error; });
  const exited = new Promise(resolve => child.once('close', resolve));
  const lines = readline.createInterface({input: child.stdout});
  const iterator = lines[Symbol.asyncIterator]();
  const read = async () => {
    const {value, done} = await iterator.next();
    if (done) throw processError || new Error('Soak timing helper ended before returning its verdict');
    return JSON.parse(value);
  };
  const close = async () => {
    child.stdin.end();
    await exited;
    lines.close();
  };
  try {
    const ready = await read();
    if (!ready.ready) throw new Error('Soak host sampler did not become ready');
  } catch (error) {
    await close();
    throw error;
  }
  return {
    async finish(report) {
      child.stdin.end(JSON.stringify(report) + '\n');
      const result = await read();
      const status = await exited;
      if (status !== 0) throw processError || new Error(`Soak timing helper exited ${status}`);
      return result;
    },
    close
  };
}

module.exports = {startSoakTiming};
