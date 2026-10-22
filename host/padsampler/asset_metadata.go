package padsampler

// assetMetadata is the portable JSON view of a project audio asset. Keep this
// wire contract independent of compiler, filesystem and audio engine packages.
type assetMetadata struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Format   string `json:"format"`
	Frames   int64  `json:"frames"`
	RateHz   int    `json:"rate_hz"`
	Channels int    `json:"channels"`
	Source   string `json:"source,omitempty"`
}
