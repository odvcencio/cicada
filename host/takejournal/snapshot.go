package takejournal

import (
	"os"
	"path/filepath"
	"sort"
)

// Snapshot reads complete acknowledged records without recovering or locking an
// active recorder. PCM is append-only, so Bytes can be copied from that prefix.
type Snapshot struct {
	Take         Take
	JournalBytes int64
	Bytes        int64
}

func Snapshots(root *os.Root, score string) ([]Snapshot, error) {
	dir := Namespace(score)
	d, err := root.Open(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var ss []Snapshot
	for _, name := range names {
		if filepath.Ext(name) != ".jsonl" {
			continue
		}
		file, err := root.Open(dir + "/" + name)
		if err != nil {
			return nil, err
		}
		t, n, err := decode(file)
		file.Close()
		if err != nil {
			return nil, err
		}
		if t == nil {
			continue
		}
		if name != t.ID+".jsonl" || !validID(t.ID) {
			return nil, os.ErrInvalid
		}
		ss = append(ss, Snapshot{clone(t), n, int64(t.Frames) * int64(t.Channels*4)})
	}
	return ss, nil
}
func SyncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return syncDirectory(f)
}
