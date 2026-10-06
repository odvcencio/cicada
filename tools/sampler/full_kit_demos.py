#!/usr/bin/env python3
"""Write repeatable groove scores and explicit starter-kit comparison maps.

The starter directory must contain the verified kit from the CC0 catalog.
Only pack paths/pins change between paired scores; musical events stay equal.
"""

import argparse
import copy
import hashlib
import json
import pathlib
import shutil


def encoded(data):
    return (json.dumps(data, indent=2, sort_keys=True) + "\n").encode()


def pitch(note):
    return ["c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"][
        note % 12
    ] + str(note // 12 - 1)


def main(args):
    out = pathlib.Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    packs = pathlib.Path(args.packs)
    catalog = json.loads((packs / "catalog.json").read_text())
    starter = pathlib.Path(args.starter)
    old = json.loads((starter / "manifest.json").read_text())
    pins = {p["id"].split("-")[-1]: p["sha256"] for p in catalog["packs"]}
    before_pins = {}
    substitutions = []
    for bank in ("shells", "metals"):
        name = "full-kit-" + bank
        folder = out / "before-packs" / name
        folder.mkdir(parents=True, exist_ok=True)
        assets = {}
        zones = []
        for art in catalog["articulations"]:
            if art["bank"] != name:
                continue
            root = (
                (36 if art["name"].startswith(("kick", "tom")) else 38)
                if bank == "shells"
                else (
                    46
                    if art["name"]
                    in (
                        "hat_open",
                        "hat_half_open",
                        "crash_left",
                        "crash_right",
                        "splash",
                        "splash_right",
                        "china",
                    )
                    else 42
                )
            )
            source = [z for z in old["zones"] if z["Root"] == root]
            substitutions.append(
                dict(articulation=art["name"], note=art["note"], starter_note=root)
            )
            for z in source:
                z = copy.deepcopy(z)
                z.update(
                    Root=art["note"],
                    KeyLow=art["note"],
                    KeyHigh=art["note"],
                    Group=art["note"],
                    ChokeGroup=art["choke_group"],
                    ChokeSustain=bank == "metals"
                    and not art["name"].startswith("hat_")
                    and not art["name"].endswith("choke"),
                )
                if art["kind"] == "silent choke control" or art["name"].endswith(
                    "choke"
                ):
                    z["Gain"] = 0
                if art["name"] == "snare_ghost":
                    z["Gain"] = 0.25
                zones.append(z)
                assets[z["asset"]] = next(
                    a for a in old["assets"] if a["id"] == z["asset"]
                )
        m = dict(
            format=old["format"],
            id=name,
            description="Starter VCSL kit comparison with explicit nearest-piece substitutions.",
            config=old["config"],
            assets=sorted(assets.values(), key=lambda a: a["id"]),
            zones=sorted(zones, key=lambda z: (z["Root"], z["Layer"], z["Position"])),
        )
        data = encoded(m)
        (folder / "manifest.json").write_bytes(data)
        before_pins[bank] = hashlib.sha256(data).hexdigest()
        for a in assets.values():
            source = starter / a["path"]
            if hashlib.sha256(source.read_bytes()).hexdigest() != a["sha256"]:
                raise ValueError("starter asset mismatch")
            target = folder / a["path"]
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, target)
    (out / "SUBSTITUTIONS.json").write_bytes(encoded(substitutions))
    # Fixed sixteenth-note score grid. Source note notation has velocity 100;
    # quiet recorded snare mappings provide ghosts. Live MIDI and diagnostic
    # events exercise the full continuous velocity response separately.
    demos = {}
    for style in ("rock", "funk", "reggae-one-drop", "soca", "jazz-ride"):
        rows = {
            track: ["."] * 64 for track in ("kick", "snare", "toms", "hats", "cymbals")
        }

        def hit(track, bar, step, note, suffix=""):
            rows[track][bar * 16 + step] = pitch(note) + suffix

        for bar in range(4):
            if style == "rock":
                for s in (0, 6, 8, 10):
                    hit("kick", bar, s, 36)
                for s in (4, 12):
                    hit("snare", bar, s, 38)
                for s in (3, 11, 15):
                    hit("snare", bar, s, 34)
            elif style == "funk":
                for s in (0, 3, 6, 10, 14):
                    hit("kick", bar, s, 36)
                for s in (4, 12):
                    hit("snare", bar, s, 40)
                for s in (2, 7, 11, 15):
                    hit("snare", bar, s, 34)
            elif style == "reggae-one-drop":
                hit("kick", bar, 8, 36)
                hit("snare", bar, 8, 37)
                for s in (6, 14):
                    hit("snare", bar, s, 34)
            elif style == "soca":
                for s in (0, 4, 8, 12):
                    hit("kick", bar, s, 36)
                for s in (2, 6, 10, 14):
                    hit("snare", bar, s, 38)
                for s in (3, 11, 15):
                    hit("snare", bar, s, 34)
            else:
                for s in (0, 8):
                    hit("kick", bar, s, 36)
                for s in (4, 12):
                    hit("hats", bar, s, 44)
                for s in (0, 4, 7, 8, 12, 15):
                    hit("cymbals", bar, s, 53 if bar == 2 and s % 4 == 0 else 51)
                for s in (5, 10, 14):
                    hit("snare", bar, s, 34)
                hit("snare", bar, 12, 37)
            if style != "jazz-ride":
                for s in range(0, 16, 2):
                    hit("hats", bar, s, 42)
                if bar % 2:
                    hit("hats", bar, 14, 23 if style == "reggae-one-drop" else 46)
            if bar == 3:
                for s, note in zip((10, 11, 12, 13, 14, 15), (48, 48, 45, 43, 41, 41)):
                    hit("toms", bar, s, note)
            if style != "jazz-ride" and bar == 0:
                hit("cymbals", bar, 0, 49)
            if style in ("funk", "soca") and bar == 2:
                hit("cymbals", bar, 0, 55)
                hit("cymbals", bar, 3, 28)
            if style == "rock" and bar == 2:
                hit("cymbals", bar, 0, 57)
                hit("cymbals", bar, 6, 27)
        demos[style] = rows
    for style, rows in demos.items():
        tempo = {
            "rock": 104,
            "funk": 108,
            "reggae-one-drop": 80,
            "soca": 128,
            "jazz-ride": 132,
        }[style]
        for before in (False, True):
            packdir = "before-packs" if before else "packs"
            pp = before_pins if before else pins
            text = f'cicada 2\n\ntitle "Full kit: {style}"\n\ntempo {tempo}\n\nkey c major\n\nseed 4242\n\n'
            for bank in ("shells", "metals"):
                text += f'sampler {bank} {{\n  pack = "{packdir}/full-kit-{bank}/manifest.json"\n  sha256 = "{pp[bank]}"\n  root = c4\n  voices = {4 if bank == "shells" else 10}\n}}\n\n'
            for track in rows:
                bank = "shells" if track in ("kick", "snare", "toms") else "metals"
                text += f"track {track} {bank} {{ level = -12dB }}\n\n"
                text += (
                    f"pattern {track}_groove {{\n  gate = 65%\n  "
                    + " | ".join(
                        " ".join(rows[track][b * 16 : (b + 1) * 16]) for b in range(4)
                    )
                    + "\n}\n\n"
                )
            text += (
                "pattern quiet { . . . . . . . . . . . . . . . . }\n\nscene groove {\n"
            )
            text += (
                "".join(f"  {track} = {track}_groove\n" for track in rows)
                + "}\n\nscene decay {\n"
            )
            text += (
                "".join(f"  {track} = quiet\n" for track in rows)
                + "}\n\nsong {\n  groove*8\n"
            )
            if style == "jazz-ride":
                text += "  decay*12\n"
            text += "}\n"
            (out / (("before-" if before else "") + style + ".cicada")).write_text(text)
    print("Wrote five score pairs and checksummed starter substitution maps")


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--packs", default="build/kit-final/packs")
    p.add_argument("--starter", required=True)
    p.add_argument("--out", default="build/kit-final")
    main(p.parse_args())
