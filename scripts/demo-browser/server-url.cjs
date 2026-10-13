// Waits for a child server to print its loopback URL. Output is buffered across
// chunks, and a URL counts only once whitespace follows it, so a URL split
// between two reads is never matched early or with a truncated port.
'use strict';
const URL_PATTERN = /http:\/\/127\.0\.0\.1:\d+(?=\s)/;
const TAIL_BYTES = 8000;

function findServerURL(output) {
  const match = URL_PATTERN.exec(output);
  return match ? match[0] : undefined;
}

function tail(text) {
  return text.length > TAIL_BYTES ? '...' + text.slice(-TAIL_BYTES) : text;
}

// Resolves with the URL. On timeout or early exit, rejects with an error that
// carries the tail of the server's stdout and stderr. `onFailure({stdout, stderr})`
// receives the full captured text when the wait fails.
function waitForServerURL(server, {timeoutMs = 15000, label = 'Demo server', onFailure} = {}) {
  return new Promise((resolve, reject) => {
    let stdout = '', stderr = '', done = false;
    const finish = (error, url) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      if (!error) return resolve(url);
      if (onFailure) onFailure({stdout, stderr});
      error.stdout = stdout;
      error.stderr = stderr;
      error.message += `\n--- stdout (tail) ---\n${tail(stdout)}\n--- stderr (tail) ---\n${tail(stderr)}`;
      reject(error);
    };
    const timer = setTimeout(() => finish(new Error(`${label} start timed out after ${timeoutMs} ms`)), timeoutMs);
    server.stdout.setEncoding('utf8');
    server.stderr.setEncoding('utf8');
    server.stdout.on('data', chunk => {
      stdout += chunk;
      const url = findServerURL(stdout);
      if (url) finish(null, url);
    });
    server.stderr.on('data', chunk => { stderr += chunk; process.stderr.write(chunk); });
    server.once('exit', (code, signal) => finish(new Error(`${label} exited ${code ?? signal} before printing its URL`)));
  });
}

module.exports = {findServerURL, waitForServerURL, tail};
