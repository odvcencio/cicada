package instrumentpack

import (
	"fmt"
	"strings"
	"sync/atomic"

	"m31labs.dev/cicada/kernel/voice/sample"
)

// DefaultMaxResidentPCM is the default cap, in bytes, on the decoded PCM of all
// sampler packs that one native render, play or Studio session holds in
// memory. It is separate from MaxPCMBytes, which caps each pack, and from the
// 64 MiB resident budget of the browser image and WAM2 export.
const DefaultMaxResidentPCM int64 = 1 << 30

// processMaxResidentPCM is the cap that a zero limit selects. A command sets it
// once at startup (SetDefaultMaxResidentPCM); zero means DefaultMaxResidentPCM.
var processMaxResidentPCM atomic.Int64

// SetDefaultMaxResidentPCM changes the cap that NewResidentCap(0) selects for
// the rest of the process, for a host such as Studio whose compiles are too
// many to pass a limit to each. Zero restores DefaultMaxResidentPCM. A caller
// that passes its own limit is not affected.
func SetDefaultMaxResidentPCM(limit int64) error {
	if limit < 0 {
		return fmt.Errorf("resident PCM cap must not be negative")
	}
	processMaxResidentPCM.Store(limit)
	return nil
}

func defaultMaxResidentPCM() int64 {
	if limit := processMaxResidentPCM.Load(); limit > 0 {
		return limit
	}
	return DefaultMaxResidentPCM
}

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

// ZoneMap is the zone table of a pack without its audio: which keys and
// velocities start a sound. A caller that only needs the table, such as the
// score check, keeps a ZoneMap and lets the PCM go.
type ZoneMap []sample.Zone

// ZoneMap copies the pack's zones and drops their PCM.
func (p *Prepared) ZoneMap() ZoneMap {
	zones := make(ZoneMap, len(p.Zones))
	for i, z := range p.Zones {
		z.Region = sample.Region{}
		zones[i] = z
	}
	return zones
}

// HasAttackZone reports whether a note at this velocity can start a sound: some
// zone that is not a release zone holds the note in its key range and the
// velocity in its velocity range. sample.Instrument.NoteOn refuses any other
// note, and the engine then stops with fault 29. Velocity 0 never plays, so it
// reports false.
func (m ZoneMap) HasAttackZone(note, velocity uint8) bool {
	for _, z := range m {
		if !z.Release && note >= z.KeyLow && note <= z.KeyHigh && velocity >= z.VelocityLow && velocity <= z.VelocityHigh {
			return true
		}
	}
	return false
}

// HasAttackZone is ZoneMap.HasAttackZone for the pack's own zones.
func (p *Prepared) HasAttackZone(note, velocity uint8) bool {
	return ZoneMap(p.Zones).HasAttackZone(note, velocity)
}

// ResidentCap tallies the decoded PCM of the packs that one native host loads
// for a project, and refuses the pack that takes the total past its limit. It
// counts from the manifests, before any audio is read. A ResidentCap belongs to
// one compile and is not safe for concurrent use.
type ResidentCap struct {
	limit, used int64
	counted     map[packKey]bool
}

// NewResidentCap starts a tally against limit bytes. Zero selects the default
// cap, DefaultMaxResidentPCM unless SetDefaultMaxResidentPCM changed it; a
// negative limit is an error.
func NewResidentCap(limit int64) (*ResidentCap, error) {
	if limit < 0 {
		return nil, fmt.Errorf("resident PCM cap must not be negative")
	}
	if limit == 0 {
		limit = defaultMaxResidentPCM()
	}
	return &ResidentCap{limit: limit}, nil
}

// Limit is the cap in force, in bytes.
func (c *ResidentCap) Limit() int64 { return c.limit }

// Reserve counts the PCM that decoding the pack will leave in memory, from its
// manifest, before the audio is read. It returns a *ResidentPCMError when the
// packs counted so far would hold more PCM than the cap. A pack that several
// samplers name counts once, because they share one decoded copy.
func (c *ResidentCap) Reserve(pack *Pack) error {
	key := pack.key()
	if c.counted[key] {
		return nil
	}
	if c.counted == nil {
		c.counted = map[packKey]bool{}
	}
	c.counted[key] = true
	c.used += pack.ResidentPCMBytes()
	if c.used > c.limit {
		return &ResidentPCMError{Pack: pack.manifestPath, Bytes: c.used, Limit: c.limit}
	}
	return nil
}

// Open reads the manifest of a pack and counts its PCM against the cap, before
// any audio is read. It returns a *ResidentPCMError when the packs counted so
// far would hold more PCM than the cap.
//
// A host with several packs opens every one first and loads their audio only
// after that, with Pack.LoadShared(rate, Limit()), so that a project over the cap
// is refused before any of its audio is decoded.
func (c *ResidentCap) Open(dir, manifestPath, pin string) (*Pack, error) {
	pack, err := Open(dir, manifestPath, pin)
	if err != nil {
		return nil, err
	}
	if err = c.Reserve(pack); err != nil {
		return nil, err
	}
	return pack, nil
}

// Load opens the pack, reserves its PCM against the cap, and only then decodes
// it, so a pack over the cap is refused before it allocates any audio. It suits
// a host with one pack; a host with several uses Open for each and then loads
// them, so that the cap sees the whole project before any audio is read.
//
// The decoded PCM is shared with every other load of the same pack in the
// process (see Pack.LoadShared); the returned Prepared is the caller's own and
// may have its manifest configuration changed, but its PCM must not be written.
func (c *ResidentCap) Load(dir, manifestPath, pin string, rate int) (*Prepared, error) {
	pack, err := c.Open(dir, manifestPath, pin)
	if err != nil {
		return nil, err
	}
	return pack.LoadShared(rate, c.limit)
}

// ResidentPCMError reports that the sampler packs of a native host decode to
// more PCM than the cap allows. The tally stops at the pack that crossed the
// cap, so Bytes is what the packs up to it would hold, not the whole project's
// total.
type ResidentPCMError struct {
	Pack  string // manifest path of the pack that crossed the cap
	Bytes int64  // PCM the packs would hold, including that pack
	Limit int64  // the cap in force
}

func (e *ResidentPCMError) Error() string {
	return fmt.Sprintf("sampler packs exceed the native resident PCM cap (--max-resident-pcm): loading %s would hold %s of decoded PCM, cap %s",
		e.Pack, FormatBytes(e.Bytes), FormatBytes(e.Limit))
}

// FormatBytes spells a byte count in binary units and, above 1 MiB, in bytes
// too, such as "1 GiB (1073741824 bytes)" or "68.66 MiB (72000000 bytes)". A
// size within a hundredth of a unit of a whole number of units, but not equal to
// it, says "just under" or "just over", so that two sizes which differ by a
// byte do not print alike: "just under 256 MiB (268435455 bytes)".
func FormatBytes(n int64) string {
	const mib, gib = 1 << 20, 1 << 30
	switch {
	case n >= gib && n%gib == 0:
		return fmt.Sprintf("%d GiB (%d bytes)", n/gib, n)
	case n >= gib:
		return approximateBytes(n, gib, "GiB")
	case n >= mib && n%mib == 0:
		return fmt.Sprintf("%d MiB (%d bytes)", n/mib, n)
	case n >= mib:
		return approximateBytes(n, mib, "MiB")
	}
	return fmt.Sprintf("%d bytes", n)
}

// approximateBytes spells n, which is not a whole number of units, in units to
// two decimals.
func approximateBytes(n, unit int64, name string) string {
	spelled := fmt.Sprintf("%.2f", float64(n)/float64(unit))
	if strings.HasSuffix(spelled, ".00") {
		whole := (n + unit/2) / unit
		relation := "just over"
		if n < whole*unit {
			relation = "just under"
		}
		if name == "MiB" && whole == 1024 {
			whole, name = 1, "GiB"
		}
		return fmt.Sprintf("%s %d %s (%d bytes)", relation, whole, name, n)
	}
	return fmt.Sprintf("%s %s (%d bytes)", spelled, name, n)
}
