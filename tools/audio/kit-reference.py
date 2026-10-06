#!/usr/bin/env python3
"""Fetch pinned CC0 kit references and compare spectra, envelopes and rates.

Requires NumPy. Generate model WAVs with kit-images.go first. Reference audio
stays outside the repository. Every fetched or reused source is checksummed.
Example: python3 tools/audio/kit-reference.py --models images --references refs
         --output comparison.json
"""

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import urllib.request

import numpy as np

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("modal_reference", Path(__file__).with_name("modal-reference.py"))
reader = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reader)


def spectral(data, rate):
    signal = data[:min(len(data), int(.5 * rate))]
    size = 1 << max(16, (len(signal)-1).bit_length())
    power = np.abs(np.fft.rfft(signal * np.hanning(len(signal)), size))**2
    frequencies = np.fft.rfftfreq(size, 1/rate)
    total = np.sum(power)
    if total <= 0:
        raise ValueError("Silent spectral probe")
    bands = []
    for low,high in ((0,80),(80,250),(250,1000),(1000,3000),(3000,8000),(8000,20000)):
        energy = np.sum(power[(frequencies>=low)&(frequencies<high)])
        bands.append(dict(low_hz=low,high_hz=high,power_fraction=float(energy/total)))
    return dict(power_centroid_hz=float(np.sum(frequencies*power)/total),bands=bands,
                power_above_045_rate_fraction=float(np.sum(power[frequencies>.45*rate])/total))


def envelope(data, rate):
    frames = max(1,round(.005*rate))
    count = len(data)//frames
    levels = np.sqrt(np.mean(data[:count*frames].reshape(count,frames)**2,axis=1))
    normalized = levels/np.max(levels)
    times = (np.arange(count)+.5)*frames/rate
    decibels = 20*np.log10(np.maximum(normalized,1e-15))
    peak_index = int(np.argmax(levels))
    t20 = None
    for i in range(peak_index,count-20):
        if np.all(decibels[i:i+20]<=-20):
            t20=float(times[i]);break
    selection = (np.arange(count)>peak_index)&(decibels>=-25)&(decibels<=-5)
    t60,r_squared = None,None
    if np.count_nonzero(selection)>=5:
        slope,intercept=np.polyfit(times[selection],decibels[selection],1)
        error=np.sum((slope*times[selection]+intercept-decibels[selection])**2)
        total=np.sum((decibels[selection]-np.mean(decibels[selection]))**2)
        r_squared=float(1-error/total) if total else None
        if slope<0:t60=float(-60/slope)
    tail=data[-min(len(data),int(.1*rate)):]
    return normalized,times,dict(peak_rms=float(np.max(levels)),
        sustained_minus20db_time_s=t20,rms_t60_extrapolated_s=t60,
        decay_fit_r_squared=r_squared,last_100ms_rms=float(np.sqrt(np.mean(tail**2))),
        last_100ms_dc=float(np.mean(tail)),duration_from_onset_s=len(data)/rate)


def rate_comparison(low,high):
    # Explicit zero-phase FFT low-pass before 2:1 decimation. This is a rendered
    # residual measurement, not proof that stochastic attack energy is aliasing.
    size=1 << ((len(high)-1).bit_length())
    spectrum=np.fft.rfft(high,size)
    frequencies=np.fft.rfftfreq(size,1/96000)
    spectrum[frequencies>=21600]=0
    down=np.fft.irfft(spectrum,size)[:len(high):2]
    length=min(len(low),len(down),48000)
    reference=down[:length];candidate=low[:length]
    denominator=float(np.dot(reference,reference))
    gain=float(np.dot(reference,candidate)/denominator) if denominator else 0
    difference=candidate-gain*reference
    error=float(np.dot(difference,difference))
    total=float(np.dot(candidate,candidate))
    return dict(compared_seconds=length/48000,gain_fit=gain,
                normalized_residual_rms=float(np.sqrt(error/max(total,1e-300))),
                residual_to_signal_db=float(10*np.log10(max(error,1e-300)/max(total,1e-300))))


def fetch(asset,directory,cache):
    destination=directory/asset["file"]
    if not destination.exists():
        cached=(cache/(hashlib.sha256(asset["source_url"].encode()).hexdigest()+".wav")) if cache else None
        if cached and cached.exists():
            shutil.copyfile(cached,destination)
        else:
            with urllib.request.urlopen(asset["source_url"],timeout=60) as response:
                destination.write_bytes(response.read())
    if hashlib.sha256(destination.read_bytes()).hexdigest()!=asset["sha256"]:
        raise ValueError(f'Reference checksum mismatch: {asset["id"]}')
    return destination


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--models",type=Path,required=True)
    parser.add_argument("--references",type=Path,required=True)
    parser.add_argument("--source-cache",type=Path)
    parser.add_argument("--output",type=Path,required=True)
    args=parser.parse_args()
    manifest=json.loads((ROOT/"assets/reference/drum-kit.json").read_text())
    args.references.mkdir(parents=True,exist_ok=True)
    reports=[]
    for asset in manifest["assets"]:
        reference,rr,ro,rhash=reader.wav(fetch(asset,args.references,args.source_cache))
        model,mr,mo,mhash=reader.wav(args.models/f'model-{asset["id"]}-48000.wav')
        high,hr,ho,hhash=reader.wav(args.models/f'model-{asset["id"]}-96000.wav')
        if mr!=48000 or hr!=96000:raise ValueError("Wrong model probe rates")
        rs,ms=spectral(reference,rr),spectral(model,mr)
        rl,rt,re=envelope(reference,rr);ml,mt,me=envelope(model,mr)
        duration=min(rt[-1],mt[-1]);times=np.arange(.0025,duration,.005)
        residual=np.interp(times,mt,ml)-np.interp(times,rt,rl)
        reports.append(dict(id=asset["id"],model=asset["model"],
            comparison_velocity=asset["comparison_velocity"],
            reference_sha256=rhash,model_sha256=mhash,model_96k_sha256=hhash,
            reference_rate_hz=rr,model_rate_hz=mr,
            reference_onset_frame=int(ro),model_onset_frame=int(mo),model_96k_onset_frame=int(ho),
            spectra=dict(reference=rs,model=ms),envelopes=dict(reference=re,model=me),
            differences=dict(model_minus_reference_centroid_hz=ms["power_centroid_hz"]-rs["power_centroid_hz"],
                             normalized_envelope_rms_error=float(np.sqrt(np.mean(residual**2)))),
            rendered_rate_residual=rate_comparison(model,high)))
    report=dict(version=1,license_manifest="assets/reference/drum-kit.json",comparisons=reports,
        method=dict(mixdown="arithmetic mean of channels",onset="first sample within 60 dB of waveform peak",
            spectrum="first 500 ms after onset; Hann window at native rate; separately normalized bands",
            envelope="5 ms RMS windows; separately peak normalized; onset aligned",
            t60="broadband RMS extrapolation from -5 to -25 dB; report fit quality",
            rate_residual="onset aligned; 96 kHz FFT low-pass at 21.6 kHz; 2:1 decimation; first second; fitted scalar gain"),
        limits=["Single reference take and model variation per piece; unequal recording dynamics.",
            "Reference includes microphone, room and capture noise; no room is synthesized in this probe.",
            "Suspended cymbal stick reference is related to a ride, not a dimension-matched ride recording.",
            "Rendered rate residual also includes rate-dependent stochastic excitation and omitted modes; it is not an isolated alias rejection number.",
            "Measured differences do not establish perceptual realism or equivalence to a DAW instrument."])
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps(report,indent=2,allow_nan=False)+"\n")
    shutil.copyfile(ROOT/"assets/reference/drum-kit.json",args.references/"license-manifest.json")
    print(json.dumps([{k:c[k] for k in ("id","differences","rendered_rate_residual")} for c in reports],indent=2))


if __name__=="__main__":
    main()
