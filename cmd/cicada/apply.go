package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/edit/rendercheck"
	"m31labs.dev/cicada/internal/filetarget"
)

const applyUsage = "usage: cicada apply <score.cicada> <intents.json> [--write] [--json] [--author <name>] [--session <id>]"

func applyCommand(args []string) error {
	return applyCommandWithHooks(args, os.Stdout, nil)
}

// applyResult is what one apply run did, for the text and JSON outputs.
type applyResult struct {
	path            string
	diff            string
	revisionBefore  string
	revisionAfter   string
	written, dryRun bool
	unchanged       bool
	intents         []edits.Intent
	labels          []string
	files           []string
}

// applyFailure is an apply error with the stage it happened in.
type applyFailure struct {
	stage string
	err   error
}

func (e *applyFailure) Error() string {
	var located *edits.IntentError
	if errors.As(e.err, &located) {
		return located.Location() + ": " + e.err.Error()
	}
	return e.err.Error()
}

func (e *applyFailure) Unwrap() error { return e.err }

func applyCommandWithHooks(args []string, stdout io.Writer, beforeSwap func()) error {
	var paths []string
	var write, asJSON bool
	var author, session string
	var usageErr error
	for i := 0; i < len(args) && usageErr == nil; i++ {
		switch args[i] {
		case "--write":
			write = true
		case "--json":
			asJSON = true
		case "--author", "--session":
			flag := args[i]
			i++
			if i == len(args) {
				usageErr = fmt.Errorf("%s needs a value", flag)
				break
			}
			if flag == "--author" {
				author = args[i]
			} else {
				session = args[i]
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				usageErr = fmt.Errorf("unknown apply flag %q", args[i])
				break
			}
			paths = append(paths, args[i])
		}
	}
	if usageErr == nil && len(paths) != 2 {
		usageErr = fmt.Errorf("%s", applyUsage)
	}
	var result applyResult
	var err error
	if usageErr != nil {
		err = &applyFailure{stage: "usage", err: usageErr}
	} else {
		result, err = runApply(paths[0], paths[1], write, author, session, beforeSwap)
	}
	if asJSON {
		if writeErr := writeApplyJSON(stdout, result, err); writeErr != nil {
			return writeErr
		}
		return err
	}
	// The diff comes before a write, so a failed write still shows it.
	if _, writeErr := io.WriteString(stdout, result.diff); writeErr != nil {
		return writeErr
	}
	return err
}

func runApply(scorePath, intentsPath string, write bool, author, session string, beforeSwap func()) (applyResult, error) {
	result := applyResult{path: scorePath}
	fail := func(stage string, err error) (applyResult, error) {
		return result, &applyFailure{stage: stage, err: err}
	}
	target, err := filetarget.Resolve(scorePath, filetarget.Existing)
	if err != nil {
		return fail("read", err)
	}
	path := target.Logical
	data, err := os.ReadFile(intentsPath)
	if err != nil {
		return fail("read", err)
	}
	var env edits.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fail("decode", err)
	}
	result.intents, result.dryRun = env.Intents, env.DryRun
	before, err := os.ReadFile(path)
	if err != nil {
		return fail("read", err)
	}
	revision := edits.Revision(before)
	result.revisionBefore = revision
	if env.Revision != "" && env.Revision != revision {
		return fail("revision", fmt.Errorf("score changed; intents target revision %s, file is at %s", env.Revision, revision))
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
	s := &studio{path: path, renderCheck: rendercheck.New(path)}
	opts, err := s.editOptions(before, nil)
	if err != nil {
		return fail("read", err)
	}
	applied, err := edits.Apply(before, env, opts)
	if err != nil {
		var located *edits.IntentError
		if errors.As(err, &located) {
			return fail("apply", err)
		}
		return fail("validate", err)
	}
	result.diff = intentDiff(before, applied, filepath.Base(path))
	result.revisionAfter = edits.Revision(applied.Source)
	result.labels = applied.Labels
	for _, file := range applied.Files {
		if fileChanged(file.Before, file.After) {
			result.files = append(result.files, file.Path)
		}
	}
	result.unchanged = bytes.Equal(before, applied.Source) && len(result.files) == 0
	if !write || env.DryRun || result.unchanged {
		return result, nil
	}
	files, err := auxiliaryFiles(applied.Files)
	if err != nil {
		return fail("write", err)
	}
	changedFiles := files[:0]
	for _, file := range files {
		if fileChanged(file.Before, file.After) {
			changedFiles = append(changedFiles, file)
		}
	}
	if err := studioRecoveryConflict(path); err != nil {
		return fail("write", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fail("write", err)
	}
	if err := writeFixedRevisionChecked(path, before, applied.Source, info.Mode().Perm(), beforeSwap, func() error {
		return s.verifyRenderIdentity(applied.RenderChecked, applied.RenderIdentity, applied.RenderRecheck)
	}); err != nil {
		return fail("write", err)
	}
	if err := studioWriteAuxiliaryFiles(changedFiles); err != nil {
		rollbackErr := writeFixedRevision(path, applied.Source, before, info.Mode().Perm(), nil)
		if rollbackErr != nil {
			return fail("write", fmt.Errorf("source auxiliary write failed: %w; source rollback failed: %v", err, rollbackErr))
		}
		return fail("write", fmt.Errorf("source auxiliary write failed: %w", err))
	}
	result.written = true
	record, err := editlog.Commit(env, revision, edits.Revision(applied.Source), applied.Label, time.Now().UTC())
	if err == nil {
		err = editlog.AppendCommit(editlog.Path(path), record)
	}
	if err != nil {
		return fail("write", fmt.Errorf("score written; edit log failed: %w", err))
	}
	return result, nil
}

func fileChanged(before, after []byte) bool {
	return !bytes.Equal(before, after) || (before == nil) != (after == nil)
}

// applySchema names the format of cicada apply --json.
const applySchema = "cicada.apply.v1"

type applyDocument struct {
	Schema         string            `json:"schema"`
	Score          string            `json:"score,omitempty"`
	OK             bool              `json:"ok"`
	Written        bool              `json:"written"`
	DryRun         bool              `json:"dry_run"`
	Changed        bool              `json:"changed"`
	RevisionBefore string            `json:"revision_before,omitempty"`
	RevisionAfter  string            `json:"revision_after,omitempty"`
	Intents        []applyIntentJSON `json:"intents"`
	Files          []string          `json:"files,omitempty"`
	Diff           string            `json:"diff"`
	Error          *applyErrorJSON   `json:"error,omitempty"`
}

// applyIntentJSON is one intent of the batch and what it did, in the words of
// the edit history.
type applyIntentJSON struct {
	Index int    `json:"index"`
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

// applyErrorJSON says where an apply failed. Index, kind and field name the
// intent at fault when the error is about one; code, reason and note carry a
// note refusal of editnotes as data.
type applyErrorJSON struct {
	Stage   string         `json:"stage"`
	Message string         `json:"message"`
	Index   *int           `json:"index,omitempty"`
	Kind    string         `json:"kind,omitempty"`
	Field   string         `json:"field,omitempty"`
	Code    string         `json:"code,omitempty"`
	Reason  string         `json:"reason,omitempty"`
	Note    map[string]any `json:"note,omitempty"`
}

func writeApplyJSON(w io.Writer, result applyResult, err error) error {
	document := applyDocument{Schema: applySchema, Score: result.path, OK: err == nil, Written: result.written, DryRun: result.dryRun, Changed: result.revisionAfter != "" && !result.unchanged, RevisionBefore: result.revisionBefore, RevisionAfter: result.revisionAfter, Intents: []applyIntentJSON{}, Files: result.files, Diff: result.diff}
	for i, intent := range result.intents {
		entry := applyIntentJSON{Index: i, Kind: intent.Kind()}
		if i < len(result.labels) {
			entry.Label = result.labels[i]
		}
		document.Intents = append(document.Intents, entry)
	}
	for i := range document.Files {
		document.Files[i] = displayPath(document.Files[i])
	}
	if err != nil {
		failure := &applyErrorJSON{Stage: "apply", Message: err.Error()}
		var stage *applyFailure
		if errors.As(err, &stage) {
			failure.Stage = stage.stage
		}
		var located *edits.IntentError
		if errors.As(err, &located) {
			index := located.Index
			failure.Index, failure.Kind, failure.Field = &index, located.Kind, located.Field
			failure.Message = located.Err.Error()
		}
		var refusal *edits.NoteError
		if errors.As(err, &refusal) {
			failure.Code, failure.Reason, failure.Note = "CICADA-NOTES", refusal.Code, refusal.Note()
		}
		document.Error = failure
	}
	data, marshalErr := marshalAgentJSON(document)
	if marshalErr != nil {
		return marshalErr
	}
	_, writeErr := fmt.Fprintln(w, string(data))
	return writeErr
}
