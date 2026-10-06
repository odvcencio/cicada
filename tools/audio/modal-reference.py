#!/usr/bin/env python3
"""Compare a modeled marimba hit with the separately fetched CC0 reference.

Requires Python 3 and NumPy. Render one model_marimba MIDI 72 hit at velocity
96 with a natural tail; pass that WAV and the downloaded reference WAV:

    python3 tools/audio/modal-reference.py --model model.wav \
        --reference reference.wav --output comparison.json

The source labels its approximately 524 Hz note C4. MIDI 72 compensates for
that octave naming convention; it is C5 in scientific pitch notation. The
comparison reports differences without treating one recording as a realism
gate. It does not write or fetch assets.
"""

import argparse
import hashlib
import json
import math
from pathlib import Path
import struct

import numpy as np


def wav(path):
    raw = Path(path).read_bytes()
    if raw[:4] != b"RIFF" or raw[8:12] != b"WAVE":
        raise ValueError("Expected a RIFF WAV")
    fmt, payload = None, None
    offset = 12
    while offset + 8 <= len(raw):
        name, length = struct.unpack_from("<4sI", raw, offset)
        start = offset + 8
        if start + length > len(raw):
            raise ValueError("WAV chunk exceeds file size")
        if name == b"fmt " and length >= 16:
            fmt = struct.unpack_from("<HHIIHH", raw, start)
        elif name == b"data":
            payload = raw[start : start + length]
        offset = start + length + (length & 1)
    if fmt is None or payload is None:
        raise ValueError("Missing WAV fmt or data")
    encoding, channels, rate, _, alignment, bits = fmt
    if channels not in (1, 2) or alignment != channels * bits // 8:
        raise ValueError("Expected consistent mono or stereo WAV")
    if encoding == 3 and bits == 32:
        data = np.frombuffer(payload, dtype="<f4").astype(np.float64)
    elif encoding == 1 and bits in (16, 32):
        data = np.frombuffer(payload, dtype=f"<i{bits // 8}").astype(np.float64)
        data /= 2 ** (bits - 1)
    elif encoding == 1 and bits == 24:
        packed = np.frombuffer(payload, dtype=np.uint8).reshape(-1, 3)
        integer = (packed[:, 0].astype(np.int32)
                   | packed[:, 1].astype(np.int32) << 8
                   | packed[:, 2].astype(np.int32) << 16)
        integer = (integer ^ 0x800000) - 0x800000
        data = integer.astype(np.float64) / 8388608
    else:
        raise ValueError("Expected PCM 16/24/32 or float32 WAV")
    data = data.reshape(-1, channels).mean(axis=1)
    if not len(data) or not np.isfinite(data).all() or not np.max(np.abs(data)):
        raise ValueError("Expected finite, audible WAV")
    onset = np.flatnonzero(np.abs(data) >= np.max(np.abs(data)) * .001)[0]
    return data[onset:], rate, onset, hashlib.sha256(raw).hexdigest()


def spectral(data, rate, fundamental):
    start = int(.01 * rate)
    signal = data[start : start + int(.5 * rate)]
    if len(signal) < int(.1 * rate):
        raise ValueError("Reference and model must contain at least 110 ms")
    fft_size = 1 << max(18, (len(signal) - 1).bit_length())
    power = np.abs(np.fft.rfft(signal * np.hanning(len(signal)), fft_size)) ** 2
    frequencies = np.fft.rfftfreq(fft_size, 1 / rate)

    def peak(target):
        candidates = np.flatnonzero((frequencies >= target * .94)
                                    & (frequencies <= target * 1.06))
        index = candidates[np.argmax(power[candidates])]
        a, b, c = np.log(np.maximum(power[index - 1 : index + 2], 1e-300))
        offset = .5 * (a - c) / (a - 2 * b + c)
        return float((index + offset) * rate / fft_size)

    measured = peak(fundamental)
    modes = []
    for ratio in (1, 4, 9.9, 17.1, 24.5, 33.2):
        if measured * ratio * 1.06 >= min(20000, .45 * rate):
            break
        frequency = peak(measured * ratio)
        modes.append({"nominal_ratio": ratio, "frequency_hz": frequency,
                      "ratio_to_fundamental": frequency / measured})
    mask = frequencies <= min(20000, .45 * rate)
    centroid = np.sum(frequencies[mask] * power[mask]) / np.sum(power[mask])
    total = np.sum(power[mask])
    bands = []
    for low, high in ((0, 1000), (1000, 3000), (3000, 8000), (8000, 20000)):
        energy = np.sum(power[mask & (frequencies >= low) & (frequencies < high)])
        bands.append({"low_hz": low, "high_hz": high,
                      "power_fraction": float(energy / total)})
    return {"fundamental_hz": measured, "mode_peaks": modes,
            "power_centroid_hz": float(centroid), "bands": bands}


def envelope(data, rate):
    frames = int(.02 * rate)
    complete = len(data) // frames
    if complete < 10:
        raise ValueError("Need at least 200 ms for an envelope comparison")
    levels = np.sqrt(np.mean(data[: complete * frames].reshape(-1, frames) ** 2,
                             axis=1))
    levels /= np.max(levels)
    times = (np.arange(complete) + .5) * frames / rate
    db = 20 * np.log10(np.maximum(levels, 1e-15))
    # Broadband RMS T60 is an extrapolation from the -5 to -25 dB region.
    # Room noise and faster upper-mode damping can change this fit.
    after_peak = np.arange(complete) >= np.argmax(levels)
    selection = after_peak & (db <= -5) & (db >= -25)
    t60, fit_quality = None, None
    if np.count_nonzero(selection) >= 5:
        slope, intercept = np.polyfit(times[selection], db[selection], 1)
        fit = slope * times[selection] + intercept
        denominator = np.sum((db[selection] - np.mean(db[selection])) ** 2)
        fit_quality = float(1 - np.sum((db[selection] - fit) ** 2) / denominator)
        if slope < 0:
            t60 = float(-60 / slope)
    t20 = None
    for i in range(int(np.argmax(levels)), complete - 4):
        if np.all(db[i : i + 5] <= -20):
            t20 = float(times[i])
            break
    return levels, times, {"rms_t60_extrapolated_s": t60,
                          "decay_fit_r_squared": fit_quality,
                          "sustained_minus20db_time_s": t20}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True)
    parser.add_argument("--reference", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--manifest", default=str(Path(__file__).resolve().parents[2]
                                                  / "assets/reference/marimba.json"))
    parser.add_argument("--model-velocity", type=int, default=96)
    args = parser.parse_args()
    asset = json.loads(Path(args.manifest).read_text())["assets"][0]
    reference, reference_rate, reference_onset, checksum = wav(args.reference)
    if checksum != asset["sha256"]:
        raise ValueError("Reference checksum does not match the license manifest")
    model, model_rate, model_onset, model_checksum = wav(args.model)
    note = asset["comparison_midi_note"]
    authored = 440 * 2 ** ((note - 69) / 12)
    spectra = {"reference": spectral(reference, reference_rate, authored),
               "model": spectral(model, model_rate, authored)}
    reference_env, reference_times, reference_decay = envelope(reference, reference_rate)
    model_env, model_times, model_decay = envelope(model, model_rate)
    duration = min(reference_times[-1], model_times[-1])
    times = np.arange(.01, duration, .02)
    reference_levels = np.interp(times, reference_times, reference_env)
    model_levels = np.interp(times, model_times, model_env)
    envelope_error = float(np.sqrt(np.mean((model_levels - reference_levels) ** 2)))
    fundamental_cents = 1200 * math.log2(spectra["model"]["fundamental_hz"]
                                         / spectra["reference"]["fundamental_hz"])
    report = {
        "version": 1,
        "reference_asset_id": asset["id"],
        "reference_sha256": checksum,
        "model_sha256": model_checksum,
        "comparison_midi_note": note,
        "model_velocity": args.model_velocity,
        "source_note_label": asset["source_note_label"],
        "scientific_note_label": asset["scientific_note_label"],
        "octave_compensation": asset["octave_compensation"],
        "method": {
            "mixdown": "arithmetic mean of channels",
            "onset": "first sample within 60 dB of waveform peak",
            "spectrum_start_s": .01,
            "spectrum_duration_s": .5,
            "spectral_window": "Hann at each WAV's native sample rate",
            "mode_peak_search": "maximum within 6 percent of each nominal ratio; not proof of mode identity",
            "envelope_window_s": .02,
            "envelope_normalization": "separate peak RMS per waveform",
            "envelope_comparison_duration_s": float(duration),
            "t60": "broadband RMS extrapolation from -5 to -25 dB",
        },
        "reference_rate_hz": reference_rate,
        "model_rate_hz": model_rate,
        "reference_onset_frame": int(reference_onset),
        "model_onset_frame": int(model_onset),
        "spectra": spectra,
        "reference_decay": reference_decay,
        "model_decay": model_decay,
        "differences": {
            "model_minus_reference_fundamental_cents": fundamental_cents,
            "model_minus_reference_centroid_hz": spectra["model"]["power_centroid_hz"]
                                                  - spectra["reference"]["power_centroid_hz"],
            "normalized_envelope_rms_error": envelope_error,
        },
        "limits": [
            "One recording, one authored note, one model velocity and strike variation.",
            "The reference includes microphone, room and capture noise.",
            "RMS envelopes are onset aligned and separately normalized; waveform phases are not fitted.",
            "Broadband fitted T60 differs from an isolated resonator's modal T60.",
            "Measured differences do not establish perceptual realism or DAW equivalence.",
        ],
    }
    Path(args.output).write_text(json.dumps(report, indent=2, allow_nan=False) + "\n")
    print(json.dumps(report["differences"], indent=2))


if __name__ == "__main__":
    main()
