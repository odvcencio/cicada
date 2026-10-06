#!/usr/bin/env python3
"""Measure licensed source fidelity, envelope, spectrum and recorded tail noise."""

import argparse
import gzip
import hashlib
import json
import pathlib
import subprocess
import urllib.parse
import wave

import numpy as np

from build_full_kit import pcm24


def db(value):
    return 20 * np.log10(max(float(value), 1e-30))


def rms(values):
    return float(np.sqrt(np.mean(values * values)))


def main(args):
    packs = pathlib.Path(args.packs)
    catalog = json.loads((packs / "catalog.json").read_text())
    sources = pathlib.Path(args.sources)
    renders = pathlib.Path(args.renders)
    metrics = []
    references = []
    for bank in catalog["packs"]:
        m = json.loads((packs / bank["manifest"]).read_text())
        for a in m["assets"]:
            if a["license"] == "CC0-1.0":
                path = sources / ("vcsl-xstick-" + a["id"].split("-")[-1] + ".wav")
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
                original = np.frombuffer(raw, "<f4").reshape(-1, 2).astype(np.float64)
            else:
                path = sources / urllib.parse.unquote(
                    urllib.parse.urlsplit(a["source_url"]).fragment
                )
                with wave.open(str(path)) as w:
                    original = (
                        pcm24(w.readframes(w.getnframes())).reshape(-1, 2) / 8388608
                    ).astype(np.float64)
            if hashlib.sha256(path.read_bytes()).hexdigest() != a["source_sha256"]:
                raise ValueError("source checksum mismatch")
            zipped = (packs / bank["id"] / a["path"]).read_bytes()
            wav = gzip.decompress(zipped)
            if (
                hashlib.sha256(zipped).hexdigest() != a["sha256"]
                or hashlib.sha256(wav).hexdigest() != a["wav_sha256"]
            ):
                raise ValueError("asset checksum mismatch")
            prepared = (
                np.frombuffer(wav[44:], "<f4").reshape(-1, 2).astype(np.float64)
                if a["license"] == "CC0-1.0"
                else (
                    pcm24(wav[44 : 44 + a["frames"] * 6]).reshape(-1, 2) / 8388608
                ).astype(np.float64)
            )
            if prepared.shape != original.shape or len(original) != a["frames"]:
                raise ValueError("source decay truncated")
            body = slice(128, len(original) - 240)
            signal = rms(original[body])
            conversion_error = rms(prepared[body] - original[body])
            if conversion_error > signal * 10 ** (-90 / 20):
                raise ValueError("conversion residual exceeds -90 dB")
            metrics.append(
                dict(
                    asset=a["id"],
                    bank=bank["id"],
                    duration_seconds=a["frames"] / 48000,
                    source_relative_residual_db=db(
                        conversion_error / max(signal, 1e-30)
                    ),
                    source_peak_dbfs=db(np.max(np.abs(original))),
                    source_dc_dbfs=db(abs(np.mean(original))),
                    source_tail_100ms_rms_dbfs=db(rms(original[-4800:])),
                    source_sha256=a["source_sha256"],
                )
            )
            render = renders / (bank["id"] + "--" + a["id"] + ".f32")
            if render.exists():
                got = np.fromfile(render, "<f4").reshape(-1, 2).astype(np.float64)[body]
                ref = original[body]
                n = min(32768, len(ref))
                window = np.hanning(n)[:, None]
                sref = np.abs(np.fft.rfft(ref[:n] * window, axis=0))
                sgot = np.abs(np.fft.rfft(got[:n] * window, axis=0))
                mask = sref > np.max(sref) * 0.001
                spectral = float(
                    np.max(
                        np.abs(
                            20 * np.log10(np.maximum(sgot[mask], 1e-30) / sref[mask])
                        )
                    )
                )
                blocks = len(ref) // 960
                envref = np.sqrt(
                    np.mean(
                        ref[: blocks * 960].reshape(blocks, 960, 2) ** 2, axis=(1, 2)
                    )
                )
                envgot = np.sqrt(
                    np.mean(
                        got[: blocks * 960].reshape(blocks, 960, 2) ** 2, axis=(1, 2)
                    )
                )
                valid = envref > signal * 0.01
                envelope = float(
                    np.max(
                        np.abs(
                            20
                            * np.log10(np.maximum(envgot[valid], 1e-30) / envref[valid])
                        )
                    )
                )
                residual = db(rms(got - ref) / max(signal, 1e-30))
                if residual > -90 or spectral > 0.01 or envelope > 0.01:
                    raise ValueError("licensed source/player reference miss")
                references.append(
                    dict(
                        asset=a["id"],
                        bank=bank["id"],
                        residual_db=residual,
                        max_spectral_error_db_above_minus60=spectral,
                        max_20ms_envelope_error_db_above_minus40=envelope,
                        source_url=a["source_url"],
                        source_sha256=a["source_sha256"],
                    )
                )
    if len(references) < 20:
        raise ValueError("reference selections incomplete")
    report = dict(
        reference="Licensed source WAV; native pitch; first 128 frames and final 5 ms fade excluded. No decay trimming. Tail RMS includes musical decay, not an isolated recording of room noise.",
        assets=metrics,
        references=references,
        summary=dict(
            assets=len(metrics),
            reference_takes=len(references),
            worst_residual_db=max(r["residual_db"] for r in references),
            worst_spectral_error_db=max(
                r["max_spectral_error_db_above_minus60"] for r in references
            ),
            worst_envelope_error_db=max(
                r["max_20ms_envelope_error_db_above_minus40"] for r in references
            ),
            max_source_duration_seconds=max(r["duration_seconds"] for r in metrics),
        ),
    )
    pathlib.Path(args.out).write_text(
        json.dumps(report, indent=2, sort_keys=True) + "\n"
    )
    print(json.dumps(report["summary"], indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--packs", default="build/kit-final/packs")
    p.add_argument("--sources", default="build/kit-source-cache")
    p.add_argument("--renders", default="build/kit-reports/reference")
    p.add_argument("--out", default="build/kit-reports/source-metrics.json")
    main(p.parse_args())
