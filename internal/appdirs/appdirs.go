// Package appdirs resolves the per-user directories Cicada writes to.
//
// By default Studio state lives in the OS config directory and caches in the
// OS cache directory, exactly as os.UserConfigDir and os.UserCacheDir report
// them. Setting CICADA_STATE_DIR moves both beneath one root instead.
//
// Nothing in the product sets that variable; tests do (see internal/isolatetest).
// They need it because Go honors XDG_CONFIG_HOME and XDG_CACHE_HOME only on
// Linux and the BSDs. On macOS os.UserConfigDir and os.UserCacheDir always
// return $HOME/Library paths, so pointing the XDG variables at a temporary
// directory does not keep test files out of the developer's real Library.
package appdirs

import (
	"os"
	"path/filepath"
)

// StateEnv names the variable that relocates Cicada's per-user directories.
const StateEnv = "CICADA_STATE_DIR"

// StateDir returns the directory for durable per-user Studio state, such as the
// recovery files that keep a displaced score reachable after a save.
func StateDir() (string, error) {
	if root, set, err := override(); set {
		return root, err
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Cicada Studio"), nil
}

// CacheDir returns the directory for regenerable per-user files, such as the
// editor copies of embedded library sources. When StateEnv is set it is the
// cache subdirectory of that root, so a single variable relocates everything.
func CacheDir() (string, error) {
	if root, set, err := override(); set {
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "cache"), nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "cicada"), nil
}

// override reports the absolute StateEnv root. An empty value counts as unset,
// matching how CICADA_LIBRARY is read.
func override() (root string, set bool, err error) {
	value := os.Getenv(StateEnv)
	if value == "" {
		return "", false, nil
	}
	root, err = filepath.Abs(value)
	return root, true, err
}
