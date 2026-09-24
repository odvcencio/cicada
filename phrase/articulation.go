package phrase

import "sort"

func accentTarget(onsets int, density uint8) int {
	// 0.1 + 0.4*(density/64) = (16+density)/160.
	return (onsets*(16+int(density)) + 80) / 160
}

func slideTarget(onsets int, density uint8) int {
	return (onsets*int(density) + 64) / 128
}

func addAccents(notes []noteState, density uint8) {
	order := make([]int, 0, len(notes))
	for index, note := range notes {
		if note.active {
			order = append(order, index)
		}
	}
	target := accentTarget(len(order), density)
	if target > len(order) {
		target = len(order)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		strengthA, strengthB := metricStrength[a%metricPeriod], metricStrength[b%metricPeriod]
		if strengthA != strengthB {
			return strengthA > strengthB
		}
		if (notes[a].class == classOctave) != (notes[b].class == classOctave) {
			return notes[a].class == classOctave
		}
		return a < b
	})
	for _, index := range order[:target] {
		notes[index].accent = true
	}
	if target > 0 && notes[0].active && !notes[0].accent {
		notes[order[target-1]].accent = false
		notes[0].accent = true
	}
	if density >= 52 {
		return
	}
	sort.Ints(order)
	for onset := 2; onset < len(order); onset++ {
		if !notes[order[onset-2]].accent || !notes[order[onset-1]].accent || !notes[order[onset]].accent {
			continue
		}
		notes[order[onset]].accent = false
		for later := onset + 1; later < len(order); later++ {
			if notes[order[later]].accent {
				continue
			}
			notes[order[later]].accent = true
			if !hasAccentTriple(notes, order) {
				break
			}
			notes[order[later]].accent = false
		}
	}
}

// repairDefaultAccents keeps generated default 16-step bars inside the
// distribution gate after structure mutations without changing their draws.
// The structure mutation retains its unrestricted accent toggle.
func repairDefaultAccents(notes []noteState) {
	var order []int
	accents := 0
	for index, note := range notes {
		if !note.active || note.tie {
			continue
		}
		order = append(order, index)
		if note.accent {
			accents++
		}
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		strengthA, strengthB := metricStrength[a%metricPeriod], metricStrength[b%metricPeriod]
		if strengthA != strengthB {
			return strengthA > strengthB
		}
		if (notes[a].class == classOctave) != (notes[b].class == classOctave) {
			return notes[a].class == classOctave
		}
		return a < b
	})
	for _, index := range order {
		if accents >= 2 {
			break
		}
		if !notes[index].accent {
			notes[index].accent = true
			accents++
		}
	}
	for i := len(order) - 1; i >= 0 && accents > 6; i-- {
		index := order[i]
		if notes[index].accent && index != 0 {
			notes[index].accent = false
			accents--
		}
	}
}

func hasAccentTriple(notes []noteState, onsets []int) bool {
	for index := 2; index < len(onsets); index++ {
		if notes[onsets[index-2]].accent && notes[onsets[index-1]].accent && notes[onsets[index]].accent {
			return true
		}
	}
	return false
}

func addSlides(s *stream, notes []noteState, density uint8) {
	onsets := 0
	for _, note := range notes {
		if note.active {
			onsets++
		}
	}
	target := slideTarget(onsets, density)
	weights := make([]uint8, len(notes))
	for index, source := range notes {
		next := (index + 1) % len(notes)
		if !source.active || !notes[next].active {
			continue
		}
		interval := notes[next].note - source.note
		if !allowedSlideInterval(interval) {
			continue
		}
		if interval == 0 {
			weights[index] = 2
			continue
		}
		weight := uint8(2)
		if abs(interval) >= 7 {
			weight += 2
		}
		if !source.accent {
			weight++
		}
		switch abs(interval) {
		case 2, 3, 5, 7, 12:
			weight++
		}
		weights[index] = weight
	}
	for range target {
		valid := false
		for _, weight := range weights {
			valid = valid || weight > 0
		}
		if !valid {
			break
		}
		chosen := s.pick("slide", -1, weights)
		s.trace[len(s.trace)-1].Step = chosen
		weights[chosen] = 0
		next := (chosen + 1) % len(notes)
		if notes[chosen].note == notes[next].note {
			notes[next].tie = true
		} else {
			notes[chosen].slide = true
		}
	}
}

func allowedSlideInterval(interval int) bool {
	switch abs(interval) {
	case 0, 2, 3, 4, 5, 7, 10, 12:
		return true
	}
	return false
}
