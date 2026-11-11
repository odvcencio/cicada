//go:build !unix

package render

import "os"

// unlinkOpenFile does nothing where an open file cannot lose its name, as on
// Windows: the file keeps its name until Close removes it.
func unlinkOpenFile(*os.File) {}

// unlinksOpenFiles reports that NewMasterCache leaves no name behind.
const unlinksOpenFiles = false
