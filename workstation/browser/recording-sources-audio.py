#!/usr/bin/env python3
"""Compare multi-file recording output with separately authored scores."""

import argparse
import hashlib
import json
import math
from pathlib import Path
import subprocess
import tempfile
import wave


ENTRY = '''cicada 2
title "Keep the complete take"
tempo 120
seed 42
scene main { lead=line keys=answer }
song { main*2 }
'''
LEAD = '''instrument glass { voice mono { out=sine(pitch)*env(gate,200ms)*(0.1+pressure*0.1+timbre*0.1) } }
track lead glass { level=-24dB }
'''
KEYS = 'track keys piano { level=-24dB }\n'
GRID_LEAD = '''pattern line notes steps=16 {
 c4 - . . g4 - . . . . . . . . . .
 bend: 75ct 75ct 0ct 0ct 0ct 0ct 0ct 0ct 0ct 0ct 0ct 0ct 0ct 0ct 0ct 0ct
 pressure: 0.6 0.6 0 0 0 0 0 0 0 0 0 0 0 0 0 0
 timbre: 0.4 0.4 0.5 0.5 0.5 0.5 0.5 0.5 0.5 0.5 0.5 0.5 0.5 0.5 0.5 0.5
}
'''
GRID_KEYS = 'pattern answer notes steps=16 { . e4 . . . a4 . . . . . . . . . . }\n'
EMPTY_KEYS = 'pattern answer notes steps=16 { . . . . . . . . . . . . . . . . }\n'
PLAYED_LEAD = '''pattern line performance length=3840ticks {
 gesture-group touch {
  gesture at=17ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=137ticks bend=100.01220852154805ct pressure=0 timbre=0.5;
  gesture at=170ticks bend=100.01220852154805ct pressure=0 timbre=1;
  gesture at=3360ticks bend=-100ct pressure=0 timbre=1;
 }
 note 60 at=17ticks length=343ticks velocity=103 group=touch {
  gesture at=73ticks bend=0ct pressure=1 timbre=0.5;
 }
 note 67 at=977ticks length=343ticks velocity=87 group=touch;
}
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cicada', type=Path, required=True)
    parser.add_argument('--proof', type=Path, required=True)
    args = parser.parse_args()
    proof = args.proof.resolve()
    report = json.loads((proof / 'report.json').read_text())['result']
    reopened = json.loads((proof / 'reopen-report.json').read_text())['result']
    assert report['ok'] and reopened['ok']
    assert report['played']['audio'] == reopened['reopened']
    for name in ['played', 'reopened']:
        assert hashlib.sha256((proof / (name + '.wav')).read_bytes()).hexdigest() == report['played']['audio']['hash']
    capture = report['played']['capture']
    assert capture['version'] == 4 and capture['sourceRevision'] == report['captureReceipt']
    take, = capture['takes']
    assert [(n['note'], n['tick'], n['endTick'], n['velocity'], n['group']) for n in take['notes']] == [(60, 3857, 4200, 103, 1), (67, 4817, 5160, 87, 1)]
    group, = take['groups']
    assert group['kind'] == 'channel' and [p['tick'] for p in group['points']] == [3857, 3977, 4010, 7200]
    assert group['points'][-1]['pitchCents'] == -100
    assert take['notes'][0]['expressions'] == [{'tick': 3930, 'pitchCents': 0, 'pressure': 1, 'timbre': 0.5}]
    fixture = json.loads((proof / 'fixture.json').read_text())
    for key, filename in [('lead', 'lead.cicada'), ('keys', 'keys.cicada')]:
        data = Path(fixture[key]).read_bytes()
        assert b'\r\n' in data and b'\n' not in data.replace(b'\r\n', b''), (filename, 'lost CRLF')
        # HTML textarea values normalize line endings; check disk bytes too.
        assert data.decode().replace('\r\n', '\n') == report['played']['files'][filename]
    sources = {
        'grid': (report['initialEntry'], report['grid']['files']),
        'played': (report['initialEntry'], report['played']['files']),
        'reference-grid': (ENTRY, {'lead.cicada': LEAD + GRID_LEAD, 'keys.cicada': KEYS + GRID_KEYS}),
        'reference-played': (ENTRY, {'lead.cicada': LEAD + PLAYED_LEAD, 'keys.cicada': KEYS + EMPTY_KEYS}),
    }
    output = Path(tempfile.mkdtemp(prefix='audio-reference-', dir=proof))
    scores = {}
    for name, (entry, files) in sources.items():
        folder = output / name
        folder.mkdir()
        (folder / 'main.cicada').write_text(entry)
        for filename, text in files.items():
            (folder / filename).write_text(text)
        (folder / 'cicada.mod').write_text('project recording-sources\ncicada 2\nentry "main.cicada"\nsource "lead.cicada"\nsource "keys.cicada"\n')
        scores[name] = folder / 'main.cicada'
    checks = []
    for rate in [44100, 48000]:
        pcm, frames = {}, {}
        for name, score in scores.items():
            wav = output / (name + f'-{rate}.wav')
            result = subprocess.run([str(args.cicada.resolve()), 'render', str(score), '-o', str(wav), f'--rate={rate}', '--bits=24', '--dither=false'], capture_output=True, text=True)
            wav.with_suffix('.log').write_text(result.stdout + result.stderr)
            assert result.returncode == 0, result.stderr
            with wave.open(str(wav), 'rb') as stream:
                assert stream.getframerate() == rate and stream.getnchannels() == 2 and stream.getsampwidth() == 3
                frames[name] = stream.getnframes()
                pcm[name] = stream.readframes(frames[name])
        for name in ['grid', 'played']:
            assert pcm[name] == pcm['reference-' + name], (rate, name, 'differs from authored reference')
            samples = [int.from_bytes(pcm[name][i:i+3], 'little', signed=True) / 8388608 for i in range(0, len(pcm[name]), 3)]
            peak = max(map(abs, samples))
            rms = math.sqrt(sum(x*x for x in samples) / len(samples))
            assert 0 < peak < 1 and rms > 1e-6
            checks.append({'timing': name, 'rate': rate, 'frames': frames[name], 'authoredReferencePCMIdentical': True, 'peak': peak, 'rms': rms})
        assert pcm['grid'] != pcm['played']
    (proof / 'audio.json').write_text(json.dumps({'output': str(output), 'freshProcessReopenMatches': True, 'rawCaptureIdentityAndArticulationRetained': True, 'checks': checks}, indent=2) + '\n')
    print('Grid and played recordings match authored references at 44.1 and 48 kHz; reopened WAV is identical.')


if __name__ == '__main__':
    main()
