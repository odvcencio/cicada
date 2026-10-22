package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/internal/appdirs"
	"m31labs.dev/cicada/internal/filetarget"
)

var errStudioSwapUnavailable = errors.New("atomic score exchange is unavailable on this filesystem; edit the score in your text editor")

// studioWriteIfRevision exchanges an already validated score with a staged
// file, then checks the exact version displaced by that exchange. A pathname
// replacement by another editor between validation and commit is detected.
// On conflict the displaced version is exchanged back into place. Displaced
// files stay linked: an editor can still write through an open file handle.
func studioWriteIfRevision(path string, updated []byte, mode os.FileMode, expected string, beforeSwap func()) (bool, string, error) {
	return studioWriteTakeIfRevision(path, updated, mode, expected, beforeSwap, nil)
}

func studioWriteTakeIfRevision(path string, updated []byte, mode os.FileMode, expected string, beforeSwap func(), checkpoint func(takejournal.Stage)) (bool, string, error) {
	return studioPublish(path, updated, mode, expected, beforeSwap, checkpoint, nil)
}

// studioPublishSeam runs immediately before the veto, after the replacement is
// staged. Tests use it to change the project at the publication boundary.
var studioPublishSeam func()

// studioPublish writes updated through a staged file and an atomic exchange.
// veto, when set, runs after the staged file is written and synced and
// immediately before the exchange; a non-nil error aborts: the staged file is
// removed and the score is untouched.
func studioPublish(path string, updated []byte, mode os.FileMode, expected string, beforeSwap func(), checkpoint func(takejournal.Stage), veto func() error) (bool, string, error) {
	target, err := filetarget.Resolve(path, filetarget.Existing)
	if err != nil {
		return false, "", err
	}
	path = target.Physical
	dir, err := studioRecoveryDir(path)
	if err != nil {
		return false, "", err
	}
	stage, err := os.CreateTemp(dir, studioRecoveryPattern(path))
	if err != nil {
		return false, "", err
	}
	stagePath := stage.Name()
	removeStage := true
	defer func() {
		if removeStage {
			_ = os.Remove(stagePath)
		}
	}()
	if err := stage.Chmod(mode); err != nil {
		_ = stage.Close()
		return false, "", err
	}
	if _, err := stage.Write(updated); err != nil {
		_ = stage.Close()
		return false, "", err
	}
	if err := stage.Sync(); err != nil {
		_ = stage.Close()
		return false, "", err
	}
	if err := stage.Close(); err != nil {
		return false, "", err
	}
	if checkpoint != nil {
		checkpoint(takejournal.SourceStaged)
	}
	if beforeSwap != nil {
		beforeSwap()
	}
	if studioPublishSeam != nil {
		studioPublishSeam()
	}
	if veto != nil {
		if err := veto(); err != nil {
			return false, "", err
		}
	}
	displacedPath, err := studioSwap(path, stagePath)
	if err != nil {
		if errors.Is(err, errStudioSwapUnavailable) {
			return false, "", errStudioSwapUnavailable
		}
		if displacedPath != "" {
			removeStage = false
			return false, displacedPath, fmt.Errorf("score replacement failed; displaced score preserved at %s: %w", displacedPath, err)
		}
		return false, "", err
	}
	removeStage = false
	if checkpoint != nil {
		if err := takejournal.SyncDirectory(filepath.Dir(path)); err != nil {
			return false, displacedPath, err
		}
		checkpoint(takejournal.SourceSwapped)
	}
	displaced, err := os.ReadFile(displacedPath)
	if err == nil && studioRevision(displaced) == expected {
		if err := studioKeepRecovery(displacedPath, expected); err != nil {
			return false, displacedPath, err
		}
		if err := takejournal.SyncDirectory(filepath.Dir(path)); err != nil {
			return false, displacedPath, err
		}
		return true, displacedPath, nil
	}
	// Never unlink either displaced inode, even if its bytes match now. An
	// external writer can finish after this function returns.
	interveningPath, rollbackErr := studioSwap(path, displacedPath)
	if rollbackErr != nil {
		return false, displacedPath, fmt.Errorf("score changed during commit; saved files remain at %s; rollback failed: %w", displacedPath, rollbackErr)
	}
	if err := studioKeepRecovery(interveningPath, studioRevision(updated)); err != nil {
		return false, interveningPath, err
	}
	if err := takejournal.SyncDirectory(filepath.Dir(path)); err != nil {
		return false, interveningPath, err
	}
	return false, interveningPath, fmt.Errorf("score changed during commit; the external score was restored; another version remains at %s", interveningPath)
}

// Each score has its own recovery namespace. Recovery files are never removed
// by Studio. A revision receipt detects writes through displaced open handles,
// including writes that finish after Studio restarts.
func studioRecoveryPattern(path string) string {
	return ".cicada-studio-" + studioRevision([]byte(path))[:16] + "-*"
}

func studioKeepRecovery(path, revision string) error {
	file, err := os.OpenFile(path+".revision", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, err = file.WriteString(revision)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return fmt.Errorf("cannot record recovery revision; keep %s and compare it with the score: %w", path, err)
	}
	return takejournal.SyncDirectory(filepath.Dir(path))
}

func studioRecoveryConflict(path string) error {
	target, err := filetarget.Resolve(path, filetarget.Existing)
	if err != nil {
		return err
	}
	path = target.Physical
	// ReadDir avoids treating metacharacters in the score's directory as glob syntax.
	dir, err := studioRecoveryDir(path)
	if err != nil {
		return err
	}
	for _, dir := range []string{dir, filepath.Dir(path)} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		prefix := strings.TrimSuffix(studioRecoveryPattern(path), "*")
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), prefix) || strings.HasSuffix(entry.Name(), ".revision") {
				continue
			}
			recovery := filepath.Join(dir, entry.Name())
			revision, revisionErr := os.ReadFile(recovery + ".revision")
			source, sourceErr := os.ReadFile(recovery)
			if revisionErr != nil || sourceErr != nil || string(revision) != studioRevision(source) {
				return fmt.Errorf("save refused: an external write or an incomplete save remains at %s; close other editors, compare and merge that file with the score, then move the recovery file and its .revision file out of this directory", recovery)
			}
		}
	}
	return nil
}

// Read a displaced version only when both its receipt and bytes agree. An
// unresolved write through any displaced handle makes automatic restore unsafe.
func studioRecoverySource(path, revision string) ([]byte, bool, error) {
	if err := studioRecoveryConflict(path); err != nil {
		return nil, false, nil
	}
	target, err := filetarget.Resolve(path, filetarget.Existing)
	if err != nil {
		return nil, false, err
	}
	dir, err := studioRecoveryDir(target.Physical)
	if err != nil {
		return nil, false, err
	}
	prefix := strings.TrimSuffix(studioRecoveryPattern(target.Physical), "*")
	for _, directory := range []string{dir, filepath.Dir(target.Physical)} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, false, err
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), prefix) || strings.HasSuffix(entry.Name(), ".revision") {
				continue
			}
			name := filepath.Join(directory, entry.Name())
			receipt, err := os.ReadFile(name + ".revision")
			if err != nil || string(receipt) != revision {
				continue
			}
			data, err := os.ReadFile(name)
			if err != nil {
				return nil, false, err
			}
			if studioRevision(data) == revision {
				return data, true, nil
			}
		}
	}
	return nil, false, nil
}

// Keep linked displaced inodes in app data: late writes through an external
// editor's open handle remain detectable without littering the project.
func studioRecoveryDir(path string) (string, error) {
	target, err := filetarget.Write(path)
	if err != nil {
		return "", err
	}
	path = target.Physical
	state, err := appdirs.StateDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(state, "revisions", studioRevision([]byte(path))[:16])
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}
