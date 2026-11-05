//go:build !tinygo || modular

package kernelimage

// modularFlags are the header flag bits this build decodes: bit 1, the
// modulator table (spec 24, lane 1). The default browser core leaves them out,
// so it refuses a flagged image before decoding it.
const modularFlags uint32 = 1<<1 | 1<<2 | 1<<3 | 1<<4 | 1<<6
