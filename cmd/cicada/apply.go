package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/editlog"
)

func applyCommand(args []string) error {
	return applyCommandWithHooks(args, os.Stdout, nil)
}

func applyCommandWithHooks(args []string, stdout io.Writer, beforeSwap func()) error {
	var paths []string
	var write bool
	var author, session string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--write":
			write = true
		case "--author", "--session":
			flag := args[i]
			i++
			if i == len(args) {
				return fmt.Errorf("%s needs a value", flag)
			}
			if flag == "--author" {
				author = args[i]
			} else {
				session = args[i]
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown apply flag %q", args[i])
			}
			paths = append(paths, args[i])
		}
	}
	if len(paths) != 2 {
		return fmt.Errorf("usage: cicada apply <score.cicada> <intents.json> [--write] [--author <name>] [--session <id>]")
	}
	path, err := filepath.Abs(paths[0])
	if err != nil {
		return err
	}
	data, err := os.ReadFile(paths[1])
	if err != nil {
		return err
	}
	var env edits.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	before, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	revision := edits.Revision(before)
	if env.Revision != "" && env.Revision != revision {
		return fmt.Errorf("score changed; intents target revision %s, file is at %s", env.Revision, revision)
	}
	env.Revision = revision
	if author != "" {
		env.Author = author
	}
	if env.Author == "" {
		env.Author = "cli"
	}
	if session != "" {
		env.Session = session
	}
	s := &studio{path: path}
	opts, err := s.editOptions(before, nil)
	if err != nil {
		return err
	}
	result, err := edits.Apply(before, env, opts)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(stdout, intentDiff(before, result)); err != nil {
		return err
	}
	if !write || env.DryRun {
		return nil
	}
	files, err := auxiliaryFiles(result.Files)
	if err != nil {
		return err
	}
	changedFiles := files[:0]
	for _, file := range files {
		if !bytes.Equal(file.Before, file.After) || (file.Before == nil) != (file.After == nil) {
			changedFiles = append(changedFiles, file)
		}
	}
	if bytes.Equal(before, result.Source) && len(changedFiles) == 0 {
		return nil
	}
	if err := studioRecoveryConflict(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err := writeFixedRevision(path, before, result.Source, info.Mode().Perm(), beforeSwap); err != nil {
		return err
	}
	if err := studioWriteAuxiliaryFiles(changedFiles); err != nil {
		rollbackErr := writeFixedRevision(path, result.Source, before, info.Mode().Perm(), nil)
		if rollbackErr != nil {
			return fmt.Errorf("source auxiliary write failed: %w; source rollback failed: %v", err, rollbackErr)
		}
		return fmt.Errorf("source auxiliary write failed: %w", err)
	}
	record, err := editlog.Commit(env, revision, edits.Revision(result.Source), result.Label, time.Now().UTC())
	if err == nil {
		err = editlog.AppendCommit(editlog.Path(path), record)
	}
	if err != nil {
		return fmt.Errorf("score written; edit log failed: %w", err)
	}
	return nil
}
