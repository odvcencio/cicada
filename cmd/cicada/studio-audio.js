(() => {
  'use strict';

  function decodeMeters(value) {
    if (!value || value.type !== 'meters' || !Number.isFinite(value.tick)) return null;
    const readPair = pair => pair && Number.isFinite(pair.peak) && Number.isFinite(pair.rms)
      ? {peak: pair.peak, rms: pair.rms} : null;
    const readMap = map => {
      const result = {};
      if (!map || typeof map !== 'object' || Array.isArray(map)) return result;
      for (const [key, pair] of Object.entries(map)) {
        const parsed = readPair(pair);
        if (parsed) result[key] = parsed;
      }
      return result;
    };
    const master = value.master;
    if (!master || !['pre_peak', 'peak', 'rms', 'comp_gr', 'limiter_gr'].every(key => Number.isFinite(master[key])) || typeof master.over !== 'boolean') return null;
    return {type: 'meters', tick: value.tick, tracks: readMap(value.tracks), returns: readMap(value.returns), buses: readMap(value.buses), master: {...master}};
  }

  function createCicadaAudio(options = {}) {
    const root = options.window || (typeof window !== 'undefined' ? window : null);
    const Socket = options.WebSocket || (root && root.WebSocket);
    const fetcher = options.fetch || (root && root.fetch && root.fetch.bind(root));
    const raf = options.requestAnimationFrame || (root && root.requestAnimationFrame && root.requestAnimationFrame.bind(root)) || (callback => setTimeout(callback, 0));
    const listeners = {meters: new Set(), errors: new Set()};
    const queuedParams = new Map();
    const pendingMessages = [];
    let meterFrame = null;
    let meterScheduled = false;
    let paramsScheduled = false;
    let socket = null;
    let closed = false;

    function report(error) {
      for (const listener of listeners.errors) listener(error);
    }
    function connect() {
      if (!Socket || !root || closed) return;
      const scheme = root.location && root.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const host = root.location ? root.location.host : '';
      socket = new Socket(`${scheme}//${host}/api/audio/ws`);
      socket.addEventListener('open', flushMessages);
      socket.addEventListener('message', event => {
        let message;
        try { message = JSON.parse(event.data); }
        catch { report({type: 'error', code: 'CICADA-PARAM', message: 'invalid server message', address: ''}); return; }
        if (message.type === 'meters') {
          const parsed = decodeMeters(message);
          if (!parsed) { report({type: 'error', code: 'CICADA-PARAM', message: 'invalid meter message', address: ''}); return; }
          meterFrame = parsed;
          if (!meterScheduled) {
            meterScheduled = true;
            raf(() => {
              meterScheduled = false;
              const latest = meterFrame;
              meterFrame = null;
              if (latest) for (const listener of listeners.meters) listener(latest);
            });
          }
        } else if (message.type === 'error') report(message);
      });
      socket.addEventListener('error', () => report({type: 'error', code: 'CICADA-PARAM', message: 'audio socket failed', address: ''}));
    }
    function send(message) {
      if (socket && socket.readyState === 1) socket.send(JSON.stringify(message));
      else pendingMessages.push(message);
    }
    function flushMessages() {
      while (pendingMessages.length && socket && socket.readyState === 1) socket.send(JSON.stringify(pendingMessages.shift()));
      for (const [address, value] of queuedParams) socket && socket.readyState === 1 && socket.send(JSON.stringify({type: 'param', address, value}));
      if (socket && socket.readyState === 1) queuedParams.clear();
    }
    function scheduleParams() {
      if (paramsScheduled) return;
      paramsScheduled = true;
      raf(() => {
        paramsScheduled = false;
        if (socket && socket.readyState === 1) flushMessages();
      });
    }
    if (options.connect !== false) connect();

    return {
      backend: 'native',
      params() {
        if (!fetcher) return Promise.reject(new Error('fetch is unavailable'));
        return fetcher('/api/params').then(response => {
          if (!response.ok) throw new Error(`parameter request failed: ${response.status}`);
          return response.json();
        });
      },
      setParam(address, value) {
        if (typeof address !== 'string' || !address) throw new TypeError('parameter address is required');
        queuedParams.set(address, value);
        scheduleParams();
      },
      setMute(track, on) { send({type: 'mute', track, on: !!on}); },
      setSolo(track, on) { send({type: 'solo', track, on: !!on}); },
      onMeters(callback) { listeners.meters.add(callback); return () => listeners.meters.delete(callback); },
      onError(callback) { listeners.errors.add(callback); return () => listeners.errors.delete(callback); },
      close() { closed = true; if (socket) socket.close(); }
    };
  }

  const api = {createCicadaAudio, decodeMeters};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window !== 'undefined') window.cicadaAudio = createCicadaAudio();
})();
