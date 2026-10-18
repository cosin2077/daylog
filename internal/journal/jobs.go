package journal

import (
	"daylog/internal/platform"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Never take analysis.lock while holding store.lock: analysis takes them in
// the opposite order. A busy analyzer owns all job directories until it exits.
func (s *Store) cleanupJobs() error {
	lock, err := os.OpenFile(filepath.Join(s.Root, "analysis.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = platform.TryLock(lock); errors.Is(err, platform.ErrBusy) {
		return nil
	} else if err != nil {
		return err
	}
	defer platform.Unlock(lock)
	return s.removeAbandonedJobs()
}

// Caller holds analysis.lock, so no cooperating process can be using a job.
func (s *Store) removeAbandonedJobs() error {
	root := filepath.Join(s.Root, "jobs")
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("整理临时目录不能是符号链接")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "analysis-") {
			if err = os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) jobBytes() (int64, error) {
	var size int64
	root := filepath.Join(s.Root, "jobs")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("整理临时目录不能含符号链接")
		}
		if entry.Type().IsRegular() {
			info, e := entry.Info()
			if e != nil {
				return e
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// A bounded 12 MiB workspace remains available even when protected raw frames fill
// the capture cap; otherwise analysis could never drain a full queue.
func (s *Store) saveJobImage(path string, data []byte) error {
	return s.locked(func() error {
		size, err := s.jobBytes()
		if err != nil {
			return err
		}
		if size+int64(len(data)) > 12<<20 {
			return ErrStorageFull
		}
		return os.WriteFile(path, data, 0600)
	})
}
