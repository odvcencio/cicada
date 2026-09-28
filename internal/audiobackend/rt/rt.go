package rt

import "errors"

var ErrUnsupported = errors.New("process thread priority is unavailable on this platform")
