#!/usr/bin/env python3
"""Build the full drum kit from pinned, independently licensed recordings.

Audio is external. Python 3, NumPy, bsdtar and FFmpeg are required. The two
banks preserve stereo decay and each stays below the host's 256 MiB PCM cap.
"""

import argparse
import concurrent.futures
import hashlib
import json
import pathlib
import shutil
import struct
import subprocess
import tempfile
import urllib.parse
import urllib.request
import wave
import xml.etree.ElementTree as ET

import numpy as np

from build_cc0 import canonical_gzip, config, zone

ARCHIVE_URL = "https://drumgizmo.org/kits/CrocellKit/CrocellKit_Stereo_MIX.rar"
ARCHIVE_SHA = "a6ec5a58987c90ba482f568005e01ed826a534fd9b3c041dc5efa07018c4d81d"
CREDITS_URL = "https://drumgizmo.org/wiki/doku.php?id=kits:crocellkit"
ATTRIBUTION = (
    "CrocellKit by DrumGizmo and Crocell, CC-BY-4.0; official stereo adaptation. "
    "Full recording and stereo-mix credits: " + CREDITS_URL + ". "
    "Cicada adaptation: selected recorded hits, velocity/take mapping, canonical "
    "WAV encoding and a 5 ms ending fade. No endorsement implied."
)
VCSL_PIN = "c1ea7bcc3c7309650ab0da9d15c9cd1fbc4a4c7e"
# bank, articulation, MIDI note, source instrument, dynamic centers, takes/center
PIECES = [
    ("shells", "kick", 36, "KDrumR", 6, 3),
    ("shells", "kick_left", 35, "KDrumL", 6, 3),
    ("shells", "snare_center", 38, "Snare", 8, 3),
    ("shells", "snare_rimshot", 40, "SnareRimShot", 4, 3),
    ("shells", "snare_rim", 31, "SnareRim", 6, 3),
    ("shells", "tom_high", 48, "Tom1", 6, 3),
    ("shells", "tom_mid", 45, "Tom2", 6, 3),
    ("shells", "tom_floor", 43, "FTom1", 6, 3),
    ("shells", "tom_low_floor", 41, "FTom2", 6, 3),
    ("metals", "hat_closed", 42, "HihatClosed", 6, 3),
    ("metals", "hat_loose", 22, "HihatClosedNoPedal", 4, 3),
    ("metals", "hat_half_open", 23, "HihatSemiOpen", 6, 3),
    ("metals", "hat_open", 46, "HihatOpen", 6, 3),
    ("metals", "hat_pedal", 44, "HihatPedal", 4, 2),
    ("metals", "ride_bow", 51, "RideR", 4, 2),
    ("metals", "ride_bell", 53, "RideRBell", 4, 2),
    ("metals", "crash_left", 49, "CrashL", 4, 3),
    ("metals", "crash_right", 57, "CrashR", 4, 3),
    ("metals", "splash", 55, "SplashL", 4, 2),
    ("metals", "splash_right", 32, "SplashR", 4, 2),
    ("metals", "china", 52, "ChinaR", 4, 3),
    ("metals", "crash_left_stopped", 24, "CrashLStopped", 4, 2),
    ("metals", "crash_right_stopped", 25, "CrashRStopped", 4, 2),
]
CHOKES = {
    "ride_bow": 7,
    "ride_bell": 7,
    "ride_crash": 7,
    "crash_left": 2,
    "crash_left_stopped": 2,
    "crash_right": 3,
    "crash_right_stopped": 3,
    "splash": 4,
    "splash_right": 5,
    "china": 6,
}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def json_bytes(data):
    return (json.dumps(data, indent=2, sort_keys=True) + "\n").encode()


def fetch(url, target, expected):
    if target.exists():
        if sha(target.read_bytes()) != expected:
            raise ValueError("cached source checksum mismatch: " + target.name)
        return
    temp = target.with_suffix(target.suffix + ".part")
    try:
        with urllib.request.urlopen(url, timeout=120) as response, temp.open("wb") as f:
            shutil.copyfileobj(response, f)
        if sha(temp.read_bytes()) != expected:
            raise ValueError("download checksum mismatch")
        temp.replace(target)
    finally:
        temp.unlink(missing_ok=True)


def pcm24(data):
    b = np.frombuffer(data, np.uint8).reshape(-1, 3).astype(np.int32)
    return ((b[:, 0] | b[:, 1] << 8 | b[:, 2] << 16) ^ 0x800000) - 0x800000


def encode_pcm24(values):
    x = values.astype(np.int32)
    return (
        np.stack([x & 255, x >> 8 & 255, x >> 16 & 255], axis=1)
        .astype(np.uint8)
        .tobytes()
    )


def wav_bytes(pcm, channels, floating=False):
    width = 4 if floating else 3
    fmt = struct.pack(
        "<HHIIHH",
        3 if floating else 1,
        channels,
        48000,
        48000 * channels * width,
        channels * width,
        width * 8,
    )
    pad = b"\0" if len(pcm) & 1 else b""
    return (
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


def build(args):
    if args.tier != "gzip":
        from pack_tiers import build_tier
        if not args.from_packs:
            raise ValueError("tier conversion requires --from-packs")
        return build_tier(args)
    cache = pathlib.Path(args.cache)
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / "CrocellKit_Stereo_MIX.rar"
    fetch(ARCHIVE_URL, archive, ARCHIVE_SHA)
    root = cache / "CrocellKit_Stereo_MIX"
    # Re-extract the verified archive; do not trust an edited extraction cache.
    members = subprocess.check_output(
        ["bsdtar", "-tf", str(archive)], text=True
    ).splitlines()
    for member in members:
        p = pathlib.PurePosixPath(member)
        if p.is_absolute() or ".." in p.parts or p.parts[0] != root.name:
            raise ValueError("unsafe archive member")
    subprocess.run(["bsdtar", "-xf", str(archive), "-C", str(cache)], check=True)
    dest = pathlib.Path(args.out)
    dest.parent.mkdir(parents=True, exist_ok=True)
    out = pathlib.Path(tempfile.mkdtemp(prefix=".full-kit-", dir=dest.parent))
    assets = {b: {} for b in ("shells", "metals")}
    zones = {b: [] for b in assets}
    articulations = []
    jobs = []
    sources = []
    for bank, name, note, instrument, layers, rr in PIECES:
        xml = root / instrument / (instrument + ".xml")
        samples = sorted(
            ET.parse(xml).getroot().find("samples"),
            key=lambda s: (float(s.attrib["power"]), s.attrib["name"]),
        )
        if len(samples) < layers * rr:
            raise ValueError("not enough distinct recordings: " + instrument)
        selected = [
            samples[round(i * (len(samples) - 1) / (layers * rr - 1))]
            for i in range(layers * rr)
        ]
        centers = [round(16 + i * 104 / (layers - 1)) for i in range(layers)]
        for i, s in enumerate(selected):
            path = root / instrument / s[0].attrib["file"]
            ident = instrument.lower() + "-" + path.stem.split("-")[0]
            z = zone(note, centers[i // rr], i % rr, rr)
            z.update(
                asset=ident,
                Group=len(articulations),
                OneShot=True,
                ChokeGroup=1 if name.startswith("hat_") else CHOKES.get(name, 0),
                ChokeSustain=name in CHOKES and not name.endswith("stopped"),
            )
            zones[bank].append(z)
            jobs.append((bank, ident, path, False))
            sources.append(
                dict(
                    asset=ident,
                    bank=bank,
                    member=path.relative_to(cache).as_posix(),
                    power=float(s.attrib["power"]),
                    xml_sha256=sha(xml.read_bytes()),
                )
            )
        articulations.append(
            dict(
                name=name,
                bank="full-kit-" + bank,
                note=note,
                layers=layers,
                round_robins=rr,
                source=instrument,
                kind="recorded",
                choke_group=z["ChokeGroup"],
            )
        )
    # Explicitly labeled CC0 cross-stick; keep the actual one dynamic/two takes.
    vcsl_license = f"https://github.com/sgossner/VCSL/blob/{VCSL_PIN}/README.md"
    for rr in (1, 2):
        p = f"Membranophones/Struck Membranophones/Snare Drum, Modern 3/Snare4_Xstick_v2_rr{rr}_Mid.wav"
        url = (
            f"https://raw.githubusercontent.com/sgossner/VCSL/{VCSL_PIN}/"
            + urllib.parse.quote(p, safe="/")
        )
        target = cache / (f"vcsl-xstick-{rr}.wav")
        # Expected source pins are checked against the published selection on rebuild.
        if not target.exists():
            with urllib.request.urlopen(url, timeout=120) as response:
                target.write_bytes(response.read())
        ident = f"cross-stick-{rr}"
        z = zone(37, 64, rr - 1, 2)
        z.update(asset=ident, Group=23, OneShot=True, ChokeGroup=0)
        zones["shells"].append(z)
        jobs.append(("shells", ident, target, True))
        sources.append(
            dict(asset=ident, bank="shells", url=url, license_url=vcsl_license)
        )
    articulations.append(
        dict(
            name="snare_cross_stick",
            bank="full-kit-shells",
            note=37,
            layers=1,
            round_robins=2,
            source="VCSL Snare4 Xstick",
            kind="recorded",
            choke_group=0,
        )
    )
    # These are mappings of existing hits, not extra recordings or synthetic takes.
    for bank, base, name, note, layers, rr, group in [
        ("shells", "snare_center", "snare_ghost", 34, 2, 3, 24),
        ("metals", "ride_bow", "ride_crash", 59, 2, 2, 25),
    ]:
        art = next(a for a in articulations if a["name"] == base)
        candidates = [z for z in zones[bank] if z["Root"] == art["note"]]
        available = sorted({z["Layer"] for z in candidates})
        chosen = available[:layers] if name == "snare_ghost" else available[-layers:]
        for z in candidates:
            if z["Layer"] in chosen:
                zones[bank].append(
                    dict(
                        z,
                        Root=note,
                        KeyLow=note,
                        KeyHigh=note,
                        Group=group,
                        Layer=32 if z["Layer"] == chosen[0] else 112,
                    )
                )
        articulations.append(
            dict(
                name=name,
                bank="full-kit-" + bank,
                note=note,
                layers=layers,
                round_robins=rr,
                source=art["source"],
                kind="mapped subset",
                choke_group=art["choke_group"],
                limitation="Quiet center hits"
                if name == "snare_ghost"
                else "Hard bow hits; no separately labeled edge/crash-ride recordings",
            )
        )
    # GM has six tom notes; four physical drums supply its neighboring aliases.
    for base, name, note, group in [
        ("tom_mid", "tom_mid_gm", 47, 32),
        ("tom_high", "tom_high_gm", 50, 33),
    ]:
        art = next(a for a in articulations if a["name"] == base)
        for z in list(zones["shells"]):
            if z["Root"] == art["note"]:
                zones["shells"].append(
                    dict(z, Root=note, KeyLow=note, KeyHigh=note, Group=group)
                )
        articulations.append(dict(art, name=name, note=note, kind="GM alias"))
    for base, name, note, group in [
        ("crash_left", "crash_left_choke", 26, 30),
        ("crash_right", "crash_right_choke", 27, 31),
        ("splash", "splash_choke", 28, 26),
        ("splash_right", "splash_right_choke", 29, 27),
        ("china", "china_choke", 30, 28),
        ("ride_bow", "ride_choke", 33, 29),
    ]:
        art = next(a for a in articulations if a["name"] == base)
        template = next(z for z in zones["metals"] if z["Root"] == art["note"])
        zones["metals"].append(
            dict(
                template,
                Root=note,
                KeyLow=note,
                KeyHigh=note,
                Group=group,
                Position=0,
                Count=1,
                Layer=64,
                Gain=0,
                ChokeSustain=False,
                End=96,
            )
        )
        articulations.append(
            dict(
                name=name,
                bank="full-kit-metals",
                note=note,
                layers=0,
                round_robins=0,
                source=art["source"],
                kind="silent choke control",
                choke_group=art["choke_group"],
            )
        )

    by_id = {(s["bank"], s["asset"]): s for s in sources}

    def convert(job):
        bank, ident, path, floating = job
        src = path.read_bytes()
        if floating:
            raw = subprocess.check_output(
                [
                    "ffmpeg",
                    "-v",
                    "error",
                    "-nostdin",
                    "-i",
                    str(path),
                    "-ar",
                    "48000",
                    "-ac",
                    "2",
                    "-f",
                    "f32le",
                    "-threads",
                    "1",
                    "pipe:1",
                ]
            )
            pcm = np.frombuffer(raw, "<f4").reshape(-1, 2).copy()
            frames = len(pcm)
            fade = min(240, frames)
            pcm[-fade:] *= np.linspace(1, 0, fade, dtype=np.float32)[:, None]
            raw = pcm.astype("<f4").tobytes()
        else:
            with wave.open(str(path)) as w:
                if (w.getframerate(), w.getnchannels(), w.getsampwidth()) != (
                    48000,
                    2,
                    3,
                ):
                    raise ValueError("unexpected stereo source format")
                frames = w.getnframes()
                raw = w.readframes(frames)
            pcm = pcm24(raw).reshape(-1, 2)
            fade = min(240, frames)
            pcm[-fade:] = np.rint(
                pcm[-fade:] * np.linspace(1, 0, fade)[:, None]
            ).astype(np.int32)
            raw = encode_pcm24(pcm.reshape(-1))
        wav = wav_bytes(raw, 2, floating)
        zipped = canonical_gzip(wav)
        folder = out / ("full-kit-" + bank) / "samples"
        folder.mkdir(parents=True, exist_ok=True)
        (folder / (ident + ".wav.gz")).write_bytes(zipped)
        meta = by_id[bank, ident]
        a = dict(
            id=ident,
            path="samples/" + ident + ".wav.gz",
            sha256=sha(zipped),
            bytes=len(zipped),
            wav_sha256=sha(wav),
            wav_bytes=len(wav),
            frames=frames,
            rate=48000,
            channels=2,
            source_url=meta.get(
                "url",
                ARCHIVE_URL
                + "#"
                + urllib.parse.quote(meta.get("member", ""), safe="/"),
            ),
            source_sha256=sha(src),
            license="CC0-1.0" if floating else "CC-BY-4.0",
            license_url=meta.get("license_url", CREDITS_URL),
        )
        if not floating:
            a["attribution"] = ATTRIBUTION
        return bank, a

    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
            for bank, asset in pool.map(convert, jobs):
                assets[bank][asset["id"]] = asset
        catalog = dict(
            format="cicada.full-kit/1",
            source_archive=dict(
                url=ARCHIVE_URL,
                sha256=ARCHIVE_SHA,
                bytes=archive.stat().st_size,
                license="CC-BY-4.0",
                credits_url=CREDITS_URL,
            ),
            processing="48 kHz stereo, no decay trimming or normalization; 5 ms ending fade; source power-ranked hits partitioned into velocity/take bands",
            articulations=articulations,
            selections=sources,
            packs=[],
        )
        for bank in assets:
            name = "full-kit-" + bank
            aa = sorted(assets[bank].values(), key=lambda a: a["id"])
            zz = sorted(
                zones[bank], key=lambda z: (z["Root"], z["Layer"], z["Position"])
            )
            m = dict(
                format="cicada.instrument-pack/1",
                id=name,
                description="Full acoustic kit "
                + bank
                + "; recorded dynamics/takes and stereo room.",
                config=config(voices=16, release=1000),
                assets=aa,
                zones=zz,
            )
            data = json_bytes(m)
            (out / name / "manifest.json").write_bytes(data)
            pcm_bytes = sum(a["frames"] * 8 for a in aa)
            if pcm_bytes > 256 << 20:
                raise ValueError("bank PCM exceeds 256 MiB: " + name)
            catalog["packs"].append(
                dict(
                    id=name,
                    manifest=name + "/manifest.json",
                    sha256=sha(data),
                    assets=len(aa),
                    zones=len(zz),
                    compressed_bytes=sum(a["bytes"] for a in aa),
                    pcm_bytes=pcm_bytes,
                )
            )
        data = json_bytes(catalog)
        (out / "catalog.json").write_bytes(data)
        if args.verify:
            pinned = pathlib.Path(args.verify)
            if data != pinned.read_bytes():
                raise ValueError("rebuilt catalog differs from the published pin")
            for p in catalog["packs"]:
                if (out / p["manifest"]).read_bytes() != (
                    pinned.parent / p["manifest"]
                ).read_bytes():
                    raise ValueError("rebuilt manifest differs from the published pin")
        if dest.exists():
            for f in out.rglob("*"):
                if f.is_file() and (
                    not (dest / f.relative_to(out)).is_file()
                    or f.read_bytes() != (dest / f.relative_to(out)).read_bytes()
                ):
                    raise ValueError(
                        "refusing to replace different or corrupt destination"
                    )
        else:
            out.replace(dest)
        print(json.dumps(catalog["packs"], indent=2))
    finally:
        if out.exists():
            shutil.rmtree(out)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--out", required=True)
    p.add_argument("--cache", required=True)
    p.add_argument("--verify", help="published catalog.json to verify on rebuild")
    p.add_argument("--jobs", type=int, default=4)
    from pack_tiers import tier_arguments
    tier_arguments(p, "assets/sampler/full-kit/catalog.json")
    build(p.parse_args())
