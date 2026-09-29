package acid

import (
	"encoding/binary"
	"math"
	"sync"
	"unsafe"
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

const oscillatorBankImageVersion = 1
const oscillatorBankImageHeaderSize = 4
const MaxOscillatorBankImageBytes = oscillatorBankImageHeaderSize + oscillatorBins*2 + oscillatorBins*oscillatorTableSize*4

// ExportOscillatorBankImage returns the exact shared wavetable data used by
// voices at this sample rate. Hosts can pass it to another kernel instance so
// score edits do not rebuild the FFT tables on the audio thread.
func ExportOscillatorBankImage(sampleRate int) ([]byte, error) {
	image := make([]byte, MaxOscillatorBankImageBytes)
	return ExportOscillatorBankImageInto(sampleRate, image)
}

// ExportOscillatorBankImageInto writes an oscillator cache into caller-owned
// storage and returns the used prefix.
func ExportOscillatorBankImageInto(sampleRate int, storage []byte) ([]byte, error) {
	_, ok := oscillatorBankIndex(sampleRate)
	if !ok {
		return nil, Error("unsupported oscillator sample rate")
	}
	bank := bankForSampleRate(sampleRate)
	var unique []*oscillatorTable
	indices := make(map[*oscillatorTable]uint16, oscillatorBins)
	for _, table := range bank.tables {
		if _, found := indices[table]; found {
			continue
		}
		indices[table] = uint16(len(unique))
		unique = append(unique, table)
	}
	const mappingBytes = oscillatorBins * 2
	imageBytes := oscillatorBankImageHeaderSize + mappingBytes + len(unique)*oscillatorTableSize*4
	if len(storage) < imageBytes {
		return nil, Error("oscillator bank image storage is too small")
	}
	image := storage[:imageBytes]
	clear(image[:oscillatorBankImageHeaderSize])
	image[0] = oscillatorBankImageVersion
	binary.LittleEndian.PutUint16(image[1:3], uint16(len(unique)))
	for bin, table := range bank.tables {
		binary.LittleEndian.PutUint16(image[oscillatorBankImageHeaderSize+bin*2:], indices[table])
	}
	at := oscillatorBankImageHeaderSize + mappingBytes
	for _, table := range unique {
		for _, value := range table {
			binary.LittleEndian.PutUint32(image[at:], math.Float32bits(value))
			at += 4
		}
	}
	return image, nil
}

// InstallOscillatorBankImage installs a bank exported by another instance.
// The format stores unique tables once and maps semitone bins to them.
func InstallOscillatorBankImage(sampleRate int, image []byte) error {
	index, ok := oscillatorBankIndex(sampleRate)
	if !ok {
		return Error("unsupported oscillator sample rate")
	}
	shared := &sharedOscillatorBanks[index]
	if shared.bank != nil {
		return Error("oscillator bank is already initialized")
	}
	bank, err := decodeOscillatorBankImage(image)
	if err != nil {
		return err
	}
	shared.once.Do(func() { shared.bank = bank })
	if shared.bank != bank {
		return Error("oscillator bank was initialized concurrently")
	}
	return nil
}

func decodeOscillatorBankImage(image []byte) (*oscillatorBank, error) {
	if len(image) < oscillatorBankImageHeaderSize || image[0] != oscillatorBankImageVersion {
		return nil, Error("invalid oscillator bank image")
	}
	count := int(binary.LittleEndian.Uint16(image[1:3]))
	if count < 1 || count > oscillatorBins {
		return nil, Error("invalid oscillator bank table count")
	}
	const mappingBytes = oscillatorBins * 2
	dataAt := oscillatorBankImageHeaderSize + mappingBytes
	if len(image) != dataAt+count*oscillatorTableSize*4 {
		return nil, Error("invalid oscillator bank image length")
	}
	unique := make([]*oscillatorTable, count)
	littleEndian := func() bool {
		var value uint16 = 1
		return *(*byte)(unsafe.Pointer(&value)) == 1
	}()
	for i := range unique {
		start := dataAt + i*oscillatorTableSize*4
		part := image[start : start+oscillatorTableSize*4]
		if littleEndian {
			unique[i] = (*oscillatorTable)(unsafe.Pointer(&image[start]))
		} else {
			table := new(oscillatorTable)
			for j := range table {
				table[j] = math.Float32frombits(binary.LittleEndian.Uint32(part[j*4:]))
			}
			unique[i] = table
		}
	}
	bank := new(oscillatorBank)
	for bin := range bank.tables {
		index := int(binary.LittleEndian.Uint16(image[oscillatorBankImageHeaderSize+bin*2:]))
		if index >= len(unique) {
			return nil, Error("invalid oscillator bank table reference")
		}
		bank.tables[bin] = unique[index]
	}
	return bank, nil
}

func oscillatorBankIndex(sampleRate int) (int, bool) {
	switch sampleRate {
	case 44_100:
		return 0, true
	case 48_000:
		return 1, true
	case 96_000:
		return 2, true
	default:
		return 0, false
	}
}

func bankForSampleRate(sampleRate int) *oscillatorBank {
	index, _ := oscillatorBankIndex(sampleRate)
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
