//go:build !tinygo && !js

package engine

type trackMask = uint64

// messageCapacity holds one emission of stereo meters for every native track
// (six messages each, 384 for 64 tracks) plus the sixteen other meter messages,
// beside the other status messages of a block.
const messageCapacity = 512
