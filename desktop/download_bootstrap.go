package main

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
)

//go:embed testdata/download-bootstrap.js
var nativeDownloadBootstrap string

// Test destinations come only from the command line, below the smoke report.
// A page cannot supply a filesystem path or enable the picker bypass.
func nativeSmokeDownloadDir(report, directory string) (string, error) {
	if directory == "" {
		return "", nil
	}
	if report == "" {
		return "", fmt.Errorf("--smoke-download-dir requires --smoke-out")
	}
	root, err := filepath.Abs(filepath.Dir(report))
	if err != nil {
		return "", err
	}
	destination, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("--smoke-download-dir must be below the smoke report's directory")
	}
	return destination, nil
}
