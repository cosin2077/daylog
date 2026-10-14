//go:build windows

package platform

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const IsWindows = true

var ErrBusy = errors.New("file locked")

func TryLock(f *os.File) error {
	e := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	return e
}
func Unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
func BackgroundPriority() {
	_ = windows.SetPriorityClass(windows.CurrentProcess(), windows.BELOW_NORMAL_PRIORITY_CLASS)
}
func userSID() (string, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return "", e
	}
	return u.User.Sid.String(), nil
}
func PrivateDir(path string) error {
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return errors.New("data path must not be a link")
	}
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	attr, e := windows.GetFileAttributes(p)
	if e != nil {
		return e
	}
	if attr&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("data path must not be a reparse point")
	}
	sid, e := userSID()
	if e != nil {
		return e
	}
	want, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + sid + ")")
	if e != nil {
		return e
	}
	dacl, _, e := want.DACL()
	if e != nil {
		return e
	}
	current, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	if strings.ReplaceAll(current.String(), "D:PAI", "D:P") == want.String() {
		return nil
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
func pipePath(root string) (string, string, error) {
	sid, e := userSID()
	if e != nil {
		return "", "", e
	}
	p, e := filepath.Abs(root)
	if e != nil {
		return "", "", e
	}
	h := sha256.Sum256([]byte(sid + "\n" + strings.ToLower(filepath.Clean(p))))
	return fmt.Sprintf(`\\.\pipe\daylog-%x`, h[:16]), "D:P(A;;GA;;;" + sid + ")", nil
}
func ListenControl(root string) (net.Listener, func(), error) {
	p, sd, e := pipePath(root)
	if e != nil {
		return nil, nil, e
	}
	l, e := winio.ListenPipe(p, &winio.PipeConfig{SecurityDescriptor: sd, InputBufferSize: 4096, OutputBufferSize: 16384})
	if e != nil {
		return nil, nil, e
	}
	return l, func() { l.Close() }, nil
}
func DialControl(root string) (net.Conn, error) {
	p, _, e := pipePath(root)
	if e != nil {
		return nil, e
	}
	timeout := time.Second
	return winio.DialPipe(p, &timeout)
}

// Suspend before assigning the job so a CLI cannot spawn a child that escapes
// cancellation. Closing the job kills remaining descendants on every exit path.
func RunProcess(cmd *exec.Cmd) error {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		return e
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_SUSPENDED | windows.BELOW_NORMAL_PRIORITY_CLASS}
	var canceled atomic.Bool
	cmd.Cancel = func() error { canceled.Store(true); return windows.TerminateJobObject(job, 1) }
	if e = cmd.Start(); e != nil {
		return e
	}
	fail := func(err error) error { cmd.Process.Kill(); cmd.Wait(); return err }
	process, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if e != nil {
		return fail(e)
	}
	e = windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if e != nil {
		return fail(e)
	}
	if canceled.Load() {
		windows.TerminateJobObject(job, 1)
		return cmd.Wait()
	}
	snapshot, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if e != nil {
		return fail(e)
	}
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := false
	for e = windows.Thread32First(snapshot, &entry); e == nil; e = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(cmd.Process.Pid) {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			e = openErr
			break
		}
		_, e = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		resumed = e == nil
		break
	}
	windows.CloseHandle(snapshot)
	if !resumed {
		if e == nil {
			e = errors.New("agent initial thread unavailable")
		}
		return fail(e)
	}
	return cmd.Wait()
}
