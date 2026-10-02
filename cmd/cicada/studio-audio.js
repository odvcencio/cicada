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
    const loudness = value.loudness;
    const loudnessKeys = ['momentary', 'short_term', 'integrated', 'range', 'true_peak', 'sample_peak'];
    if (loudness !== undefined && (!loudness || !loudnessKeys.every(key => loudness[key] === null || Number.isFinite(loudness[key])) || !Number.isSafeInteger(loudness.dropped_blocks) || loudness.dropped_blocks < 0)) return null;
    return {type: 'meters', tick: value.tick, tracks: readMap(value.tracks), returns: readMap(value.returns), buses: readMap(value.buses), master: {...master}, loudness: loudness ? {...loudness} : null};
  }

  function createCicadaAudio(options = {}) {
    const root = options.window || (typeof window !== 'undefined' ? window : null);
    const Socket = options.WebSocket || (root && root.WebSocket);
    const fetcher = options.fetch || (root && root.fetch && root.fetch.bind(root));
    const raf = options.requestAnimationFrame || (root && root.requestAnimationFrame && root.requestAnimationFrame.bind(root)) || (callback => setTimeout(callback, 0));
    const listeners = {meters: new Set(), errors: new Set()};
    const liveNotes = new Map();
    let reconnectTimer = null;
    const later = options.setTimeout || setTimeout;
    const cancelLater = options.clearTimeout || clearTimeout;
    const isBrowser = () => root?.document?.getElementById('audio-mode')?.value === 'browser';
    const browser = () => {
      if (!isBrowser()) return null;
      if (!root.cicadaBrowserPerformance) throw new Error('Bind the qualified browsercontrol adapter before using browser live inputs');
      return root.cicadaBrowserPerformance;
    };
    function inputPanic() { if (root?.dispatchEvent && root.CustomEvent) root.dispatchEvent(new root.CustomEvent('cicada:inputpanic')); }
    function panic() {
      for (const held of liveNotes.values()) if (socket?.readyState === 1) socket.send(JSON.stringify({type: 'note', ...held, velocity: 0, on: false}));
      liveNotes.clear();
      try { root?.cicadaBrowserPerformance?.panic?.(); } catch (error) { report(error); }
    }
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
      socket.addEventListener('close', () => {
        liveNotes.clear(); inputPanic();
        if (!closed) reconnectTimer = later(connect, 1000);
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
    for (const event of ['blur', 'pagehide']) root?.addEventListener?.(event, () => { panic(); inputPanic(); });
    root?.document?.addEventListener?.('visibilitychange', () => { if (root.document.hidden) { panic(); inputPanic(); } });
    root?.document?.getElementById('audio-mode')?.addEventListener('change', () => { panic(); inputPanic(); });

    return {
      backend: 'native',
      params() {
        if (isBrowser()) { try { return Promise.resolve(browser().params()); } catch (error) { return Promise.reject(error); } }
        if (!fetcher) return Promise.reject(new Error('fetch is unavailable'));
        return fetcher('/api/params').then(response => {
          if (!response.ok) throw new Error(`parameter request failed: ${response.status}`);
          return response.json();
        });
      },
      setParam(address, value) {
        if (typeof address !== 'string' || !address) throw new TypeError('parameter address is required');
        if (browser()) return browser().setParam(address, value);
        queuedParams.set(address, value);
        scheduleParams();
      },
      setMute(track, on) { if (browser()) return browser().setMute(track, on); send({type: 'mute', track, on: !!on}); },
      setSolo(track, on) { if (browser()) return browser().setSolo(track, on); send({type: 'solo', track, on: !!on}); },
      noteOn(track, note, velocity, noteId) {
        if (typeof track !== 'string' || !track) throw new TypeError('note track is required');
        if (!Number.isInteger(note) || note < 0 || note > 127) throw new RangeError('note must be an integer from 0 to 127');
        if (!Number.isInteger(velocity) || velocity < 0 || velocity > 127) throw new RangeError('velocity must be an integer from 0 to 127');
        if (browser()) return browser().noteOn(track, note, velocity, noteId || `${track}:${note}`);
        if (!socket || socket.readyState !== 1 || closed) return false;
        const held = {track, note, ...(noteId ? {noteId} : {})};
        liveNotes.set(noteId || `${track}:${note}`, held);
        socket.send(JSON.stringify({type: 'note', ...held, velocity, on: true}));
        return true;
      },
      noteOff(track, note, noteId) {
        if (typeof track !== 'string' || !track) throw new TypeError('note track is required');
        if (!Number.isInteger(note) || note < 0 || note > 127) throw new RangeError('note must be an integer from 0 to 127');
        if (browser()) return browser().noteOff(track, note, noteId || `${track}:${note}`);
        const key = noteId || `${track}:${note}`, held = liveNotes.get(key);
        if (!held) return false;
        liveNotes.delete(key);
        if (socket?.readyState === 1) socket.send(JSON.stringify({type: 'note', ...held, velocity: 0, on: false}));
        return true;
      },
      resetLoudness() { send({type: 'loudness-reset'}); },
      onMeters(callback) { listeners.meters.add(callback); return () => listeners.meters.delete(callback); },
      onError(callback) { listeners.errors.add(callback); return () => listeners.errors.delete(callback); },
      panic, // Owner release, including normal synthesis/effect tails.
      async silence() {
        panic(); inputPanic();
        if (browser()) return browser().silence();
        if (!fetcher) throw new Error('fetch is unavailable');
        const response = await fetcher('/api/transport', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action:'stop',revision:root?.document?.body?.dataset?.revision})});
        if (!response.ok) throw new Error('Could not close native playback');
        return response.json();
      },
      close() { panic(); inputPanic(); closed = true; if (reconnectTimer !== null) cancelLater(reconnectTimer); if (socket) socket.close(); }
    };
  }

  const api = {createCicadaAudio, decodeMeters};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof window !== 'undefined') window.cicadaAudio = createCicadaAudio();
})();
