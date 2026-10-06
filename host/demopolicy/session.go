package demopolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"unicode/utf8"
)

const MaxSourceBytes = 128 << 10
const MaxHistoryEntries = 64

var (
	ErrRevision = errors.New("CICADA-REVISION: session changed; refresh before editing")
	ErrSource   = errors.New("CICADA-LIMIT: source must be UTF-8 and at most 128 KiB")
	ErrHistory  = errors.New("CICADA-HISTORY: no change available")
)

type Snapshot struct {
	Source   []byte
	Revision string
	CanUndo  bool
	CanRedo  bool
}

// Session is owned by one GoSX browser instance. It never reads or writes the
// host filesystem, uploads a score, or stores it in a shared account. A reload
// constructs a fresh session. Serialize access on the browser event loop.
// Calling this on a server and sharing it between visitors violates its contract.
type Session struct {
	source   []byte
	seed     []byte
	undo     [][]byte
	redo     [][]byte
	version  uint64
	validate func([]byte) error
}

// NewSession requires the actual app's compiler/validator. Validation failure
// cannot partially commit a source or erase history.
func NewSession(seed []byte, validate func([]byte) error) (*Session, error) {
	if validate == nil {
		return nil, errors.New("CICADA-DEMO: session requires a score validator")
	}
	if err := validateSource(seed, validate); err != nil {
		return nil, err
	}
	return &Session{source: bytes.Clone(seed), seed: bytes.Clone(seed), version: 1, validate: validate}, nil
}

func validateSource(source []byte, validate func([]byte) error) error {
	if len(source) > MaxSourceBytes || !utf8.Valid(source) {
		return ErrSource
	}
	// Protect the caller's input even if a validator modifies its argument.
	return validate(bytes.Clone(source))
}

func (s *Session) Snapshot() Snapshot {
	hash := sha256.Sum256(s.source)
	return Snapshot{
		Source:   bytes.Clone(s.source),
		Revision: strconv.FormatUint(s.version, 10) + "-" + hex.EncodeToString(hash[:]),
		CanUndo:  len(s.undo) != 0, CanRedo: len(s.redo) != 0,
	}
}

func (s *Session) checkRevision(revision string) error {
	if revision == "" || revision != s.Snapshot().Revision {
		return ErrRevision
	}
	return nil
}

func appendHistory(history [][]byte, source []byte) [][]byte {
	if len(history) == MaxHistoryEntries {
		copy(history, history[1:])
		history = history[:len(history)-1]
	}
	return append(history, bytes.Clone(source))
}

func (s *Session) Apply(revision string, source []byte) error {
	if err := s.checkRevision(revision); err != nil {
		return err
	}
	if err := validateSource(source, s.validate); err != nil {
		return err
	}
	if bytes.Equal(s.source, source) {
		return nil
	}
	s.undo = appendHistory(s.undo, s.source)
	s.redo = nil
	s.source = bytes.Clone(source)
	s.version++
	return nil
}

func (s *Session) Undo(revision string) error {
	if err := s.checkRevision(revision); err != nil {
		return err
	}
	if len(s.undo) == 0 {
		return ErrHistory
	}
	s.redo = appendHistory(s.redo, s.source)
	s.source = s.undo[len(s.undo)-1]
	s.undo[len(s.undo)-1] = nil
	s.undo = s.undo[:len(s.undo)-1]
	s.version++
	return nil
}

func (s *Session) Redo(revision string) error {
	if err := s.checkRevision(revision); err != nil {
		return err
	}
	if len(s.redo) == 0 {
		return ErrHistory
	}
	s.undo = appendHistory(s.undo, s.source)
	s.source = s.redo[len(s.redo)-1]
	s.redo[len(s.redo)-1] = nil
	s.redo = s.redo[:len(s.redo)-1]
	s.version++
	return nil
}

// Reset discards edits and both histories, so Undo cannot recover discarded
// work. The initial score is immutable and copied at construction.
func (s *Session) Reset(revision string) error {
	if err := s.checkRevision(revision); err != nil {
		return err
	}
	s.source = bytes.Clone(s.seed)
	s.undo, s.redo = nil, nil
	s.version++
	return nil
}
