package pgconf

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	pwfs "github.com/smarsh/poolwarden/internal/fs"
)

// Change is one staged change (02 §5). Secret ops (pcpuser/pooluser passwords)
// are never staged (I27).
type Change struct {
	File     string          `json:"file"`
	Op       string          `json:"op"` // set|unset|hba_add|hba_remove|backend_set|backend_remove
	Param    string          `json:"param,omitempty"`
	Old      string          `json:"old,omitempty"`
	New      string          `json:"new,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Operator string          `json:"operator"`
	At       time.Time       `json:"at"`
}

// Staged is the persisted change set at <StateDir>/staged.json.
type Staged struct {
	Changes []Change          `json:"changes"`
	SHAs    map[string]string `json:"shas,omitempty"`
}

// StagedPath returns <dir>/staged.json.
func StagedPath(dir string) string { return dir + "/staged.json" }

// LoadStaged reads staged.json; a missing file is an empty Staged.
func LoadStaged(fsys pwfs.FS, dir string) (*Staged, error) {
	b, err := fsys.ReadFile(StagedPath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || isNotExist(err) {
			return &Staged{}, nil
		}
		return nil, err
	}
	var s Staged
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func isNotExist(err error) bool { return os.IsNotExist(err) }

// SaveStaged writes staged.json atomically with 0600.
func (s *Staged) Save(fsys pwfs.FS, dir string) error {
	if err := fsys.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsys.WriteFileAtomic(StagedPath(dir), append(b, '\n'), true)
}
