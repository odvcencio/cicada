package instrumentpack

import "fmt"

// DefaultMaxResidentPCM is the default cap, in bytes, on the decoded PCM of all
// sampler packs that one native render, play or Studio session holds in
// memory. It is separate from MaxPCMBytes, which caps each pack, and from the
// 64 MiB resident budget of the browser image and WAM2 export.
const DefaultMaxResidentPCM int64 = 1 << 30

// ResidentPCMBytes is the decoded float32 PCM that the pack keeps in memory.
// Zones that share one asset share one region, and that region counts once.
func (p *Prepared) ResidentPCMBytes() int64 {
	var total int64
	counted := map[*float32]bool{}
	for _, z := range p.Zones {
		for _, pcm := range [][]float32{z.Region.Left, z.Region.Right} {
			if len(pcm) > 0 && !counted[&pcm[0]] {
				counted[&pcm[0]] = true
				total += int64(len(pcm)) * 4
			}
		}
	}
	return total
}

// HasAttackZone reports whether a note at this velocity can start a sound: some
// zone that is not a release zone holds the note in its key range and the
// velocity in its velocity range. sample.Instrument.NoteOn refuses any other
// note, and the engine then stops with fault 29. Velocity 0 never plays, so it
// reports false.
func (p *Prepared) HasAttackZone(note, velocity uint8) bool {
	for _, z := range p.Zones {
		if !z.Release && note >= z.KeyLow && note <= z.KeyHigh && velocity >= z.VelocityLow && velocity <= z.VelocityHigh {
			return true
		}
	}
	return false
}

// ResidentCap tallies the decoded PCM of the packs that one native host loads
// for a project, and refuses the pack that takes the total past its limit.
type ResidentCap struct {
	limit, used int64
}

// NewResidentCap starts a tally against limit bytes. Zero selects
// DefaultMaxResidentPCM; a negative limit is an error.
func NewResidentCap(limit int64) (*ResidentCap, error) {
	if limit < 0 {
		return nil, fmt.Errorf("resident PCM cap must not be negative")
	}
	if limit == 0 {
		limit = DefaultMaxResidentPCM
	}
	return &ResidentCap{limit: limit}, nil
}

// Add counts one loaded pack, named by its manifest path. It returns a
// *ResidentPCMError when the packs counted so far hold more PCM than the cap.
func (c *ResidentCap) Add(manifest string, p *Prepared) error {
	c.used += p.ResidentPCMBytes()
	if c.used > c.limit {
		return &ResidentPCMError{Pack: manifest, Bytes: c.used, Limit: c.limit}
	}
	return nil
}

// ResidentPCMError reports that the sampler packs of a native host decode to
// more PCM than the cap allows. The tally stops at the pack that crossed the
// cap, so Bytes is what was measured by then, not the whole project's total.
type ResidentPCMError struct {
	Pack  string // manifest path of the pack that crossed the cap
	Bytes int64  // decoded PCM measured, including that pack
	Limit int64  // the cap in force
}

func (e *ResidentPCMError) Error() string {
	return fmt.Sprintf("sampler packs exceed the native resident PCM cap (--max-resident-pcm): %s decoded after loading %s, cap %s",
		FormatBytes(e.Bytes), e.Pack, FormatBytes(e.Limit))
}

// FormatBytes spells a byte count in binary units and, above 1 MiB, in bytes
// too, such as "1 GiB (1073741824 bytes)" or "68.66 MiB (72000000 bytes)".
func FormatBytes(n int64) string {
	const mib, gib = 1 << 20, 1 << 30
	switch {
	case n >= gib && n%gib == 0:
		return fmt.Sprintf("%d GiB (%d bytes)", n/gib, n)
	case n >= gib:
		return fmt.Sprintf("%.2f GiB (%d bytes)", float64(n)/gib, n)
	case n >= mib && n%mib == 0:
		return fmt.Sprintf("%d MiB (%d bytes)", n/mib, n)
	case n >= mib:
		return fmt.Sprintf("%.2f MiB (%d bytes)", float64(n)/mib, n)
	}
	return fmt.Sprintf("%d bytes", n)
}
