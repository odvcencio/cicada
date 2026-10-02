package render

import (
	"m31labs.dev/cicada/internal/studiolibtest"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"testing"
)

func studioLibraryExample(t testing.TB) (*notation.Score, *notation.Score, *project.Project) {
	return studiolibtest.Load(t, "..")
}
func TestStudioLibraryNativeAndOfflineByteParity(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	verifySourceNativeAndOfflineByteParity(t, studioLibraryExample, "studio-library")
}
func TestStudioLibraryRenderAllocationFree(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	verifySourceRenderAllocationFree(t, studioLibraryExample, "studio-library")
}
func BenchmarkStudioLibraryVoiceNext(b *testing.B) {
	b.Setenv("CICADA_LIBRARY", b.TempDir())
	score, _, p := studioLibraryExample(b)
	tracks, err := compileTracks(score, p, 48000)
	if err != nil {
		b.Fatal(err)
	}
	for _, track := range tracks {
		b.Run(track.name, func(b *testing.B) {
			b.ReportAllocs()
			if track.drums != nil {
				kit := track.drums
				kit.Hit(drum.BD, 100, false)
				kit.Hit(drum.CH, 100, false)
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if i%4800 == 0 {
						kit.Hit(drum.BD, 100, false)
						kit.Hit(drum.CH, 100, false)
					}
					l, r := kit.NextStereo()
					multiFileVoiceSample = l + r
				}
				return
			}
			voice := track.voice
			voice.NoteOn(45, 100, true, false)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i%4800 == 0 {
					voice.NoteOn(45, 100, true, false)
				}
				multiFileVoiceSample = voice.Next()
			}
		})
	}
}
