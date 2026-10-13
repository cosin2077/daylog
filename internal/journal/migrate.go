package journal

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var ErrMigrationBusy = errors.New("旧数据仍在使用中")

// Move the complete store together: frame references are relative, and retry
// budgets, drafts and reports must survive a directory migration unchanged.
func MigrateRoot(oldRoot, newRoot string) (bool, error) {
	info, err := os.Lstat(oldRoot)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return false, errors.New("旧数据目录不是私有目录，未迁移")
	}
	if _, err = os.Lstat(newRoot); err == nil {
		return false, errors.New("新旧数据目录同时存在，未覆盖；请先检查 ~/.daylog 和旧 Daylog 数据目录")
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var locks []*os.File
	defer func() {
		for _, f := range locks {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		}
	}()
	for _, name := range []string{"daemon.lock", "analysis.lock", "store.lock"} {
		f, err := os.OpenFile(filepath.Join(oldRoot, name), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return false, err
		}
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			return false, ErrMigrationBusy
		}
		locks = append(locks, f)
	}
	if err = os.Rename(oldRoot, newRoot); err != nil {
		return false, err
	}
	// A stopped daemon can leave a socket behind after a crash.
	_ = os.Remove(filepath.Join(newRoot, "run.sock"))
	return true, nil
}
