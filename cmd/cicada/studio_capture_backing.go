package main

import "m31labs.dev/cicada/project"

// captureBacking uses the same prepared sample-capable engine as playback.
// Existing clips, sampler parts and previous takes remain accompaniment;
// captureInput taps the device input before that mix and input monitoring.
// Engine construction copies mutable playback state without changing the score.
func captureBacking(p *project.Project) *project.Project { return p }
