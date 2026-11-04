package control

import "math"

// MIDIPickup waits for a physical controller to reach or cross the current
// parameter target before forwarding changes. A nil target is the explicit
// off value; callers must distinguish that from an unavailable snapshot.
type MIDIPickup struct {
	acquired     bool
	previous     *float64
	havePrevious bool
	lastSent     *float64
	lastSentMS   int64
}

func (p *MIDIPickup) Apply(d Descriptor, cc int, target *float64) (*float64, bool, error) {
	return p.ApplyAt(d, cc, target, 0)
}

// ApplyAt rearms pickup after another control changes the target. The grace
// period allows the asynchronous transport snapshot to catch up with MIDI.
func (p *MIDIPickup) ApplyAt(d Descriptor, cc int, target *float64, nowMS int64) (*float64, bool, error) {
	value, err := CCValue(d, cc)
	if err != nil {
		return nil, false, err
	}
	if p.acquired && nowMS > 0 && nowMS-p.lastSentMS > 500 && pickupValue(target) != pickupValue(p.lastSent) {
		p.acquired, p.havePrevious = false, false
	}
	if p.acquired {
		p.lastSent, p.lastSentMS = value, nowMS
		return value, true, nil
	}
	if value == nil && target == nil {
		p.acquired = true
	} else if value != nil && target != nil {
		low, _ := CCValue(d, max(0, cc-1))
		high, _ := CCValue(d, min(127, cc+1))
		if pickupValue(low) <= pickupValue(target) && pickupValue(target) <= pickupValue(high) {
			p.acquired = true
		} else if p.havePrevious && ((pickupValue(p.previous) <= pickupValue(target) && pickupValue(value) >= pickupValue(target)) || (pickupValue(p.previous) >= pickupValue(target) && pickupValue(value) <= pickupValue(target))) {
			p.acquired = true
		}
	}
	p.previous, p.havePrevious = value, true
	if p.acquired {
		p.lastSent, p.lastSentMS = value, nowMS
	}
	return value, p.acquired, nil
}

func pickupValue(v *float64) float32 {
	if v == nil {
		return float32(math.Inf(-1))
	}
	return float32(*v)
}
