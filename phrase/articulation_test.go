package phrase

import "testing"

func TestDefaultAccentRepairUsesMetricRankAndKeepsDownbeat(t *testing.T) {
	notes := make([]noteState, 16)
	for index := range notes {
		notes[index] = noteState{active: true, class: classRoot, note: 45}
	}
	notes[0].accent = true
	repairDefaultAccents(notes)
	if !notes[0].accent || !notes[4].accent {
		t.Fatal("repair did not add the strongest unaccented onset")
	}
	count := 0
	for _, note := range notes {
		if note.accent {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("minimum repair left %d accents, want 2", count)
	}
	for index := range notes {
		notes[index].accent = true
	}
	repairDefaultAccents(notes)
	count = 0
	for _, note := range notes {
		if note.accent {
			count++
		}
	}
	if count != 6 || !notes[0].accent {
		t.Fatalf("maximum repair left %d accents, downbeat=%t; want 6 and an accented downbeat", count, notes[0].accent)
	}
}
