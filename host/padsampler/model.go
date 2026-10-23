// Package padsampler prepares immutable pad sounds and prints performances.
// Its wire types are shared with browser clients through protocol.
package padsampler

import (
	"m31labs.dev/cicada/host/padsampler/protocol"
	"m31labs.dev/cicada/project"
)

const Rate = protocol.Rate
const MaxFrames = protocol.MaxFrames

type Pad = protocol.Pad
type Effects = protocol.Effects
type Hit = protocol.Hit
type Performance = protocol.Performance
type Source = protocol.Source[project.Asset]
type Bank = protocol.Bank[project.Asset]
