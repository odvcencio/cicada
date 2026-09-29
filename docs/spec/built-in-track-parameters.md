# Built-in track parameter catalog

This page lists source parameters for the built-in acid and drum voices. For typed JSON record fields, see the [semantic field catalog](semantic-model.md).

## Built-in voice parameters

**Status:** Implemented.

**Syntax (EBNF):** A <code>param_decl</code> is inside <code>track_decl</code>. Acid fields use their field name, such as <code>cutoff</code>; drum fields use a lane prefix, such as <code>bd_tune</code>.

**Meaning:** The selected voice validates the names, types, and ranges before compilation. Mixer fields are documented in [Track mixer settings](edition-1.md#track-mixer-settings) and are not part of the voice parameter list.

**Types and units:** Unit names in the tables describe the source value. Unitless oscillator controls and ratios have no suffix. Time values use milliseconds; frequencies use Hz; levels use dB. Switches accept <code>on</code>/<code>off</code>; <code>true</code>/<code>false</code> remain aliases.

**Defaults:** Omitted controls use the values in the tables. Every built-in lane also has level −6 dB and pan 0 by default.

**Errors:** A name unknown to the selected voice reports CICADA-PARAM. Wrong units report CICADA-PARAM; values outside the listed range fail validation with CICADA-PARAM.

**Example:**

```cicada
cicada 2
track bass acid { cutoff = 720Hz reso = 0.65 decay = 500ms }
pattern pulse acid { 1^ . 5 . }
scene main { bass = pulse }
song { main*4 }
```

**Edition history:** Available in edition 1. The semantic field catalog describes <code>track.params</code> as a typed-value map; this page defines the current built-in voice keys checked inside that map.

## Acid voice

The built-in acid voice accepts these parameters:

| Field | Unit | Range | Default |
| --- | --- | --- | --- |
| <code>tune</code> | semitone value | −12–12 | 0 |
| <code>fine</code> | cent value | −100–100 | 0 |
| <code>wave</code> | unitless | 0–1 | 0 |
| <code>pw</code> | unitless | 0.1–0.9 | 0.5 |
| <code>detune</code> | unitless | 0–50 | 0 |
| <code>sub</code> | unitless | 0–1 | 0 |
| <code>cutoff</code> | Hz | 20–8000 | 600 Hz |
| <code>reso</code> | unitless | 0–1 | 0.55 |
| <code>envmod</code> | unitless | 0–1 | 0.6 |
| <code>decay</code> | ms | 100–3000 | 400 ms |
| <code>accent</code> | unitless | 0–1 | 0.7 |
| <code>drive</code> | unitless | 0–1 | 0.2 |
| <code>release</code> | ms | 5–500 | 30 ms |
| <code>slide</code> | ms | 20–200 | 60 ms |
| <code>gate</code> | percent | 10–100 | 55 |
| <code>filter</code> | enum | <code>diode</code> or <code>ladder</code> | <code>diode</code> |
| <code>savage</code> | switch | <code>on</code> or <code>off</code> | <code>off</code> |

<code>tune</code> and <code>fine</code> change pitch; <code>wave</code> and <code>pw</code> shape the oscillator; <code>cutoff</code>, <code>reso</code>, and <code>envmod</code> control the filter; <code>decay</code>, <code>accent</code>, <code>drive</code>, <code>release</code>, and <code>slide</code> shape articulation. <code>filter</code> selects the filter model. <code>savage</code> enables the additional nonlinear filter behavior.

## Drum voice parameters

Prefix a drum parameter with the lane name. For example, <code>bd_tune</code> sets the bass drum tune. Every lane accepts <code>level</code> (−60 to +6 dB or <code>off</code>) and <code>pan</code> (−1 to +1).

| Lane | Additional fields | Defaults |
| --- | --- | --- |
| <code>bd</code> | <code>tune</code> 40–120 Hz; <code>decay</code> 80–1500 ms; <code>sweep</code> 1–8; <code>sweep_time</code> 10–80 ms; <code>click</code> 0–1; <code>drive</code> 0–1 | 55 Hz, 400 ms, 4, 30 ms, 0.3, 0.2 |
| <code>sd</code> | <code>tune</code> 0.7–1.4; <code>tone</code> 0.5–2; <code>mix</code> 0–1; <code>snappy</code> 60–400 ms; <code>decay</code> 40–300 ms | 1, 1, 0.6, 180 ms, 90 ms |
| <code>ch</code> | <code>tune</code> 0.8–1.25; <code>tone</code> 5000–10000 Hz; <code>decay</code> 20–150 ms; <code>metal</code> switch | 1, 7500 Hz, 60 ms, off |
| <code>oh</code> | <code>tune</code> 0.8–1.25; <code>tone</code> 5000–10000 Hz; <code>decay</code> 150–1200 ms; <code>metal</code> switch | 1, 7500 Hz, 400 ms, off |
| <code>cp</code> | <code>tone</code> 700–2000 Hz; <code>decay</code> 60–400 ms; <code>spread</code> 6–16 ms | 1100 Hz, 120 ms, 10 ms |
| <code>rs</code> | <code>tune</code> 0.7–1.4; <code>decay</code> 4–50 ms | 1, 12 ms |
| <code>lt</code>, <code>mt</code>, <code>ht</code> | <code>tune</code> 0.667–1.498; <code>decay</code> 80–1000 ms; <code>sweep</code> 0–2 | 1, 250 ms, 0.4 |
| <code>cb</code> | <code>tune</code> 0.7–1.4; <code>decay</code> 40–400 ms | 1, 120 ms |
| <code>cy</code> | <code>tune</code> 0.8–1.25; <code>decay</code> 500–4000 ms; <code>tone</code> 0.5–2 | 1, 1500 ms, 1 |

<code>sd_tune</code>, <code>sd_tone</code>, <code>sd_mix</code>, <code>ch_tune</code>, <code>oh_tune</code>, tom tune, <code>cb_tune</code>, <code>cy_tune</code>, and <code>cy_tone</code> are unitless ratios. <code>sweep</code>, <code>click</code>, <code>drive</code>, and <code>mix</code> are unitless controls from 0 to 1 unless a narrower range is listed. The tom tune bounds are 2 to the power of −7/12 through +7/12.

```cicada
cicada 2
track kit drums { bd_tune = 55Hz bd_decay = 400ms ch_tone = 7500Hz }
pattern beat drums {
  bd: X...x...
  ch: x.x.x.x.
}
scene main { kit = beat }
song { main*4 }
```
