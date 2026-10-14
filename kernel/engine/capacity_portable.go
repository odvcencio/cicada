//go:build tinygo || js

package engine

type trackMask = uint32

// messageCapacity holds one emission of stereo meters for the sixteen portable
// tracks (six messages each) and the other status messages of a block.
const messageCapacity = 256
