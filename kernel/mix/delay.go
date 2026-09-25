package mix

// Delay aligns tracks without an insert to the latency of inserted effects.
// Storage is allocated during project load, never in the audio callback.
type Delay struct {
	buffer   []stereo
	position int
}

func NewDelay(frames int) (*Delay, error) {
	if frames < 1 || frames > 4096 {
		return nil, Error("track compensation delay must be 1 to 4096 frames")
	}
	return &Delay{buffer: make([]stereo, frames)}, nil
}

func (d *Delay) Process(left, right float32) (float32, float32) {
	previous := d.buffer[d.position]
	d.buffer[d.position] = stereo{left, right}
	d.position++
	if d.position == len(d.buffer) {
		d.position = 0
	}
	return previous.left, previous.right
}

func (d *Delay) Reset() {
	clear(d.buffer)
	d.position = 0
}
