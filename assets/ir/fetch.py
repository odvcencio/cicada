#!/usr/bin/env python3
"""Fetch the pinned impulse pack and its redistribution notice."""

import argparse
import hashlib
import json
import pathlib
import shutil
import urllib.request

LICENSE_SHA256 = "77103419866b95f897f48af44d714cd44acfcc88f8347f6eedb40fae4cd90d1c"
MAX_BYTES = 16 << 20


def fetch(url, checksum, destination, expected_bytes=None):
    temporary = destination.with_suffix(destination.suffix + ".part")
    try:
        digest = hashlib.sha256()
        size = 0
        with urllib.request.urlopen(url, timeout=60) as source, temporary.open("wb") as output:
            while True:
                data = source.read(65536)
                if not data:
                    break
                size += len(data)
                if size > (expected_bytes if expected_bytes is not None else MAX_BYTES):
                    raise ValueError("transfer exceeds declared byte count")
                digest.update(data)
                output.write(data)
        if expected_bytes is not None and size != expected_bytes:
            raise ValueError("transfer differs from declared byte count")
        if digest.hexdigest() != checksum:
            raise ValueError("transfer checksum mismatch")
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=pathlib.Path)
    args = parser.parse_args()
    manifest_path = pathlib.Path(__file__).with_name("manifest.json")
    manifest = json.loads(manifest_path.read_text())
    args.destination.mkdir(parents=True, exist_ok=True)
    # All selected responses share this exact upstream license notice. Keep it
    # with the audio whenever the fetched pack or listening examples travel.
    fetch(manifest["assets"][0]["license_url"], LICENSE_SHA256, args.destination / "COPYING")
    for entry in manifest["assets"]:
        fetch(entry["url"], entry["sha256"], args.destination / (entry["id"] + ".wav"), entry["bytes"])
        print(entry["id"] + ": checksum verified")
    shutil.copyfile(manifest_path, args.destination / "manifest.json")


if __name__ == "__main__":
    main()
