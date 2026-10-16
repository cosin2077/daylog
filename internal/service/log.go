package service

import (
	"errors"
	"os"
	"path/filepath"
)

// launchd otherwise creates its output file using the service's default umask.
func openDaemonLog(root string) (*os.File, error) {
	path := filepath.Join(root, "daemon.log")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("后台日志必须为普通文件，不能是符号链接")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
