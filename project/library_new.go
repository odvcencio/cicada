package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/edition"
)

// NewLibrary creates a complete scaffold without replacing an existing path.
func NewLibrary(name, dir string) error {
	if !edition.ValidLibraryPath(name) || name == "std" || strings.HasPrefix(name, "std/") {
		return fmt.Errorf("CICADA-LIB-PATH: expected a non-std library path")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("library destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".cicada-new-library-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	manifest := fmt.Sprintf("library %s\ncicada 2\nsource \"main.cicada\"\nlicense \"UNLICENSED\"\nauthor \"Library authors\"\n", name)
	for file, data := range map[string]string{
		"cicada.mod":  manifest,
		"main.cicada": "// Replace this instrument with your own declarations.\ninstrument tone { voice mono { out = sine(pitch) * env(gate,90ms) } }\n",
	} {
		if err := os.WriteFile(filepath.Join(stage, file), []byte(data), 0644); err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	_, err = readLibrary(name, "user", stage, root.FS(), nil)
	root.Close()
	if err != nil {
		return err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("library destination appeared during creation")
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(stage, dir)
}
