//go:build tinygo

package engine

// returnsEnabled is false in portable kernels, which hold one delay or tape
// return and one reverb return. NewFromConfig then ignores Config.Routing.
// TODO(image version 19): when an image can carry return slots C to H, build
// them here or refuse Routing; until then no image carries it
// (host/kernelimage TestNoImageVersionCarriesRoutingYet).
const returnsEnabled = false
