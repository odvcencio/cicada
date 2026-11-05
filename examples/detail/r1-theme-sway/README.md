# R1: theme modulation

This project starts from the Bon Voyage theme at commit `46ecbf3` in
`odvcencio/bon-voyage`, under `content/audio`. Its notes and arrangement are
unchanged. It adds a slow pan LFO and contour to the pad, sway to the keys
level, and a separate sway read by the authored brass filter with `mod()`.

The original and modulated MIDI exports are byte-identical: 10 tracks, 2,431
notes, SHA-256 `665cf9907a0f4678e151624c3769ec569737ef6842013b6c925778576dd8738f`.
`TestR1ThemeModulationPreservesOriginalMIDI` checks that comparison without a
separate Bon Voyage checkout. The 64-bar render still needs a listener to
judge whether the pad, keys and brass movement is musical and nothing else
sounds changed.

```sh
cicada midi examples/detail/r1-theme-sway/main.cicada -o r1.mid
cicada render examples/detail/r1-theme-sway/main.cicada -o r1.wav
```
