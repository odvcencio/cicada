// Package spatial implements first-order AmbiX encoding, listener rotation,
// and bounded headphone decoding. Coordinates are metres: X forward, Y left,
// Z up. B-format channels use ACN order and SN3D normalization (W, Y, Z, X).
package spatial

import "math"

type Vec3 struct{ X, Y, Z float32 }

type BFormat struct{ W, Y, Z, X float32 }

// Frame contains the listener-relative buses before headphone decoding,
// music-bus gain, compression, and limiting.
type Frame struct{ Music, SFX BFormat }

// Coefficients encodes a point relative to a listener. Gain follows 1/distance
// beyond one metre and stays at unity closer in. A coincident point is omni.
func Coefficients(source, listener Vec3) BFormat {
	x, y, z := float64(source.X)-float64(listener.X), float64(source.Y)-float64(listener.Y), float64(source.Z)-float64(listener.Z)
	distance := math.Sqrt(float64(x*x) + float64(y*y) + float64(z*z))
	if distance == 0 {
		return BFormat{W: 1}
	}
	gain := 1 / math.Max(1, distance)
	return BFormat{W: float32(gain), X: float32(gain * x / distance), Y: float32(gain * y / distance), Z: float32(gain * z / distance)}
}

func (b *BFormat) Add(sample float32, coefficients BFormat) {
	b.W += float32(sample * coefficients.W)
	b.Y += float32(sample * coefficients.Y)
	b.Z += float32(sample * coefficients.Z)
	b.X += float32(sample * coefficients.X)
}

// Rotation transforms world directions into listener coordinates. Positive
// yaw turns left, positive pitch looks up, and positive roll raises the left
// ear. The listener's local-to-world order is yaw, pitch, then roll.
type Rotation [3][3]float32

func ListenerRotation(yaw, pitch, roll float32) Rotation {
	cy, sy := math.Cos(float64(yaw)), math.Sin(float64(yaw))
	cp, sp := math.Cos(float64(pitch)), math.Sin(float64(pitch))
	cr, sr := math.Cos(float64(roll)), math.Sin(float64(roll))
	// Transpose of Rz(yaw) * Ry(-pitch) * Rx(roll).
	return Rotation{
		{float32(cy * cp), float32(sy * cp), float32(sp)},
		{float32(float64(-sy*cr) - float64(float64(cy*sp)*sr)), float32(float64(cy*cr) - float64(float64(sy*sp)*sr)), float32(cp * sr)},
		{float32(float64(sy*sr) - float64(float64(cy*sp)*cr)), float32(float64(-cy*sr) - float64(float64(sy*sp)*cr)), float32(cp * cr)},
	}
}

func (r Rotation) Apply(b BFormat) BFormat {
	return BFormat{W: b.W,
		X: float32(r[0][0]*b.X) + float32(r[0][1]*b.Y) + float32(r[0][2]*b.Z),
		Y: float32(r[1][0]*b.X) + float32(r[1][1]*b.Y) + float32(r[1][2]*b.Z),
		Z: float32(r[2][0]*b.X) + float32(r[2][1]*b.Y) + float32(r[2][2]*b.Z)}
}
