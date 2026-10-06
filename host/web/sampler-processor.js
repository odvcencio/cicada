import {prepareSampler} from './instrument-pack.js';

// One immutable bank per instance. Note commands are applied at exact output
// frames through a fixed queue. Late events apply at the next frame boundary.
class CicadaSamplerProcessor extends AudioWorkletProcessor {
  constructor(options) {
    super();this.events=Array.from({length:512},()=>({kind:'',frame:0,id:0,note:0,velocity:0,cents:0,down:0,seeded:false,seedLow:0,seedHigh:0}));this.read=0;this.write=0;this.failed=false;this.owners=new Uint32Array(64);this.ids=new Uint32Array(64);
    try {
      const p=options.processorOptions;const instance=new WebAssembly.Instance(p.module,{});this.kernel=instance.exports;if(this.kernel._initialize)this.kernel._initialize();prepareSampler(this.kernel,sampleRate,p.pack,p.seed);this.outputPtr=this.kernel.sampler_output_ptr();this.left=new Float32Array(this.kernel.memory.buffer,this.outputPtr,4096);this.right=new Float32Array(this.kernel.memory.buffer,this.outputPtr+4096*4,4096);
      this.port.onmessage=e=>this.enqueue(e.data);this.port.postMessage({kind:'ready'});
    }catch(e){this.failed=true;this.port.postMessage({kind:'error',message:String(e.message)});}
  }
  enqueue(e) {
    if(this.failed)return;
    if(this.write-this.read>=this.events.length){this.failed=true;this.kernel.sampler_reset();this.port.postMessage({kind:'error',message:'sampler event queue overflow'});return;}
    if(!['on','off','pedal','legato','reset'].includes(e.kind)||!Number.isSafeInteger(e.frame)||e.frame<0){this.port.postMessage({kind:'error',message:'invalid sampler event'});return;}
    if(this.write>this.read&&e.frame<this.events[(this.write-1)%512].frame){this.port.postMessage({kind:'error',message:'sampler events must be ordered'});return;}
    if(!Number.isInteger(e.id??0)||(e.id??0)<0||(e.id??0)>0xffffffff||(e.kind==='on'&&(!(e.id>0)||!Number.isInteger(e.note)||e.note<0||e.note>127||!Number.isInteger(e.velocity)||e.velocity<1||e.velocity>127))||(e.kind==='legato'&&(!Number.isInteger(e.note)||e.note<0||e.note>127||!Number.isFinite(e.cents)||Math.abs(e.cents)>100))){this.port.postMessage({kind:'error',message:'invalid sampler note/owner'});return;}
    let seeded=false,seedLow=0,seedHigh=0;
    if(e.seed!==undefined){if(!(typeof e.seed==='string'&&/^\d+$/.test(e.seed)||Number.isSafeInteger(e.seed))){this.port.postMessage({kind:'error',message:'invalid exact event seed'});return;}const seed=BigInt(e.seed);if(seed<0n||seed>0xffffffffffffffffn){this.port.postMessage({kind:'error',message:'event seed outside uint64'});return;}seeded=true;seedLow=Number(seed&0xffffffffn);seedHigh=Number(seed>>32n);}
    const target=this.events[this.write%512];target.seeded=seeded;target.seedLow=seedLow;target.seedHigh=seedHigh;target.kind=e.kind;target.frame=e.frame;target.id=e.id??0;target.note=e.note??0;target.velocity=e.velocity??0;target.cents=e.cents??0;target.down=e.down?1:0;this.write++;
  }
  apply(e) {
    const k=this.kernel;
    if(e.kind==='on'){const id=e.seeded?k.sampler_note_on_seeded(e.note,e.velocity,e.seedLow,e.seedHigh):k.sampler_note_on(e.note,e.velocity);if(id){const slot=k.sampler_last_slot();this.owners[slot]=e.id;this.ids[slot]=id;}}
    else if(e.kind==='off'){for(let i=0;i<64;i++)if(this.owners[i]===e.id){k.sampler_note_off(this.ids[i]);this.owners[i]=0;}}
    else if(e.kind==='pedal')k.sampler_sustain(e.down);
    else if(e.kind==='legato'){for(let i=0;i<64;i++)if(this.owners[i]===e.id)k.sampler_legato(this.ids[i],e.note,e.cents);}
    else {k.sampler_reset();this.owners.fill(0);this.ids.fill(0);}
  }
  process(_inputs,outputs) {
    const out=outputs[0];if(!out||out.length<2)return true;
    if(this.failed){out[0].fill(0);out[1].fill(0);return true;}
    let offset=0;const length=out[0].length;
    while(offset<length) {
      while(this.read<this.write&&this.events[this.read%512].frame<=currentFrame+offset){this.apply(this.events[this.read%512]);this.read++;}
      const next=this.read<this.write?this.events[this.read%512].frame-currentFrame:length;
      const count=Math.min(length-offset,next-offset);
      if(this.kernel.sampler_render(count)!==0){this.failed=true;out[0].fill(0);out[1].fill(0);break;}
      for(let i=0;i<count;i++){out[0][offset+i]=this.left[i];out[1][offset+i]=this.right[i];}offset+=count;
    }
    return true;
  }
}
registerProcessor('cicada-sampler',CicadaSamplerProcessor);
