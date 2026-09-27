package project

import (
	"encoding/json"
	"fmt"
)

// Migrate1To2 returns a detached project value with the v2 semantic format
// marker. It never mutates the input. CanonicalJSON still chooses /1 when the
// returned project has no /2-only scene settings.
func Migrate1To2(source *Project) (*Project, error) {
	if source == nil || source.Format != FormatID || source.Version != 1 {
		return nil, fmt.Errorf("Migrate1To2 requires cicada.project/1")
	}
	if err := ValidateProject(source); err != nil {
		return nil, err
	}
	data, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var migrated Project
	if err := json.Unmarshal(data, &migrated); err != nil {
		return nil, err
	}
	migrated.Format, migrated.Version = FormatID2, 2
	return &migrated, nil
}
