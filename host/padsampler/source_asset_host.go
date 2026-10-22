//go:build !js || !wasm

package padsampler

import "m31labs.dev/cicada/project"

// Preserve the host API and its private project-root confinement metadata.
type sourceAsset = project.Asset
