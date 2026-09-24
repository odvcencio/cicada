package phrase

const metricPeriod = 16

var metricStrength = [metricPeriod]uint8{4, 1, 2, 1, 3, 1, 2, 1, 3, 1, 2, 1, 3, 1, 2, 1}

var rhythmMasks = [...]string{
	"x.x.x.x.x.x.x.x.",
	"x..x..x...x..x..",
	"x.xx.xx.x.xx.xx.",
	"xxxxxxxxxxxxxxxx",
	"x.x.xx.xx.x.xx.x",
	"x..xx..xx..xx..x",
	"x.xxx.xxx.xxx.xx",
	"xx.xx.xx.xx.xx.x",
	"x...x.x.x...x.x.",
	"x.x..x.xx.x..x.x",
	"xxx.xxx.xxx.xxx.",
	"x..x.x..x..x.x..",
}

var rhythmWeights = [...]uint8{10, 10, 12, 8, 10, 9, 8, 10, 9, 10, 7, 9}

func onsetTarget(steps, density int) int {
	// 0.35 + 0.6*(density/64) = (112 + 3*density)/320.
	target := (steps*(112+3*density) + 160) / 320
	if target < 1 {
		return 1
	}
	if target > steps {
		return steps
	}
	return target
}

func generateRhythm(s *stream, steps int, density uint8, restDownbeat bool) []bool {
	selected := s.pick("rhythm", -1, rhythmWeights[:])
	mask := rhythmMasks[selected]
	onsets := make([]bool, steps)
	count := 0
	for index := range onsets {
		onsets[index] = mask[index%metricPeriod] == 'x'
		if onsets[index] {
			count++
		}
	}
	target := onsetTarget(steps, int(density))
	for count != target {
		adding := count < target
		strength := -1
		if !adding {
			strength = 5
		}
		var eligible [64]int
		n := 0
		for index, active := range onsets {
			if adding == active || !adding && !restDownbeat && index == 0 {
				continue
			}
			value := int(metricStrength[index%metricPeriod])
			if adding && value > strength || !adding && value < strength {
				strength, n = value, 0
			}
			if value == strength {
				eligible[n] = index
				n++
			}
		}
		if n == 0 {
			break // A protected downbeat may leave the count one above target.
		}
		chosen := eligible[s.choose("rhythm", -1, n)]
		s.trace[len(s.trace)-1].Step = chosen
		onsets[chosen] = adding
		if adding {
			count++
		} else {
			count--
		}
	}
	if !restDownbeat {
		onsets[0] = true
	}
	return onsets
}
