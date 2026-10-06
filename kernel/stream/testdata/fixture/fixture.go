// Package fixture drives the same guarded pages, seeks and underruns in Go and
// TinyGo. Storage service is simulated between callback blocks.
package fixture

import "m31labs.dev/cicada/kernel/stream"

const Frames = 16384

func Render(blockSize int, output []float32) error {
	c, err := stream.New(stream.Config{Pages: 6, Readers: 1, AheadPages: 3}, []stream.Asset{{ID: 1, Frames: 48000 * 3600, RateHz: 48000, Channels: 2}})
	if err != nil {
		return err
	}
	r, err := c.NewReader(1)
	if err != nil {
		return err
	}
	r.SeekFrame(0, 1)
	for frame := 0; frame < Frames; frame += blockSize {
		switch frame {
		case 4096:
			r.SeekFrame(100*stream.PageFrames+3, 1)
		case 8192:
			r.SeekFrame(200*stream.PageFrames+7, -1)
		case 12288:
			r.SeekFrame(48000*3600-100, 1)
		}
		if frame < 4096 || frame >= 4608 {
			for {
				work, ok := c.Next()
				if !ok {
					break
				}
				l, rr := work.Left(), work.Right()
				for i := range l {
					l[i] = float32((work.Start+int64(i))%251-125) / 256
					rr[i] = -l[i]
				}
				work.Publish()
			}
		}
		r.Render(output[frame:frame+blockSize], output[Frames+frame:Frames+frame+blockSize])
	}
	r.Stop()
	return nil
}
