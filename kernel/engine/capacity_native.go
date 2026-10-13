//go:build !tinygo && !js

package engine

type trackMask = uint64

// messageCapacity holds one emission of stereo meters for every native track
// (six messages each) beside the other status messages of a block.
const messageCapacity = 512
