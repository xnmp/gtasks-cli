// Package fsutil holds small filesystem helpers shared by adapters.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// WriteFileAtomic writes data to path via a temp file and rename, so readers
// never see a partial file. Missing parent directories are created
// owner-only. perm applies on Unix; on Windows access is governed by the
// directory's ACL.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(perm); err != nil {
			tmp.Close()
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// rename retries briefly on Windows, where replacing a file fails while
// another process (a concurrent gtasks, antivirus, the indexer) has it open.
func rename(from, to string) error {
	err := os.Rename(from, to)
	if runtime.GOOS != "windows" {
		return err
	}
	for i := 1; err != nil && i <= 5; i++ {
		time.Sleep(time.Duration(i*20) * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}
