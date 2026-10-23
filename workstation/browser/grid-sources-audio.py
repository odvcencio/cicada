#!/usr/bin/env python3
"""Verify source-aware grid editing against independently authored audio."""

import argparse
import hashlib
import json
import math
from pathlib import Path
import subprocess
import tempfile
import wave


ENTRY = '''cicada 2
title "Develop the idea"
tempo 120
seed 42
track lead acid { level=-24dB cutoff=700Hz }
track keys piano { level=-24dB }
track drums drums { level=-24dB }
scene main { lead=line keys=harmony drums=beat }
song { main*2 }
'''
PHRASE = 'phrase motif { c4 . e4 . }\n'
BASELINE = {
    'main.cicada': ENTRY,
    'lead.cicada': 'pattern line acid steps=8 { use motif use motif +12 }\n',
    'keys.cicada': 'pattern harmony notes steps=8 { c4 - . . g4 . . . }\n',
    'drums.cicada': 'pattern beat drums steps=8 { sd: ..x..... bd: x....... ch: x.x.x.x. }\n',
    'phrases.cicada': PHRASE,
}
EDITED = {
    'main.cicada': ENTRY,
    'lead.cicada': 'pattern line acid steps=12 swing=55% gate=65% transpose=-12 { d5^~*2?75 . f5 . c5 a4 e5 . . . . . }\n',
    'keys.cicada': 'pattern harmony notes steps=8 { [d4 f4 a4] - . . g4 . . . }\n',
    'drums.cicada': 'pattern beat drums steps=8 { sd: ..xx4*2?90.... bd: x....... ch: x.x.x.x. }\n',
    'phrases.cicada': PHRASE,
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cicada', type=Path, required=True)
    parser.add_argument('--proof', type=Path, required=True)
    args = parser.parse_args()
    proof = args.proof.resolve()
    report = json.loads((proof / 'report.json').read_text())['result']
    reopened = json.loads((proof / 'reopen-report.json').read_text())['result']
    assert report['ok'] and reopened['ok']
    assert report['exported'] == reopened['reopened']
    for name in ['edited', 'reopened']:
        assert hashlib.sha256((proof / (name + '.wav')).read_bytes()).hexdigest() == report['exported']['hash']
    fixture = json.loads((proof / 'fixture.json').read_text())
    folder = Path(fixture['score']).parent
    for filename, text in report['final'].items():
        raw = (folder / filename).read_bytes()
        assert b'\r\n' in raw and b'\n' not in raw.replace(b'\r\n', b''), (filename, 'lost CRLF')
        assert raw.decode().replace('\r\n', '\n') == text
    assert report['final']['main.cicada'] == report['initialEntry']
    sources = {'baseline': report['original'], 'edited': report['final'], 'reference-baseline': BASELINE, 'reference-edited': EDITED}
    output = Path(tempfile.mkdtemp(prefix='audio-reference-', dir=proof))
    scores = {}
    for name, files in sources.items():
        folder = output / name
        folder.mkdir()
        for filename, text in files.items():
            (folder / filename).write_text(text)
        (folder / 'cicada.mod').write_text('project grid-sources\ncicada 2\nentry "main.cicada"\nsource "lead.cicada"\nsource "keys.cicada"\nsource "drums.cicada"\nsource "phrases.cicada"\n')
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
        for name in ['baseline', 'edited']:
            assert pcm[name] == pcm['reference-' + name], (rate, name, 'differs from authored reference')
        assert frames['baseline'] == frames['edited']
        samples = [int.from_bytes(pcm['edited'][i:i+3], 'little', signed=True) / 8388608 for i in range(0, len(pcm['edited']), 3)]
        delta = [(int.from_bytes(pcm['edited'][i:i+3], 'little', signed=True) - int.from_bytes(pcm['baseline'][i:i+3], 'little', signed=True)) / 8388608 for i in range(0, len(pcm['edited']), 3)]
        peak = max(map(abs, samples))
        rms = math.sqrt(sum(x*x for x in samples) / len(samples))
        difference = math.sqrt(sum(x*x for x in delta) / len(delta))
        assert 0 < peak < 1 and rms > 1e-6 and difference > 1e-6
        checks.append({'rate': rate, 'frames': frames['edited'], 'baselinePCMIdentical': True, 'editedPCMIdentical': True, 'peak': peak, 'rms': rms, 'differenceRMS': difference})
    (proof / 'audio.json').write_text(json.dumps({'output': str(output), 'freshProcessReopenMatches': True, 'checks': checks}, indent=2) + '\n')
    print('Baseline and edited grid songs match authored references at 44.1 and 48 kHz; reopened WAV is identical.')


if __name__ == '__main__':
    main()
