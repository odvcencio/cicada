package lsp

import (
	"os"
	"testing"

	"m31labs.dev/cicada/internal/isolatetest"
)

// Definitions into the standard library are served from an editor cache under
// the per-user cache directory. Go ignores XDG_CACHE_HOME on macOS, so only
// appdirs.StateEnv keeps that cache out of the developer's real Library.
func TestMain(m *testing.M) { os.Exit(isolatetest.Run(m)) }
