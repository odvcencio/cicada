# Demo songs

Complete, editable compositions for exploring Cicada beyond a single
loop. Each score is self-contained and uses the edition-2 manifest in this
folder. No audio packs or library downloads are needed.

| Song | Form | Musical focus | Cicada features |
| --- | --- | --- | --- |
| [Anywhere You Are](anywhere-you-are.cicada) | 80 bars at 108 BPM; about 178 seconds | Original D-major electronic soul love song for Danilo; optional vocal melody guide | Bespoke bass, pad, plucked string and bell, tine EP, full-song dynamics, empty vocal recording track |
| [Neon Causeway](neon-causeway.cicada) | 40 bars at 126 BPM; about 76 seconds | Acid groove with a melodic hook, breakdown, return, and fade | Phrases, slides, drum dynamics, PM synthesis, delay/reverb, continuous cutoff and level lanes |
| [Lanterns After Rain](lanterns-after-rain.cicada) | 40 bars at 104 BPM; about 92 seconds | D-minor keyboard groove with extended chords and an A7 turnaround | Modeled tine EP, four-note chords, quarter/eighth/sixteenth grids, velocity rows, two- and four-bar patterns |
| [Tidal Glass](tidal-glass.cicada) | 24 bars at 80 BPM; 72 seconds | Sparse Dorian plucks, slow harmony, and an offbeat answering voice | Graph comb synthesis, polyphonic pad, triplet grid, named placements, markers, stereo space |

Durations exclude the exported effect tail. These are authored compositions,
not generated seed variations. The drum grooves repeat, but the arrangements
change density, harmony, timbre, or register between sections.

## Play and edit

From the repository root:

```sh
make build
./build/cicada check examples/songs/neon-causeway.cicada
./build/cicada studio examples/songs/neon-causeway.cicada
```

Choose **Play** after Studio opens. Use `--audio null` for a silent, device-free
editing session. On a PulseAudio desktop, including WSLg, select the system
output explicitly with `--audio oto`. Substitute either other score to open it. The native host renders the
keyboard voice in Lanterns; the legacy portable host needs its keyboard module.

With the binary on your PATH, `cicada check` and `cicada fmt --check` from this
folder check the independent scores.

To keep a personal version, use Save As:

```sh
mkdir -p /tmp/my-neon
./build/cicada save-as examples/songs/neon-causeway.cicada /tmp/my-neon/main.cicada
```

## Render the full arrangements

```sh
mkdir -p build/demo-songs
./build/cicada render examples/songs/neon-causeway.cicada -o build/demo-songs/neon-causeway.wav --bits 16 --loudness -16
./build/cicada render examples/songs/lanterns-after-rain.cicada -o build/demo-songs/lanterns-after-rain.wav --bits 16 --loudness -18
./build/cicada render examples/songs/tidal-glass.cicada -o build/demo-songs/tidal-glass.wav --bits 16 --loudness -20
```

Leaving out `--bars` renders the whole arrangement. The default sample rate is
48 kHz and the default effect tail is three seconds. WAVs stay in the ignored
build folder; the score files are the reusable examples.

Export a note arrangement for another sequencer:

```sh
./build/cicada midi examples/songs/tidal-glass.cicada -o build/demo-songs/tidal-glass.mid
./build/cicada verify-midi build/demo-songs/tidal-glass.mid --ppq 960 --type 1
./build/cicada stems examples/songs/neon-causeway.cicada -o build/neon-return-stems --from 29 --bars 4
./build/cicada verify-stems build/neon-return-stems
```

MIDI keeps the placement starts, bounded note lengths, chords, and section
markers. It carries notes rather than Cicada's instrument sounds or effects.
The stems command isolates the four-bar return, including its effect state
from the preceding sections; use a new destination for each stem render.

## Follow the form

Neon's sections begin at bars 1, 5, 13, 17, 25, 29, and 37. The breakdown at
25 removes the drums; the return at 29 restores the full hook. Inspect the
cutoff at the start of the first chorus and during the preceding lift:

```sh
./build/cicada explain examples/songs/neon-causeway.cicada bass.cutoff @17
./build/cicada explain examples/songs/neon-causeway.cicada bass.cutoff @15
```

Lanterns changes section at bars 1, 5, 13, 21, 25, and 37. Its four-bar harmony
is Dm9, Gm9, Bbmaj7, then C9. The answer changes the final bar to A7, including
an explicit C# that is outside D natural minor. Keep bass and chord changes
aligned when changing the section lengths.

Tidal's markers name the sections. In **Session**, the named-placement view
shows each instance's exact start and exclusive end. `far-in` begins at
`@5.2.1`, one beat after the new harmony. Its pattern restarts there; moving
that placement changes the relationship between the voices without rewriting
their notes. Select the block in **Session** to change its start or length,
or drag it directly in the timeline. The pluck
blends a noise-excited comb with a sine body so it stays audible under the pad;
the sub supports that harmony. Stem comparison is useful here: a brief peak
does not tell you how much energy a part contributes across the phrase.

## Composition exercises and remaining friction

- **Neon:** change `answer`, then compare both bass patterns. Change the
  incoming `smooth` at `@17.1.1` to `step` to hear a jump instead of the build.
  A song-level lane wins over scene settings once its first point is reached.
  Editing a track cutoff while its lane is active therefore does not change
  that moment's target.
- **Lanterns:** replace one chord pitch in the grid, then Undo. Change the
  first bar's velocity values while keeping its pitches. Chord input through
  live note-take commit is still unavailable; authored chords can be edited in
  the grid or Score. Velocity rows need an initial number, even if the first
  melodic cell is a rest.
- **Tidal:** move `far-in` from `@5.2.1` to `@5.3.1`, then restore it. The
  **Start** field in Session makes the beat change exact; dragging the block
  moves it in sixteenth steps. Use Undo to compare, then try copying the
  placement to the ending. A placement loop starts locally; song-scene patterns use the shared
  transport phase. Comparing them makes that distinction audible.
- **Endings:** master level is static in the current kernel. Neon and Lanterns
  fade supported track levels; their post-fader sends fade with them while
  existing effect tails decay. A shared master fade remains useful follow-up
  work.

For a method you can apply to your own song, read
[Composing a complete song](../../docs/manual/composing-songs.md).

## Anywhere You Are

Draco Skysong's original love-song backing for Danilo has a spacious verse,
a rising pre-chorus, a wider chorus, a half-time bridge, and an extended outro.
The harmony moves through Dmaj9, A/C#, Bm9 and Gmaj9, with Em9/A13 in the chorus.
The `guide` track suggests a melody in A3–F#4; mute it for a clean backing.
A possible hook is “Anywhere you are / I can find my way back home.”
The guide is synthesized and contains no recorded human vocal.

Open Session, select the empty `danilo` audio track, choose your microphone,
and record the full song or punch a phrase. Keep **Follow song tempo (keep pitch)**
enabled to try different tempos after comping. In Automation, select
`danilo.level` and record a level ride while playing the song. Raw takes remain
available when either edit is undone. The disposable album workflow test also
records four bars of the synthesized guide to exercise this path without opening
a microphone.
