//go:build !darwin || (!amd64 && !arm64)

package midi

import "context"

func openNative(context.Context) (Stream, error) { return nil, ErrUnsupported }
