#!/usr/bin/env python3
"""Measure rendered velocity, strike position, choke and exact tail silence.

Requires NumPy and the auditions produced by kit-images.go. Measurements are
post-mixer/post-limiter. Unit tests separately check the DSP's 5 ms choke fade.
"""
import argparse
import importlib.util
import json
from pathlib import Path

import numpy as np

spec=importlib.util.spec_from_file_location("kit_reference",Path(__file__).with_name("kit-reference.py"))
reference=importlib.util.module_from_spec(spec)
spec.loader.exec_module(reference)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--auditions",type=Path,required=True)
    parser.add_argument("--output",type=Path,required=True)
    args=parser.parse_args()
    velocity=[]
    for name in ("kick","snare","hat_closed","ride_bow"):
        data,rate,onset,_=reference.reader.wav(args.auditions/f"velocity-{name}.wav")
        levels=[]
        for i,value in enumerate((24,48,80,110)):
            signal=data[i*2*rate:i*2*rate+rate]
            rms=float(np.sqrt(np.mean(signal**2)))
            levels.append(dict(velocity=value,rms=rms,rms_dbfs=float(20*np.log10(rms))))
        passed=all(a["rms"]<b["rms"] for a,b in zip(levels,levels[1:]))
        if not passed:raise ValueError(f"Non-monotonic rendered velocity response: {name}")
        velocity.append(dict(model=name,window_seconds=1,levels=levels,monotonic=True))
    positions=[]
    for value in (0,1):
        data,rate,_,_=reference.reader.wav(args.auditions/f"position-snare-{value}.wav")
        positions.append(dict(position=value,spectrum=reference.spectral(data,rate)))
    centroid_difference=positions[1]["spectrum"]["power_centroid_hz"]-positions[0]["spectrum"]["power_centroid_hz"]
    if abs(centroid_difference)<1:raise ValueError("Position did not change rendered spectral balance")
    choke=[]
    for name in ("crash","splash","hat-lane2","hat-lane4"):
        data,rate,onset,_=reference.reader.wav(args.auditions/f"choke-{name}.wav")
        last=int(np.flatnonzero(data)[-1])+int(onset)
        zero_time=1 if name.startswith("hat-") else .6
        tail=data[max(0,round(zero_time*rate)-int(onset)):]
        if np.any(tail):raise ValueError("Choke or closing-strike tail exceeded its observation window")
        choke.append(dict(model=name,event_seconds=.5,
            last_nonzero_seconds=last/rate,post_event_last_nonzero_ms=(last/rate-.5)*1000,
            exact_zero_after_seconds=zero_time,
            includes_new_closing_or_pedal_strike=name.startswith("hat-")))
    tails=[]
    for path in sorted(args.auditions.glob("audition-*.wav")):
        data,rate,onset,_=reference.reader.wav(path)
        tail=data[-round(.1*rate):]
        tails.append(dict(model=path.stem.removeprefix("audition-"),
            last_100ms_rms=float(np.sqrt(np.mean(tail**2))),last_100ms_dc=float(np.mean(tail)),
            exact_zero_last_100ms=bool(not np.any(tail))))
    report=dict(version=1,velocity=velocity,strike_position=positions,
        edge_minus_center_centroid_hz=centroid_difference,choke=choke,seven_second_tails=tails,
        limits=["Four rendered velocity sweeps; unit tests cover all fifteen articulations.",
            "Measurements include existing mixer and limiter latency.",
            "Seven-second auditions need not contain the entire longest cymbal tail; unit tests render through the model's full lifetime."])
    args.output.write_text(json.dumps(report,indent=2,allow_nan=False)+"\n")
    print(f"Velocity sweeps monotonic; snare centroid difference {centroid_difference:.1f} Hz; all four choke tails reach exact zero")


if __name__=="__main__":main()
