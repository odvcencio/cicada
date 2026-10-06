#!/usr/bin/env python3
"""Generate matched groove scores, render an A/B pack and decode every WAV.

Pass a destination explicitly; audio and machine paths are never stored in the
repository. Example: python3 tools/audio/kit-pack.py --cicada ./cicada --output pack
Use --generate-only after editing the shared groove schedule.
"""

import argparse
import array
import hashlib
import json
import math
from pathlib import Path
import shutil
import struct
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
EXAMPLES = ROOT / "examples/modeled-kit"
MODELS = dict(bd="kick", sd="snare", ch="hat_closed", oh="hat_open", cp="hat_pedal",
              rs="cross_stick", lt="tom_low", mt="tom_mid", ht="tom_high",
              cb="ride_bow", cy="crash")
ALTERNATES = dict(sd="rimshot", ch="hat_half_open", cb="ride_bell", cy="splash")


def score(schedule, groove, modeled, room=False):
    parts = ["cicada 2", f'title "{groove["title"]}: {"modeled" if modeled else "before"}"',
             f'tempo {groove["tempo"]}', f'seed {schedule["seed"]}', ""]
    if room:
        parts += ["fx room reverb {", "  size = 0.7", "  decay = 0.8s", "  damp = 6kHz",
                  "  highpass = 120Hz", "  predelay = 12ms", "  mix = 1", "}", ""]
    for name, models in (("acoustic", MODELS), ("articulations", ALTERNATES)):
        parts.append(f"kit {name} {{")
        for lane, model in models.items():
            recipe = f"model.{model}" if modeled else f"builtin.{lane}"
            parts.append(f"  {lane} = {recipe}")
        parts += ["}", ""]
    for name, kind in (("kit", "acoustic"), ("extra", "articulations")):
        parts += [f'track {name} {kind} {{',f'  level = {schedule["level_db"]}dB']
        if room:parts.append("  send room = 0.16")
        parts += ["}",""]
    for name, rows in (("main", groove["main"]), ("extra", groove["articulations"])):
        parts.append(f'pattern {name} drums steps={groove["steps"]} {{')
        for lane, cells in rows.items():
            if len(cells) != groove["steps"]:
                raise ValueError(f'{groove["id"]} {lane}: wrong cell count')
            parts.append(f"  {lane}: " + " ".join("".join(cells[i:i+4]) for i in range(0,len(cells),4)))
        parts += ["}", ""]
    parts += ["scene groove {", "  kit = main", "  extra = extra", "}",
              f'song {{ groove*{groove["repeats"]} }}', ""]
    return "\n".join(parts)


def generate(schedule):
    paths = []
    for groove in schedule["grooves"]:
        for modeled in (False, True):
            tag = "after" if modeled else "before"
            path = EXAMPLES / f'{groove["id"]}-{tag}.cicada'
            path.write_text(score(schedule, groove, modeled))
            paths.append(path)
    for groove in schedule["grooves"]:
        path=EXAMPLES/f'{groove["id"]}-after-room.cicada'
        path.write_text(score(schedule,groove,True,room=True));paths.append(path)
    return paths


def verify(path):
    raw=path.read_bytes()
    if raw[:4]!=b"RIFF" or raw[8:12]!=b"WAVE":
        raise ValueError(f"Invalid WAV: {path.name}")
    offset=12;fmt=pcm=None
    while offset+8<=len(raw):
        name,length=struct.unpack_from("<4sI",raw,offset);start=offset+8
        if start+length>len(raw):raise ValueError("Truncated WAV chunk")
        if name==b"fmt ":fmt=struct.unpack_from("<HHIIHH",raw,start)
        if name==b"data":pcm=raw[start:start+length]
        offset=start+length+(length&1)
    if fmt is None or pcm is None:raise ValueError("Missing WAV audio")
    encoding,channels,rate,_,alignment,bits=fmt;width=bits//8
    if channels not in (1,2) or alignment!=channels*width or len(pcm)%alignment:
        raise ValueError("Invalid WAV frame layout")
    if encoding==3 and bits==32:
        decoded=array.array("f");decoded.frombytes(pcm)
        if sys.byteorder!="little":decoded.byteswap()
        if not all(math.isfinite(value) for value in decoded):raise ValueError("Nonfinite float WAV")
        audible=any(decoded)
    elif encoding==1 and bits in (16,24,32):
        # Every integer code has a finite decoded amplitude. Check the full
        # frame payload rather than accepting a WAV header alone.
        audible=any(pcm)
    else:raise ValueError("Unsupported WAV encoding")
    count=len(pcm)//alignment
    if not count or not audible:raise ValueError(f"Empty WAV: {path.name}")
    return dict(file=path.name, rate_hz=rate, channels=channels, bits=bits,
                frames=count, seconds=count/rate, bytes=path.stat().st_size,
                sha256=hashlib.sha256(raw).hexdigest(), decoded=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--generate-only", action="store_true")
    parser.add_argument("--cicada", help="Candidate CLI binary")
    parser.add_argument("--before-cicada", help="Optional baseline CLI binary")
    parser.add_argument("--auditions",type=Path,help="Audition WAV directory from kit-images.go")
    parser.add_argument("--verify-only",action="store_true",help="Decode every WAV and check an existing playlist")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if args.verify_only:
        if not args.output:parser.error("--output is required for verification")
        reports=[]
        for path in sorted(args.output.rglob("*.wav")):
            report=verify(path);report["file"]=str(path.relative_to(args.output));reports.append(report)
        playlist=(args.output/"LISTEN.m3u").read_text().splitlines()
        for entry in playlist:
            if entry and not entry.startswith("#") and not (args.output/entry).is_file():
                raise ValueError("Missing playlist entry")
        (args.output/"decode-report.json").write_text(json.dumps(reports,indent=2)+"\n")
        print(f"Verified {len(reports)} WAVs and every playlist entry");return
    schedule = json.loads((EXAMPLES / "grooves.json").read_text())
    paths = generate(schedule)
    if args.generate_only:
        print(f"Generated {len(paths)} matched scores")
        return
    if not args.cicada or not args.output:
        parser.error("--cicada and --output are required for rendering")
    args.output.mkdir(parents=True, exist_ok=True)
    reports, playlist = [], ["#EXTM3U"]
    for path in paths:
        binary = args.before_cicada if path.stem.endswith("before") and args.before_cicada else args.cicada
        destination = args.output / (path.stem + ".wav")
        subprocess.run([str(Path(binary).resolve()), "render", str(path), "-o", str(destination),
                        "--rate", str(schedule["rate_hz"]), "--bits", str(schedule["bits"]),
                        "--tail", f'{schedule["tail_seconds"]}s', "--dither=false"], check=True)
        reports.append(verify(destination))
        playlist += [f'#EXTINF:{reports[-1]["seconds"]:.3f},{path.stem}', destination.name]
        shutil.copyfile(path, args.output / path.name)
    for index in range(0,2*len(schedule["grooves"]),2):
        if reports[index]["frames"] != reports[index+1]["frames"]:
            raise ValueError("Matched groove WAV lengths differ")
    if args.auditions:
        for path in sorted(args.auditions.glob("*.wav")):
            if path.name.startswith(("audition-","velocity-","position-","choke-")):
                destination=args.output/path.name;shutil.copyfile(path,destination)
                reports.append(verify(destination))
                playlist += [f'#EXTINF:{reports[-1]["seconds"]:.3f},{path.stem}',destination.name]
                recipe=path.with_suffix(".json")
                if recipe.exists():shutil.copyfile(recipe,args.output/recipe.name)
    (args.output / "LISTEN.m3u").write_text("\n".join(playlist) + "\n")
    shutil.copyfile(EXAMPLES / "grooves.json", args.output / "grooves.json")
    (args.output / "decode-report.json").write_text(json.dumps(reports,indent=2)+"\n")
    for entry in playlist:
        if not entry.startswith("#") and not (args.output / entry).is_file():
            raise ValueError("Missing playlist entry")
    print(f"Verified {len(reports)} WAVs and every playlist entry")


if __name__ == "__main__":
    main()
