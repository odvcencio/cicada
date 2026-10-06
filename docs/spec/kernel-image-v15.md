# Unified kernel image version 15

Status: integration candidate, not a release or an allocation for other branches.
The registry was checked against main 01569e19, chord PR100 b986efe and
schedule PR103 8759eb45. All three live refs matched on 2026-10-02; no tag exists.

## Compatibility and negotiation

The magic is `CIC1`. All integers and IEEE floating-point fields use little
endian. The only accepted versions are 8, 9, 10, 11, 12, 13 and 15. Existing
8–13 readers retain their validation and layouts. Configurations needing none
of the new fields are still written as **byte-identical version 13**.

Version 14 is deliberately rejected before destination or engine mutation.
Two development branches independently assigned it incompatible layouts. A
consumer must recompile project source; it must never infer the dialect from
payload shape, try both readers, or patch the version bytes.

`gosx_audio_capabilities` bit 0 remains the opcode-22 graph-chord command
capability. Bit 16 (`CapabilityUnifiedImage`) separately promises this exact
v15 layout. A v14-only module advertising only bit 0 is not v15-capable.
The reactor must be initialized before querying exports. The 24-byte command
and 16-byte message ABIs are unchanged.

## Layout

The common v13 layout is retained in its existing order, with these explicit
v15 additions. Every v15 image has all the fields below, including zero-valued
chord fields and an empty footer. Feature presence never selects another layout.

1. The 32-byte common header, global effect records and bus flags are unchanged,
   except the version field at bytes 4–5 is 15.
2. Every track adds one `u8 Polyphony` immediately after `u8 Kind`, before mute,
   gain-set, SFX-bus, and the existing mixer/effect/voice-specific records.
   Polyphony is 0 or 4; 4 is permitted only for a graph track.
3. Voice kind `VoiceSample` adds `u16 Asset, u8 RootKey, u8 Voices, u8 Loop`
   in the voice-specific record. VoiceAudio has no voice-specific payload.
4. Each of the 16 pattern slots keeps the common length/swing/transpose/gate/
   seed header. Each active non-drum step adds `u8 Count, u8 Notes[4]` directly
   after the existing packed `u32 Step`. Drum lane records are unchanged.
   Chord validation, unused-note zeros and graph-only ownership remain required.
5. Scene bindings retain 0=keep, 1=off and 2–17=slot. A v15 binding of 255 is
   followed by `u16 Clip`. Scene-setting records and Song records are unchanged.
6. The mandatory footer is `f32 MasterBiasL, f32 MasterBiasR, f64 MasterGainDB, u32 ScheduleCount,
   u16 AssetCount, u16 ClipCount`, followed by these tables in that order:
   - Each schedule event (26 bytes): `u64 Tick, u64 EndTick, u8 Kind, u8 Track,
     u16 Scene, u16 Index, u32 ID`. Ticks are nonnegative signed-int64-domain
     musical ticks and must satisfy the engine's schedule bounds/ordering.
   - Each resident asset: `u32 SampleRate, u32 Frames, u8 Stereo`, then Frames
     `f32` left samples and, iff Stereo=1, Frames `f32` right samples.
   - Each clip (42 bytes): `u16 Asset, u64 StartFrame, u64 EndFrame,
     u64 FadeInFrames, u64 FadeOutFrames, f64 GainDB`.

The footer contains **resident prepared PCM only**. Existing page-worker and
stream APIs are preserved separately; no streaming pointer/page-reference
format is introduced. The total image limit remains 2 MiB. Counts, dimensions,
finite PCM, references and ownership are checked before playable engine state.

Empty tables have explicit zero counts; canonical decoded empty footer tables
are nil. Unknown flags, unsupported versions, truncation and trailing bytes
fail. Full engine semantic validation is required after decode, as in v13.

## Ownership

Graph chords use the existing bounded graph pool/cohort generations. Sampler
tracks retain their independent sample pool/serial handles and scalar note
lifecycle; a graph chord is never implicitly a sampler chord. Stop, seek,
placement re-entry and reset must retire the appropriate owners independently.

The unified footer retains the existing engine MasterGainDB as well as the new
DC-bias values, so direct and image playback cannot lose master gain. Legacy
v13 writing is unchanged, including its historical absence of a master-gain
field; a master-gain value alone does not select a new format in this version.
