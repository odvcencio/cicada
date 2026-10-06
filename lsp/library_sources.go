package lsp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type libraryContext struct{ entry, source string }

func (s *server) librarySourceURI(sources *project.Sources, lib *project.Library, file notation.SourceFile) (string, error) {
	if _, ok := s.libraryContexts[fileURI(file.Path)]; ok {
		return fileURI(file.Path), nil
	}
	uri, err := librarySourceURI(lib, file)
	if err == nil && lib != nil && lib.Kind == "std" {
		if s.libraryContexts == nil {
			s.libraryContexts = map[string]libraryContext{}
		}
		s.libraryContexts[uri] = libraryContext{entry: sources.Files[0].Path, source: file.Path}
	}
	return uri, err
}

// Embedded definitions are copied to a content-addressed editor cache so
// ordinary file URI clients can open exactly the source used by the loader.
func librarySourceURI(lib *project.Library, file notation.SourceFile) (string, error) {
	if lib == nil || lib.Kind != "std" {
		return fileURI(file.Path), nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(lib.Root, file.Path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("invalid embedded source path")
	}
	filename := filepath.Join(cache, "cicada", "lsp", lib.SHA256, filepath.FromSlash(lib.Path), relative)
	if data, err := os.ReadFile(filename); err == nil && bytes.Equal(data, file.Source) {
		return fileURI(filename), nil
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return "", err
	}
	staged, err := os.CreateTemp(filepath.Dir(filename), ".source-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(staged.Name())
	if _, err := staged.Write(file.Source); err != nil {
		staged.Close()
		return "", err
	}
	if err := staged.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(staged.Name(), filename); err != nil {
		return "", err
	}
	return fileURI(filename), nil
}
