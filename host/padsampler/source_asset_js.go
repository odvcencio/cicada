//go:build js && wasm

package padsampler

// Browser projections need metadata, never the project compiler or DSP engine.
type sourceAsset = assetMetadata
