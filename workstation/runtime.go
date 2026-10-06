package main

import (
	"os"
	"path/filepath"
)

// GoSX bundles live beside the installed app. Development can use dist/;
// no absolute source-checkout path is built into the installed executable.
func runtimeRoot() string {
	self, _ := os.Executable()
	dir := filepath.Dir(self)
	for _, candidate := range []string{filepath.Join(dir, "workstation"), filepath.Dir(dir), "dist"} {
		if info, err := os.Stat(filepath.Join(candidate, "build.json")); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}
