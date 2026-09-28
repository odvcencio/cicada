//go:build !linux || (!amd64 && !arm64)

package rt

func RaiseProcessThreads(int) (restore func(), raised int, err error) {
	return func() {}, 0, ErrUnsupported
}
