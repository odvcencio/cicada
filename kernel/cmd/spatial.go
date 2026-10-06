package cmd

import "math"

// TrackPosition enables 3D positioning in metres (X forward, Y left, Z up).
// Positive ticks use the existing absolute musical transport clock.
func TrackPosition(track uint8, x, y, z float32, tick int64) Command {
	return Command{Op: OpSetTrackPosition, Track: track, Index: 1, Arg0: math.Float32bits(x), Arg1: math.Float32bits(y), Pad: math.Float32bits(z), Tick: tick}
}

func TrackStereo(track uint8, tick int64) Command {
	return Command{Op: OpSetTrackPosition, Track: track, Tick: tick}
}

func ListenerPosition(x, y, z float32, tick int64) Command {
	return Command{Op: OpSetListenerPosition, Track: 255, Arg0: math.Float32bits(x), Arg1: math.Float32bits(y), Pad: math.Float32bits(z), Tick: tick}
}

// ListenerRotation sets yaw/pitch/roll radians, each in [-2*pi, 2*pi].
func ListenerRotation(yaw, pitch, roll float32, tick int64) Command {
	return Command{Op: OpSetListenerRotation, Track: 255, Arg0: math.Float32bits(yaw), Arg1: math.Float32bits(pitch), Pad: math.Float32bits(roll), Tick: tick}
}
