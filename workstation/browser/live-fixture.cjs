'use strict';
// Writes the disposable project that amp-automation, live-write and live-write-uncertain run on:
// a guitar audio track that plays dry.wav (a synthetic 440 Hz sine, no recording) through one amp.
//   node live-fixture.cjs DIR [--no-curve]   prints DIR/main.cicada
// By default the score has a guitar.amp_drive curve (amp-automation edits it). With --no-curve the
// parameter has none, as live-write needs: recording a pass must not meet a saved curve.
const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
function writeFixture(dir,{curve=true}={}){
 const root=path.resolve(dir);fs.mkdirSync(root,{recursive:true});if(fs.existsSync(path.join(root,'main.cicada')))throw new Error('Use a new disposable directory');
 const frames=192000,wav=Buffer.alloc(44+frames*2);wav.write('RIFF',0);wav.writeUInt32LE(36+frames*2,4);wav.write('WAVEfmt ',8);wav.writeUInt32LE(16,16);wav.writeUInt16LE(1,20);wav.writeUInt16LE(1,22);wav.writeUInt32LE(48000,24);wav.writeUInt32LE(96000,28);wav.writeUInt16LE(2,32);wav.writeUInt16LE(16,34);wav.write('data',36);wav.writeUInt32LE(frames*2,40);
 for(let n=0;n<frames;n++)wav.writeInt16LE(Math.round(.2*32767*Math.sin(2*Math.PI*440*n/48000)),44+n*2);
 fs.writeFileSync(path.join(root,'dry.wav'),wav);
 fs.writeFileSync(path.join(root,'cicada.mod'),'project amp-automation\ncicada 2\nentry "main.cicada"\n');
 const sha=crypto.createHash('sha256').update(wav).digest('hex');
 fs.writeFileSync(path.join(root,'main.cicada'),`cicada 2
// cicada-amp-automation-test: disposable fixture
title "Synthetic live automation"
tempo 120
seed 42
asset dry "dry.wav" { sha256 = "${sha}" format = wav frames = ${frames} rate = 48000Hz channels = 1 source = generated }
clip dry-a dry { start = 0frames end = ${frames}frames }
fx rig ampcab { drive = 0.2 tone = 0.4 output = -3dB cabinet = 1 }
track guitar audio { insert = rig }
track keys acid { level = -30dB }
pattern pulse notes steps=16 { 1 . 5 . 1 . 5 . 1 . 5 . 1 . 5 . }
scene verse { guitar = dry-a keys = pulse }
song { verse*16 }
${curve?'automate guitar.amp_drive { @1.1.1 0.2 @2.1.1 0.8 }\n':''}`);
 return path.join(root,'main.cicada')
}
module.exports={writeFixture};
if(require.main===module)process.stdout.write(writeFixture(process.argv[2],{curve:!process.argv.includes('--no-curve')})+'\n');
