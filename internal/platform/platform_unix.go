//go:build !windows

package platform

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const IsWindows = false

var ErrBusy = syscall.EWOULDBLOCK

func TryLock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
func Unlock(f *os.File) error  { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
func BackgroundPriority()      { _ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10) }
func PrivateDir(path string) error {
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.IsDir() || i.Mode().Perm()&0077 != 0 {
		return errors.New("directory is not private")
	}
	return nil
}
func RunProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd.Run()
}
func ListenControl(root string) (net.Listener, func(), error) {
	p := filepath.Join(root, "run.sock")
	if e := os.Remove(p); e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, nil, e
	}
	l, e := net.Listen("unix", p)
	if e != nil {
		return nil, nil, e
	}
	cleanup := func() { l.Close(); os.Remove(p) }
	if e = os.Chmod(p, 0600); e != nil {
		cleanup()
		return nil, nil, e
	}
	return l, cleanup, nil
}
func DialControl(root string) (net.Conn, error) {
	return net.DialTimeout("unix", filepath.Join(root, "run.sock"), time.Second)
}
