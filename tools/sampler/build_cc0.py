#!/usr/bin/env python3
"""Build pinned CC0 instrument packs. Requires Python 3, gh and FFmpeg.

Audio stays outside the repository. Every transformed file records the exact
source URL/hash and output hashes. Re-running reuses verified source downloads.
"""

import argparse
import concurrent.futures
import gzip
import hashlib
import json
import pathlib
import re
import shutil
import struct
import subprocess
import tempfile
import urllib.parse
import urllib.request

SOURCES = {
    "vcsl": (
        "sgossner/VCSL",
        "c1ea7bcc3c7309650ab0da9d15c9cd1fbc4a4c7e",
        "https://github.com/sgossner/VCSL/blob/c1ea7bcc3c7309650ab0da9d15c9cd1fbc4a4c7e/README.md",
    ),
    "guitar": (
        "freepats/spanish-classical-guitar",
        "6f4eb1b092acc88f5448cea1a0001bd07b971af8",
        "https://github.com/freepats/spanish-classical-guitar/blob/6f4eb1b092acc88f5448cea1a0001bd07b971af8/LICENSE",
    ),
    "bass": (
        "sfzinstruments/karoryfer.black-and-blue-basses",
        "6e7d674cdb41be7a54dbccb15472401ad01099b9",
        "https://shop.karoryfer.com/pages/free-black-and-blue-basses",
    ),
    "vsco": (
        "sgossner/VSCO-2-CE",
        "440300901dfe9275fd84e0b7763af1f8443ae62e",
        "https://github.com/sgossner/VSCO-2-CE/blob/440300901dfe9275fd84e0b7763af1f8443ae62e/LICENSE",
    ),
}


def sha(b):
    return hashlib.sha256(b).hexdigest()


def midi(n):
    m = re.fullmatch(r"([A-Ga-g])([#b]?)(-?\d+)", n)
    if not m:
        raise ValueError(n)
    return (
        12 * (int(m[3]) + 1)
        + dict(C=0, D=2, E=4, F=5, G=7, A=9, B=11)[m[1].upper()]
        + {"": 0, "#": 1, "b": -1}[m[2]]
    )


def config(voices=16, release=180, attack=2):
    return dict(
        Voices=voices,
        Amp=dict(Attack=attack, Decay=0, Sustain=1, Release=release),
        Filter=dict(Attack=0, Decay=0, Sustain=1, Release=0),
        Cutoff=0,
        FilterDepth=0,
        Gain=0.7,
        TuneCents=0,
        Humanize=dict(Seed="4242", DelayMS=0, Velocity=0, Cents=0),
    )


def zone(root, layer=64, pos=0, count=1, release=False):
    return dict(
        Root=root,
        KeyLow=root,
        KeyHigh=root,
        VelocityLow=1,
        VelocityHigh=127,
        Layer=layer,
        Group=0,
        Position=pos,
        Count=count,
        Release=release,
        Gain=1,
        TuneCents=0,
        Start=0,
        End=0,
        Loop=False,
        LoopStart=0,
        LoopEnd=0,
        Crossfade=0,
    )


def build(args):
    destination = pathlib.Path(args.out)
    destination.parent.mkdir(parents=True, exist_ok=True)
    out = pathlib.Path(
        tempfile.mkdtemp(prefix=".cicada-pack-build-", dir=destination.parent)
    )
    cache = pathlib.Path(args.cache)
    cache.mkdir(parents=True, exist_ok=True)
    trees = {}
    for name, (repo, pin, _) in SOURCES.items():
        cached = cache / (name + "-tree.json")
        if not cached.exists():
            cached.write_bytes(
                subprocess.check_output(
                    ["gh", "api", f"repos/{repo}/git/trees/{pin}?recursive=1"]
                )
            )
        trees[name] = [
            e["path"]
            for e in json.loads(cached.read_bytes())["tree"]
            if e["type"] == "blob"
        ]
    jobs = []
    manifests = {}

    def add(pack, source, path, z):
        jobs.append((pack, source, path, z))

    manifests["grand"] = dict(
        description="CC0 acoustic grand: four recorded dynamics where available, mapped keys and recorded dampening.",
        config=config(release=200),
    )
    # Piano and trumpet contributors label middle C as C3. Bass file names
    # and its SFZ map use written pitch, one octave above sounding bass.
    # Cicada uses sounding MIDI pitch (middle C = 60); qualify the recordings.
    roots = {10, 18, 24, 30, 36, 42, 48, 54, 60, 66, 72, 84, 96}
    for p in trees["vcsl"]:
        m = re.search(
            r"Grand Piano, Kawai/(Sustains|Releases)/GPiano_(?:sus|rel)_(.*?)_v(\d+)_rr1_Player.wav$",
            p,
        )
        if m and midi(m[2]) in roots:
            # The lowest labelled A#-1 recording rings at A0 (its 4th
            # partial is about 110 Hz), not Bb0; preserve sounding pitch.
            root = 21 if midi(m[2]) == 10 else midi(m[2]) + 12
            layer = {1: 24, 2: 56, 3: 88, 4: 120}[int(m[3])]
            add("grand", "vcsl", p, zone(root, layer, release=m[1] == "Releases"))
    manifests["nylon"] = dict(
        description="CC0 nylon classical guitar: one recorded dynamic per key; no fabricated layers.",
        config=config(release=400),
    )
    for p in trees["guitar"]:
        m = re.fullmatch(r"samples/(.*?).flac", p)
        if m and 40 <= midi(m[1]) <= 76:
            add("nylon", "guitar", p, zone(midi(m[1])))
    manifests["bass"] = dict(
        description="CC0 fingered electric bass: four dynamics, four takes and recorded releases.",
        config=config(release=120),
    )
    for p in trees["bass"]:
        m = re.fullmatch(
            r"Samples/darkblack/(reg|rel)/darkblack_(.*?)_(p|mp|mf|f|rel)_rr([1-4]).wav",
            p,
        )
        if m and midi(m[2]) in {35, 40, 45, 50, 55, 60, 65, 72, 76}:
            layer = {"p": 24, "mp": 56, "mf": 88, "f": 120, "rel": 64}[m[3]]
            add(
                "bass",
                "bass",
                p,
                zone(midi(m[2]) - 12, layer, int(m[4]) - 1, 4, m[1] == "rel"),
            )
    manifests["kit"] = dict(
        description="CC0 acoustic kit: kick, snare, closed/open hats; recorded dynamics and two takes.",
        config=config(voices=16, release=1000),
    )
    for p in trees["vcsl"]:
        m = re.search(r"Bass Drum 1/BDrumNew_hit_v(\d+)_rr([12])_Sum.wav$", p)
        if m:
            add(
                "kit",
                "vcsl",
                p,
                zone(36, {2: 24, 3: 56, 5: 88, 7: 120}[int(m[1])], int(m[2]) - 1, 2),
            )
        m = re.search(r"Snare Drum, Modern 1/Snare2_HitSN_v(\d+)_rr([12])_Mid.wav$", p)
        if m:
            add(
                "kit",
                "vcsl",
                p,
                zone(
                    38,
                    {1: 16, 2: 32, 3: 48, 4: 64, 5: 80, 6: 104, 7: 120}.get(
                        int(m[1]), 127
                    ),
                    int(m[2]) - 1,
                    2,
                ),
            )
        m = re.search(r"Hi-Hat Cymbal/HiHat_HitC_v(\d+)_rr([12])_Mid.wav$", p)
        if m:
            add(
                "kit",
                "vcsl",
                p,
                zone(42, {1: 24, 2: 56, 3: 88, 4: 120}[int(m[1])], int(m[2]) - 1, 2),
            )
        m = re.search(r"Hi-Hat Cymbal/HiHat_HitO_rr([12])_Mid.wav$", p)
        if m:
            add("kit", "vcsl", p, zone(46, 64, int(m[1]) - 1, 2))
    manifests["violin"] = dict(
        description="CC0 solo violin: two recorded dynamics with recorded vibrato; no legato transitions.",
        config=config(release=220, attack=12),
    )
    manifests["trumpet"] = dict(
        description="CC0 trumpet: two recorded sustain dynamics; no recorded legato transitions.",
        config=config(release=180, attack=8),
    )
    for p in trees["vsco"]:
        m = re.fullmatch(
            r"Strings/Solo Violin/Arco Vib/LLVln_ArcoVib_(.*?)_([pf]).wav", p
        )
        if m and 55 <= midi(m[1]) <= 84:
            add("violin", "vsco", p, zone(midi(m[1]), 32 if m[2] == "p" else 112))
        m = re.fullmatch(
            r"Brass/Trumpet/sus/Sum_SHTrumpet_sus_(.*?)_v([13])_rr1.wav", p
        )
        if m and 48 <= midi(m[1]) <= 76:
            add("trumpet", "vsco", p, zone(midi(m[1]) + 12, 32 if m[2] == "1" else 112))
    selected = {k: [] for k in manifests}

    def convert(job):
        pack, source, p, z = job
        repo, pin, license_url = SOURCES[source]
        url = f"https://raw.githubusercontent.com/{repo}/{pin}/" + urllib.parse.quote(
            p, safe="/"
        )
        sourcefile = cache / (sha(url.encode()) + pathlib.Path(p).suffix)
        if not sourcefile.exists():
            with urllib.request.urlopen(url, timeout=120) as response:
                data = response.read()
            # LFS pointers are not audio. Use GitHub's pinned media endpoint if needed.
            if data.startswith(b"version https://git-lfs"):
                media = (
                    f"https://media.githubusercontent.com/media/{repo}/{pin}/"
                    + urllib.parse.quote(p, safe="/")
                )
                with urllib.request.urlopen(media, timeout=120) as response:
                    data = response.read()
            sourcefile.write_bytes(data)
        src = sourcefile.read_bytes()
        ident = sha(url.encode())[:20]
        folder = out / pack / "samples"
        folder.mkdir(parents=True, exist_ok=True)
        wavpath = folder / (ident + ".wav")
        floating = pack == "kit"
        codec = "pcm_f32le" if floating else "pcm_s24le"
        subprocess.run(
            [
                "ffmpeg",
                "-v",
                "error",
                "-nostdin",
                "-y",
                "-i",
                str(sourcefile),
                "-map_metadata",
                "-1",
                "-t",
                "8",
                "-ar",
                "48000",
                "-c:a",
                codec,
                "-threads",
                "1",
                str(wavpath),
            ],
            check=True,
        )
        encoded = wavpath.read_bytes()
        offset = 12
        pcm = None
        channels = None
        while offset + 8 <= len(encoded):
            tag = encoded[offset : offset + 4]
            length = struct.unpack_from("<I", encoded, offset + 4)[0]
            start = offset + 8
            if tag == b"fmt ":
                channels = struct.unpack_from("<H", encoded, start + 2)[0]
            if tag == b"data":
                pcm = encoded[start : start + length]
            offset = start + length + (length & 1)
        if pcm is None or channels not in (1, 2):
            raise ValueError("unsupported converted WAV")
        width = 4 if floating else 3
        frames = len(pcm) // (channels * width)
        rate = 48000
        if len(pcm) != frames * channels * width:
            raise ValueError("partial converted PCM frame")
        pad = b"\0" if len(pcm) & 1 else b""
        fmt = struct.pack(
            "<HHIIHH",
            3 if floating else 1,
            channels,
            rate,
            rate * channels * width,
            channels * width,
            width * 8,
        )
        wavdata = (
            b"RIFF"
            + struct.pack("<I", 36 + len(pcm) + len(pad))
            + b"WAVEfmt "
            + struct.pack("<I", 16)
            + fmt
            + b"data"
            + struct.pack("<I", len(pcm))
            + pcm
            + pad
        )
        data = gzip.compress(wavdata, compresslevel=9, mtime=0)
        target = folder / (ident + ".wav.gz")
        target.write_bytes(data)
        wavpath.unlink()
        a = dict(
            id=ident,
            path="samples/" + target.name,
            sha256=sha(data),
            bytes=len(data),
            wav_sha256=sha(wavdata),
            wav_bytes=len(wavdata),
            frames=frames,
            rate=rate,
            channels=channels,
            source_url=url,
            source_sha256=sha(src),
            license="CC0-1.0",
            license_url=license_url,
        )
        if pack in ("violin", "trumpet") and frames > rate * 3:
            z.update(
                Loop=True,
                LoopStart=rate,
                LoopEnd=min(frames, rate * 3),
                Crossfade=rate // 5,
            )
        z["asset"] = ident
        return pack, a, z

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
        for pack, a, z in pool.map(convert, jobs):
            selected[pack].append((a, z))
    catalog = []
    for name, meta in manifests.items():
        entries = selected[name]
        if not entries:
            raise ValueError("empty pack " + name)
        roots = sorted({z["Root"] for _, z in entries})
        for _, z in entries:
            i = roots.index(z["Root"])
            z["Group"] = i
            z["ChokeGroup"] = 1 if name == "kit" and z["Root"] in (42, 46) else 0
            z["OneShot"] = name == "kit"
            if name != "kit":
                z["KeyLow"] = (
                    (roots[i - 1] + z["Root"]) // 2 + 1
                    if i
                    else max(
                        {
                            "grand": 21,
                            "nylon": 40,
                            "bass": 23,
                            "violin": 55,
                            "trumpet": 58,
                        }[name],
                        z["Root"] - 2,
                    )
                )
                z["KeyHigh"] = (
                    (z["Root"] + roots[i + 1]) // 2
                    if i + 1 < len(roots)
                    else min(108 if name == "grand" else 127, z["Root"] + 2)
                )
        entries.sort(
            key=lambda e: (
                e[1]["Root"],
                e[1]["Release"],
                e[1]["Layer"],
                e[1]["Position"],
            )
        )
        m = dict(
            format="cicada.instrument-pack/1",
            id=name,
            **meta,
            assets=[a for a, _ in entries],
            zones=[z for _, z in entries],
        )
        data = (json.dumps(m, indent=2, sort_keys=True) + "\n").encode()
        (out / name / "manifest.json").write_bytes(data)
        pcm = sum(a["frames"] * a["channels"] * 4 for a, _ in entries)
        if pcm > 256 << 20:
            raise ValueError(f"{name} resident PCM {pcm} exceeds 256 MiB")
        catalog.append(
            dict(
                id=name,
                manifest=name + "/manifest.json",
                sha256=sha(data),
                assets=len(entries),
                compressed_bytes=sum(a["bytes"] for a, _ in entries),
                pcm_bytes=pcm,
                license="CC0-1.0",
            )
        )
        print(json.dumps(catalog[-1]), flush=True)
    if args.verify_catalog:
        expected = json.loads(pathlib.Path(args.verify_catalog).read_text())["packs"]
        if catalog != expected:
            shutil.rmtree(out)
            raise ValueError(
                "rebuilt catalog differs from published pins; keep the existing pack and inspect toolchain/source changes"
            )
    (out / "catalog.json").write_text(
        json.dumps(dict(format="cicada.instrument-catalog/1", packs=catalog), indent=2)
        + "\n"
    )
    if destination.exists():
        if not (destination / "catalog.json").is_file() or json.loads(
            (destination / "catalog.json").read_text()
        ) != json.loads((out / "catalog.json").read_text()):
            shutil.rmtree(out)
            raise ValueError(
                "refusing to overwrite a different output pack; use a fresh destination"
            )
        for entry in catalog:
            existing_manifest = destination / entry["manifest"]
            if (
                not existing_manifest.is_file()
                or sha(existing_manifest.read_bytes()) != entry["sha256"]
            ):
                shutil.rmtree(out)
                raise ValueError(
                    "existing pack manifest is corrupt; rebuild into a fresh destination"
                )
            m = json.loads((out / entry["manifest"]).read_text())
            for a in m["assets"]:
                existing = destination / entry["id"] / a["path"]
                if not existing.is_file() or sha(existing.read_bytes()) != a["sha256"]:
                    shutil.rmtree(out)
                    raise ValueError(
                        "existing pack is corrupt; rebuild into a fresh destination"
                    )
        shutil.rmtree(out)
    else:
        out.rename(destination)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", required=True)
    parser.add_argument("--cache", required=True)
    parser.add_argument("--jobs", type=int, default=4)
    parser.add_argument("--verify-catalog")
    build(parser.parse_args())
