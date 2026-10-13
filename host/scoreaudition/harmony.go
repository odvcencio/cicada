package scoreaudition

import (
	"math"
	"sort"
	"strings"
)

var noteNames = [12]string{"C", "C#", "D", "Eb", "E", "F", "F#", "G", "Ab", "A", "Bb", "B"}

type chordQuality struct {
	suffix string
	tones  []int
	minor  bool
}

var chordQualities = []chordQuality{
	{"", []int{0, 4, 7}, false},
	{"m", []int{0, 3, 7}, true},
	{"7", []int{0, 4, 7, 10}, false},
	{"m7", []int{0, 3, 7, 10}, true},
	{"maj7", []int{0, 4, 7, 11}, false},
}

// Krumhansl-Schmuckler key profiles.
var (
	profileMajor = [12]float64{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88}
	profileMinor = [12]float64{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17}
)

// chordMatch is the best template for a pitch-class weight vector.
type chordMatch struct {
	Name    string
	Root    int
	Quality int
	Score   float64
	Tones   [12]bool
}

func (m chordMatch) valid() bool { return m.Name != "" }

// bestChord finds the template with the highest cosine similarity to v. A
// seventh chord must beat its triad by 0.02 to win, and a root that is present
// in v adds up to 0.03, which settles ties between rotations of one set.
func bestChord(v [12]float64) chordMatch {
	var norm, top float64
	for _, x := range v {
		norm += x * x
		top = math.Max(top, x)
	}
	best := chordMatch{}
	bestRank := -1.0
	if norm == 0 {
		return chordMatch{}
	}
	norm = math.Sqrt(norm)
	for root := 0; root < 12; root++ {
		for qi, q := range chordQualities {
			var dot float64
			for _, t := range q.tones {
				dot += v[(root+t)%12]
			}
			score := dot / (norm * math.Sqrt(float64(len(q.tones))))
			if len(q.tones) == 4 {
				score -= 0.02
			}
			score += 0.03 * v[root] / top
			if score > bestRank+1e-12 {
				bestRank = score
				// The exported fit is the plain cosine similarity, 0 to 1;
				// the penalty and root bonus only rank the templates.
				fit := dot / (norm * math.Sqrt(float64(len(q.tones))))
				best = chordMatch{Name: noteNames[root] + q.suffix, Root: root, Quality: qi, Score: math.Min(1, fit)}
				best.Tones = [12]bool{}
				for _, t := range q.tones {
					best.Tones[(root+t)%12] = true
				}
			}
		}
	}
	best.Score = math.Round(best.Score*1000) / 1000
	return best
}

// writtenChord names a pitch-class set. Sets of one or two pitch classes are
// listed as notes because they do not decide a chord quality.
func writtenChord(set [12]bool) (name string, match chordMatch, count int) {
	var v [12]float64
	var parts []string
	for pc, on := range set {
		if on {
			v[pc] = 1
			count++
			parts = append(parts, noteNames[pc])
		}
	}
	switch {
	case count == 0:
		return "", chordMatch{}, 0
	case count < 3:
		return strings.Join(parts, "+"), chordMatch{}, count
	}
	m := bestChord(v)
	return m.Name, m, count
}

// chordsAgree decides whether the heard chord is consistent with the written set.
func chordsAgree(set [12]bool, written, heard chordMatch, count int) bool {
	if !heard.valid() {
		return false
	}
	subset := func(a, b [12]bool) bool {
		for i := range a {
			if a[i] && !b[i] {
				return false
			}
		}
		return true
	}
	if count < 3 {
		return subset(set, heard.Tones)
	}
	if written.Root == heard.Root && chordQualities[written.Quality].minor == chordQualities[heard.Quality].minor {
		return true
	}
	return subset(heard.Tones, set)
}

type keyGuess struct {
	Key   string  `json:"key"`
	Score float64 `json:"score"`
}

// estimateKey correlates a chroma vector with the 24 Krumhansl-Schmuckler
// profiles and returns the best three.
func estimateKey(chroma [12]float64) []keyGuess {
	var all []keyGuess
	for root := 0; root < 12; root++ {
		for _, p := range []struct {
			name    string
			profile *[12]float64
		}{{"major", &profileMajor}, {"minor", &profileMinor}} {
			var rolled [12]float64
			for i := 0; i < 12; i++ {
				rolled[(i+root)%12] = p.profile[i]
			}
			all = append(all, keyGuess{Key: noteNames[root] + " " + p.name, Score: math.Round(pearson(chroma[:], rolled[:])*1000) / 1000})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	return all[:3]
}

func pearson(a, b []float64) float64 {
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(len(a))
	mb /= float64(len(b))
	var num, da, db float64
	for i := range a {
		num += (a[i] - ma) * (b[i] - mb)
		da += (a[i] - ma) * (a[i] - ma)
		db += (b[i] - mb) * (b[i] - mb)
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}
