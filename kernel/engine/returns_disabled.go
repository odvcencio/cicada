//go:build tinygo

package engine

// returnsEnabled is false in portable kernels, which hold one delay or tape
// return and one reverb return.
const returnsEnabled = false
