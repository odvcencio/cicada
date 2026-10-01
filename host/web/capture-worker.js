/* PCM persistence runs only in a worker. PCM is float32 little endian,
 * interleaved; journal records use host/capture.RecordedBlock / Block fields.
 * PCM flush precedes each journal commit. Recovery ignores uncommitted tails.
 */
(function(root) {
  'use strict';
  const encoder = new TextEncoder();
  function placement(b) {
    // Browser timestamps are unavailable; match capture.Place's latency branch.
    const mapped = b.EngineFrame - (b.InputLatencyValid && b.OutputLatencyValid ? Math.round((b.InputLatencyNano + b.OutputLatencyNano) * b.SampleRate / 1e9) : 0);
    const confidence = b.InputLatencyValid && b.OutputLatencyValid ? 1 : 0;
    const calibrated = !!(confidence && b.Calibration.Valid && b.DeviceEpoch && b.Calibration.DeviceEpoch === b.DeviceEpoch);
    const correction = calibrated ? -b.Calibration.RemainingFrames : 0;
    return {rawEngineFrame:b.EngineFrame,mappedEngineFrame:mapped,correctionFrames:correction,engineFrame:mapped+correction,timingConfidence:confidence,calibrated};
  }
  function validate(b, bytes, channels) {
    if (!b || ![1,2].includes(channels) || !Number.isSafeInteger(b.Frames) || b.Frames < 1 || b.Frames > 2048 || b.Layout !== channels || !Number.isSafeInteger(b.GapFrames) || b.GapFrames < 0 || bytes.byteLength < b.Frames * channels * 4) throw new Error('Invalid capture block');
  }
  function recoverJournal(text, size, channels, rate) {
    const blocks = [];
    let offset = 0, rawFrame = 0, incomplete = true;
    for (const line of text.split('\n').slice(0,-1)) {
      if (!line) continue;
      let r;
      try { r = JSON.parse(line); } catch (_) { break; }
      if (r.type === 'end') { incomplete = !!r.incomplete; rawFrame += r.gapFrames; break; }
      if (r.type !== 'block' || r.offset !== offset || r.length !== r.timing?.Frames * channels * 4 || r.timing.SampleRate !== rate || r.timing.Layout !== channels || r.offset + r.length > size || r.rawFrame !== rawFrame + r.timing.GapFrames) break;
      offset += r.length;
      rawFrame = r.rawFrame + r.timing.Frames;
      blocks.push(r);
    }
    return {blocks, bytes:offset, rawFrames:rawFrame, incomplete:incomplete || blocks.some(b => b.timing.Flags || b.timing.GapFrames)};
  }
  class OPFSStore {
    static async open(storage, id, metadata, existing = false) {
      const rootDir = await storage.getDirectory();
      const takes = await rootDir.getDirectoryHandle('cicada-takes', {create:true});
      const dir = await takes.getDirectoryHandle(id, {create:!existing});
      const meta = await dir.getFileHandle('metadata.json', {create:!existing});
      if (!existing) { const w = await meta.createWritable(); await w.write(JSON.stringify(metadata)); await w.close(); }
      else metadata = JSON.parse(await (await meta.getFile()).text());
      const pcm = await dir.getFileHandle('pcm.f32', {create:!existing});
      const journal = await dir.getFileHandle('journal.jsonl', {create:!existing});
      if (!existing && !pcm.createSyncAccessHandle) throw new Error('OPFS synchronous writes unavailable');
      const store = new OPFSStore();
      Object.assign(store, {mode:'opfs',pcm,journal,metadata,offset:0,journalOffset:0});
      if (!existing) {
        store.pcmAccess = await pcm.createSyncAccessHandle();
        try { store.journalAccess = await journal.createSyncAccessHandle(); }
        catch (error) { store.pcmAccess.close(); throw error; }
      }
      return store;
    }
    writeAll(handle, bytes, at) {
      let done = 0;
      while (done < bytes.length) {
        const n = handle.write(bytes.subarray(done), {at:at+done});
        if (!n) throw new Error('PCM storage write made no progress');
        done += n;
      }
      handle.flush();
    }
    async append(record, bytes) {
      record.offset = this.offset;
      record.length = bytes.byteLength;
      this.writeAll(this.pcmAccess, bytes, this.offset);
      await this.commit(record);
      this.offset += bytes.length;
    }
    async commit(record) {
      const bytes = encoder.encode(JSON.stringify(record)+'\n');
      this.writeAll(this.journalAccess, bytes, this.journalOffset);
      this.journalOffset += bytes.length;
    }
    async finish(summary) { try { await this.commit({type:'end',...summary}); } finally { this.close(); } }
    close() { this.pcmAccess?.close(); this.journalAccess?.close(); this.pcmAccess = this.journalAccess = null; }
    async recover() {
      const pcm = await this.pcm.getFile(), journal = await this.journal.getFile();
      const info = recoverJournal(await journal.text(), pcm.size, this.metadata.channels, this.metadata.sampleRate);
      return {...info,metadata:this.metadata,pcm:await pcm.slice(0,info.bytes).arrayBuffer()};
    }
  }
  function request(r) { return new Promise((resolve,reject) => { r.onsuccess=()=>resolve(r.result); r.onerror=()=>reject(r.error); }); }
  class IDBStore {
    static async open(indexedDB, id, metadata, existing = false) {
      const r = indexedDB.open('cicada-pcm',1);
      r.onupgradeneeded = () => r.result.createObjectStore('takes');
      const db = await request(r);
      const store = new IDBStore();
      Object.assign(store,{mode:'indexeddb',db,id,metadata,index:0,offset:0});
      if (!existing) await store.transaction(s => s.put(metadata,id+':meta'));
      else store.metadata = await store.get(id+':meta');
      if (!store.metadata) throw new Error('Take was not found');
      return store;
    }
    transaction(action) {
      return new Promise((resolve,reject) => {
        const tx = this.db.transaction('takes','readwrite');
        tx.oncomplete=resolve; tx.onabort=tx.onerror=()=>reject(tx.error || new Error('IndexedDB transaction failed'));
        try { action(tx.objectStore('takes')); } catch (e) { tx.abort(); reject(e); }
      });
    }
    get(key) { return request(this.db.transaction('takes','readonly').objectStore('takes').get(key)); }
    async append(record, bytes) {
      record.offset = this.offset; record.length = bytes.byteLength;
      const block = {record,bytes:bytes.slice().buffer};
      await this.transaction(s => { s.put(block,this.id+':block:'+this.index); });
      this.index++; this.offset += bytes.length;
    }
    async finish(summary) { try { await this.transaction(s => s.put(summary,this.id+':end')); } finally { this.close(); } }
    close() { this.db.close(); }
    async recover() {
      const blocks = []; let size = 0;
      for (let i=0;;i++) { const block = await this.get(this.id+':block:'+i); if (!block) break; blocks.push(block); size += block.bytes.byteLength; }
      const end = await this.get(this.id+':end');
      const text = blocks.map(b=>JSON.stringify(b.record)).join('\n') + (end?'\n'+JSON.stringify({type:'end',...end}):'') + '\n';
      const info = recoverJournal(text,size,this.metadata.channels,this.metadata.sampleRate);
      const pcm = new Uint8Array(info.bytes);
      for (const b of blocks) { if (b.record.offset + b.bytes.byteLength <= info.bytes) pcm.set(new Uint8Array(b.bytes),b.record.offset); }
      return {...info,metadata:this.metadata,pcm:pcm.buffer};
    }
  }
  async function openStore(env,id,metadata,existing = false,mode) {
    let opfsError;
    if ((!mode || mode==='opfs') && env.navigator?.storage?.getDirectory) {
      try { return await OPFSStore.open(env.navigator.storage,id,metadata,existing); }
      catch (e) { opfsError=e; if (existing) throw e; }
    }
    if ((!mode || mode==='indexeddb') && env.indexedDB) return IDBStore.open(env.indexedDB,id,metadata,existing);
    throw new Error('Recoverable storage unavailable'+(opfsError?': '+opfsError.message:''));
  }
  class CaptureWriter {
    constructor(store) { this.store=store; this.rawFrame=0; this.writtenFrames=0; this.incomplete=false; this.error=''; this.lostFrames=0; }
    async append(packet) {
      try {
        const b = packet.timing;
        validate(b,packet.bytes,this.store.metadata.channels);
        if (b.SampleRate !== this.store.metadata.sampleRate) throw new Error('Capture sample rate changed');
        this.incomplete ||= !!(b.Flags || b.GapFrames);
        const rawFrame = this.rawFrame + b.GapFrames;
        const record = {type:'block',timing:b,placement:placement(b),rawFrame};
        await this.store.append(record,new Uint8Array(packet.bytes,0,b.Frames*b.Layout*4));
        this.rawFrame = rawFrame + b.Frames; this.writtenFrames += b.Frames;
      } catch (error) { this.incomplete=true; this.error=error.message; this.lostFrames += (packet.timing?.Frames || 0) + (packet.timing?.GapFrames || 0); throw error; }
    }
    async finish(gapFrames) {
      gapFrames += this.lostFrames;
      this.incomplete ||= gapFrames > 0;
      const summary = {writtenFrames:this.writtenFrames,rawFrames:this.rawFrame+gapFrames,gapFrames,incomplete:this.incomplete,error:this.error};
      await this.store.finish(summary);
      return summary;
    }
  }
  const api = {OPFSStore,IDBStore,openStore,CaptureWriter,recoverJournal,placement};
  if (typeof module !== 'undefined') module.exports=api;
  if (typeof WorkerGlobalScope !== 'undefined' && root instanceof WorkerGlobalScope) {
    root.onmessage = async event => {
      const data=event.data;
      try {
        if (data.t==='open') {
          const store=await openStore(root,data.id,data.metadata), writer=new CaptureWriter(store), port=data.port;
          let queue=Promise.resolve(), failed=false;
          port.onmessage = event => {
            const p=event.data;
            queue=queue.then(async()=>{
              if (p.t==='pcm') {
                if (!failed) {
                  try { await writer.append(p); }
                  catch (error) { failed=true; root.postMessage({t:'fault',error:error.message}); }
                } else writer.lostFrames += p.timing.Frames + p.timing.GapFrames;
                p.t='recycle';
                port.postMessage(p,[p.bytes]);
              } else if (p.t==='end') {
                const summary=await writer.finish(p.gapFrames);
                root.postMessage({t:'finished',id:data.id,mode:store.mode,...summary});
              }
            }).catch(error=>{ failed=true; writer.incomplete=true; writer.error=error.message; store.close(); root.postMessage({t:'fault',error:error.message}); });
          };
          port.start();
          root.postMessage({t:'opened',id:data.id,mode:store.mode});
        } else if (data.t==='recover') {
          const store=await openStore(root,data.id,null,true,data.mode);
          try { const take=await store.recover(); root.postMessage({t:'recovered',id:data.id,...take},[take.pcm]); }
          finally { store.close(); }
        }
      } catch (error) { root.postMessage({t:'fault',error:error.message}); }
    };
  }
})(globalThis);
