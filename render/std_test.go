package render

import (
	"testing"

	"m31labs.dev/cicada/internal/stdtest"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func stdExample(name string) func(testing.TB) (*notation.Score, *notation.Score, *project.Project) {
	return func(t testing.TB) (*notation.Score, *notation.Score, *project.Project) {
		return stdtest.Load(t, "..", name)
	}
}

func TestStdNativeAndOfflineByteParity(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, name := range stdtest.Libraries {
		t.Run(name, func(t *testing.T) {
			verifySourceNativeAndOfflineByteParity(t, stdExample(name), "std/"+name)
		})
	}
}

func TestStdRenderAllocationFree(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, name := range stdtest.Libraries {
		t.Run(name, func(t *testing.T) {
			verifySourceRenderAllocationFree(t, stdExample(name), "std/"+name)
		})
	}
}

func BenchmarkStdVoiceNext(b *testing.B) {
	b.Setenv("CICADA_LIBRARY", b.TempDir())
	for _, name := range []string{"synth", "drums", "presets"} {
		b.Run(name, func(b *testing.B) {
			score, _, p := stdExample(name)(b)
			tracks, err := compileTracks(score, p, 48000)
			if err != nil {
				b.Fatal(err)
			}
			for _, track := range tracks {
				if track.drums == nil {
					b.Run(track.name, func(b *testing.B) {
						voice := track.voice
						voice.NoteOn(45, 100, true, false)
						b.ReportAllocs()
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							if i%4800 == 0 {
								voice.NoteOn(45, 100, true, false)
							}
							multiFileVoiceSample = voice.Next()
						}
					})
					continue
				}
				b.Run(track.name, func(b *testing.B) {
					kit := track.drums
					kit.Hit(drum.BD, 100, false)
					kit.Hit(drum.SD, 100, false)
					kit.Hit(drum.CH, 100, false)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if i%4800 == 0 {
							kit.Hit(drum.BD, 100, false)
							kit.Hit(drum.SD, 100, false)
							kit.Hit(drum.CH, 100, false)
						}
						left, right := kit.NextStereo()
						multiFileVoiceSample = left + right
					}
				})
			}
		})
	}
}
