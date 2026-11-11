//go:build unix

package render

import "os"

// unlinkOpenFile removes the name of a file that stays open, so the file's
// space returns when the descriptor closes, however the process ends. A name
// that cannot be removed leaves the file as it was; Close removes it then.
func unlinkOpenFile(file *os.File) {
	_ = os.Remove(file.Name())
}

// unlinksOpenFiles reports that NewMasterCache leaves no name behind.
const unlinksOpenFiles = true
