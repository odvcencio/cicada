package acid

import (
	"math"
	"sync"
)

// Each table is limited to harmonics below the top of its semitone bin.
// Adjacent tables crossfade as pitch moves, including during a slide.
const oscillatorTableSize = 4096
const oscillatorTableMask = oscillatorTableSize - 1
const oscillatorBins = 144
const oscillatorMinPitchLog = 3 // 8 Hz

type oscillatorTable [oscillatorTableSize]float32

type oscillatorBank struct {
	tables [oscillatorBins]*oscillatorTable
}

type oscillatorSelection struct {
	low, high *oscillatorTable
	blend     float64
}

var sharedOscillatorBanks [3]struct {
	once sync.Once
	bank *oscillatorBank
}

func bankForSampleRate(sampleRate int) *oscillatorBank {
	index := 0
	switch sampleRate {
	case 44_100:
		index = 0
	case 48_000:
		index = 1
	case 96_000:
		index = 2
	}
	shared := &sharedOscillatorBanks[index]
	shared.once.Do(func() { shared.bank = buildOscillatorBank(float64(sampleRate)) })
	return shared.bank
}

func buildOscillatorBank(sampleRate float64) *oscillatorBank {
	bank := new(oscillatorBank)
	byHarmonics := make(map[int]*oscillatorTable)
	for bin := range bank.tables {
		upperFrequency := math.Exp2(oscillatorMinPitchLog + float64(bin+1)/12)
		harmonics := int((sampleRate/2 - 100) / upperFrequency)
		if harmonics > oscillatorTableSize/4 {
			harmonics = oscillatorTableSize / 4
		}
		if harmonics < 0 {
			harmonics = 0
		}
		table := byHarmonics[harmonics]
		if table == nil {
			table = buildSawTable(harmonics)
			byHarmonics[harmonics] = table
		}
		bank.tables[bin] = table
	}
	return bank
}

func buildSawTable(harmonics int) *oscillatorTable {
	table := new(oscillatorTable)
	if harmonics == 0 {
		return table
	}
	spectrum := make([]complex128, oscillatorTableSize)
	for harmonic := 1; harmonic <= harmonics; harmonic++ {
		coefficient := float64(oscillatorTableSize) / (math.Pi * float64(harmonic))
		spectrum[harmonic] = complex(0, coefficient)
		spectrum[oscillatorTableSize-harmonic] = complex(0, -coefficient)
	}
	inverseOscillatorFFT(spectrum)
	for i := range table {
		table[i] = float32(real(spectrum[i]))
	}
	return table
}

func inverseOscillatorFFT(data []complex128) {
	n := len(data)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			data[i], data[j] = data[j], data[i]
		}
	}
	for span := 2; span <= n; span <<= 1 {
		angle := 2 * math.Pi / float64(span)
		root := complex(math.Cos(angle), math.Sin(angle))
		for base := 0; base < n; base += span {
			rotation := complex(1, 0)
			for i := 0; i < span/2; i++ {
				even, odd := data[base+i], data[base+i+span/2]*rotation
				data[base+i], data[base+i+span/2] = even+odd, even-odd
				rotation *= root
			}
		}
	}
	for i := range data {
		data[i] /= complex(float64(n), 0)
	}
}

func (bank *oscillatorBank) saw(phase, pitchLog float64) float64 {
	return bank.selectTables(pitchLog).saw(phase)
}

func (bank *oscillatorBank) selectTables(pitchLog float64) oscillatorSelection {
	position := (pitchLog - oscillatorMinPitchLog) * 12
	bin := int(position)
	if position < 0 {
		bin = 0
		position = 0
	} else if bin >= oscillatorBins-1 {
		bin = oscillatorBins - 2
		position = oscillatorBins - 1
	}
	blend := position - float64(bin)
	return oscillatorSelection{low: bank.tables[bin], high: bank.tables[bin+1], blend: blend}
}

func (selection oscillatorSelection) saw(phase float64) float64 {
	low := sampleSawTable(selection.low, phase)
	high := sampleSawTable(selection.high, phase)
	return low + (high-low)*selection.blend
}

func sampleSawTable(table *oscillatorTable, phase float64) float64 {
	index := phase * oscillatorTableSize
	left := int(index)
	fraction := index - float64(left)
	a := float64(table[left&oscillatorTableMask])
	b := float64(table[(left+1)&oscillatorTableMask])
	return a + (b-a)*fraction
}

func (bank *oscillatorBank) pulse(phase, pitchLog, width float64) float64 {
	return bank.selectTables(pitchLog).pulse(phase, width)
}

func (selection oscillatorSelection) pulse(phase, width float64) float64 {
	shifted := phase - width
	if shifted < 0 {
		shifted++
	}
	return selection.saw(shifted) - selection.saw(phase) + 2*width - 1
}
