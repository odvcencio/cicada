"""Convert an existing pinned gzip catalog using cicada-audio-encode."""

import concurrent.futures
import gzip
import hashlib
import json
import pathlib
import shutil
import subprocess
import tempfile


def sha(data):
    return hashlib.sha256(data).hexdigest()


def json_bytes(value):
    return (json.dumps(value, indent=2, sort_keys=True) + "\n").encode()


def confined(root, relative):
    path = pathlib.PurePosixPath(relative)
    if not relative or path.is_absolute() or ".." in path.parts or "\\" in relative or str(path) != relative or ":" in relative:
        raise ValueError("invalid pack path")
    target = (root / path).resolve()
    if not target.is_relative_to(root.resolve()):
        raise ValueError("pack path escapes source directory")
    return target


def build_tier(args):
    source = pathlib.Path(args.from_packs)
    destination = pathlib.Path(args.out)
    catalog = json.loads((source / "catalog.json").read_text())
    # Tier generation starts from published gzip maps, never unpinned audio.
    pinned = json.loads(pathlib.Path(args.source_catalog).read_text())
    legacy_pins = [{k: v for k, v in p.items() if k != "tiers"} for p in pinned["packs"]]
    if catalog["packs"] != legacy_pins:
        raise ValueError("source catalog differs from published pins")
    destination.parent.mkdir(parents=True, exist_ok=True)
    stage = pathlib.Path(tempfile.mkdtemp(prefix=".pack-tier-", dir=destination.parent))
    try:
        for entry in catalog["packs"]:
            original = confined(source, entry["manifest"]).read_bytes()
            if sha(original) != entry["sha256"]:
                raise ValueError("source manifest pin mismatch")
            manifest = json.loads(original)
            folder = confined(stage, entry["id"])
            (folder / "samples").mkdir(parents=True)

            def convert(asset):
                compressed = confined(source / entry["id"], asset["path"]).read_bytes()
                if len(compressed) != asset["bytes"] or sha(compressed) != asset["sha256"]:
                    raise ValueError("source encoded hash/size mismatch")
                wav = gzip.decompress(compressed)
                if len(wav) != asset["wav_bytes"] or sha(wav) != asset["wav_sha256"]:
                    raise ValueError("source WAV hash/size mismatch")
                stem = confined(folder / "samples", asset["id"])
                with tempfile.TemporaryDirectory(prefix="pack-encode-") as temp:
                    input_path = pathlib.Path(temp) / "source.wav"
                    input_path.write_bytes(wav)
                    subprocess.run([args.encoder, "-in", str(input_path), "-out", str(stem), "-tier", args.tier], check=True, stdout=subprocess.DEVNULL)
                descriptors = list(stem.parent.glob(stem.name + ".*.json"))
                if len(descriptors) != 1:
                    raise ValueError("missing or ambiguous encoded descriptor")
                descriptor = json.loads(descriptors[0].read_text())
                encoded = descriptor["asset"]
                if descriptor["source_sha256"] != asset["wav_sha256"] or any(encoded[k] != asset[k] for k in ("frames", "rate", "channels")):
                    raise ValueError("encoded source/dimensions mismatch")
                target = pathlib.Path(str(descriptors[0])[:-5])
                data = target.read_bytes()
                if len(data) != encoded["bytes"] or sha(data) != encoded["sha256"]:
                    raise ValueError("encoded hash/size mismatch")
                updated = {k: v for k, v in asset.items() if k not in ("wav_bytes", "wav_sha256")}
                updated.update({k: v for k, v in encoded.items() if k != "decoded_bytes"})
                updated["path"] = "samples/" + target.name
                if encoded["encoding"] == "wav-gzip":
                    updated.update(wav_bytes=len(wav), wav_sha256=sha(wav))
                descriptors[0].unlink()
                return updated

            with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
                manifest["assets"] = list(pool.map(convert, manifest["assets"]))
            data = json_bytes(manifest)
            (folder / "manifest.json").write_bytes(data)
            entry.update(sha256=sha(data), compressed_bytes=sum(a["bytes"] for a in manifest["assets"]))
            print(json.dumps(entry), flush=True)
        catalog["tier"] = args.tier
        data = json_bytes(catalog)
        (stage / "catalog.json").write_bytes(data)
        verify = getattr(args, "verify_catalog", None) or getattr(args, "verify", None)
        if verify and data != pathlib.Path(verify).read_bytes():
            raise ValueError("rebuilt tier differs from published pins")
        if destination.exists():
            expected = {p.relative_to(stage) for p in stage.rglob("*") if p.is_file()}
            actual = {p.relative_to(destination) for p in destination.rglob("*") if p.is_file()}
            if expected != actual or any((stage / p).read_bytes() != (destination / p).read_bytes() for p in expected):
                raise ValueError("refusing to overwrite different or corrupt pack")
        else:
            stage.rename(destination)
    finally:
        if stage.exists():
            shutil.rmtree(stage)


def tier_arguments(parser, source_catalog):
    parser.add_argument("--tier", choices=["gzip", "lossless", "hq16"], default="gzip")
    parser.add_argument("--from-packs", help="verified gzip pack directory to convert")
    parser.add_argument("--source-catalog", default=source_catalog)
    parser.add_argument("--encoder", default="cicada-audio-encode")
