// Package state persists small pieces of local CLI state.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xnmp/gtasks-cli/internal/fsutil"
)

// Numbers stores the task numbering shown by the last `ls`, one file per
// list so concurrent runs on different lists cannot clobber each other. It
// implements app.NumberStore. A zero Dir disables saving.
type Numbers struct {
	Dir string
}

// DefaultNumbers uses $GTASKS_CACHE_DIR or the per-user OS cache directory.
// It never falls back to a shared location: without one, numbering is off.
func DefaultNumbers() (Numbers, error) {
	if dir := os.Getenv("GTASKS_CACHE_DIR"); dir != "" {
		return Numbers{Dir: filepath.Join(dir, "numbers")}, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return Numbers{}, fmt.Errorf("no user cache directory: %w", err)
	}
	return Numbers{Dir: filepath.Join(base, "gtasks", "numbers")}, nil
}

func (n Numbers) path(listID string) string {
	sum := sha256.Sum256([]byte(listID))
	return filepath.Join(n.Dir, hex.EncodeToString(sum[:16])+".json")
}

// Load returns the saved numbering. A missing, unreadable or corrupt file
// counts as no numbering, which makes numeric references fail with a hint
// to re-run ls.
func (n Numbers) Load(listID string) ([]string, error) {
	if n.Dir == "" {
		return nil, nil
	}
	b, err := os.ReadFile(n.path(listID))
	if err != nil {
		return nil, nil
	}
	var ids []string
	if json.Unmarshal(b, &ids) != nil {
		return nil, nil
	}
	return ids, nil
}

func (n Numbers) Save(listID string, ids []string) error {
	if n.Dir == "" {
		return errors.New("no cache directory configured (set GTASKS_CACHE_DIR)")
	}
	if ids == nil {
		ids = []string{}
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(n.path(listID), b, 0o600)
}

// Forget removes the saved numbering for listID, if any.
func (n Numbers) Forget(listID string) error {
	if n.Dir == "" {
		return nil
	}
	if err := os.Remove(n.path(listID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
