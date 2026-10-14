//go:build windows

package platform

import (
	"context"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsPrivateStoreLockAndPipe(t *testing.T) {
	root := t.TempDir()
	if e := PrivateDir(root); e != nil {
		t.Fatal(e)
	}
	sd, e := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	sid, _ := userSID()
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount != 2 {
		t.Fatal("unexpected access control list")
	}
	allowed := map[string]bool{sid: false, "S-1-5-18": false}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, i, &ace); e != nil {
			t.Fatal(e)
		}
		identity := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if _, ok := allowed[identity]; !ok || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatal("unexpected principal or rule")
		}
		allowed[identity] = true
	}
	for _, present := range allowed {
		if !present {
			t.Fatal("missing private principal")
		}
	}
	a, e := os.Create(filepath.Join(root, "lock"))
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := os.OpenFile(a.Name(), os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if e = TryLock(a); e != nil {
		t.Fatal(e)
	}
	if e = TryLock(b); e != ErrBusy {
		t.Fatalf("second writer was not excluded: %v", e)
	}
	Unlock(a)
	if e = TryLock(b); e != nil {
		t.Fatal(e)
	}
	Unlock(b)
	l, cleanup, e := ListenControl(root)
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	done := make(chan error, 1)
	go func() {
		c, e := l.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		_, e = io.Copy(c, c)
		done <- e
	}()
	c, e := DialControl(root)
	if e != nil {
		t.Fatal(e)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	_, e = c.Write([]byte("hello"))
	if e != nil {
		t.Fatal(e)
	}
	buf := make([]byte, 5)
	if _, e = io.ReadFull(c, buf); e != nil || string(buf) != "hello" {
		t.Fatalf("pipe roundtrip: %q %v", buf, e)
	}
	c.Close()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}

func TestJobHelper(t *testing.T) {
	mode := os.Getenv("DAYLOG_JOB_TEST")
	if mode == "" {
		return
	}
	if mode == "parent" {
		exe, _ := os.Executable()
		child := exec.Command(exe, "-test.run=^TestJobHelper$")
		child.Env = append(os.Environ(), "DAYLOG_JOB_TEST=child")
		if e := child.Start(); e != nil {
			os.Exit(4)
		}
		os.WriteFile(os.Getenv("DAYLOG_JOB_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0600)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestJobCancellationKillsDescendants(t *testing.T) {
	exe, _ := os.Executable()
	path := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestJobHelper$")
	cmd.Env = append(os.Environ(), "DAYLOG_JOB_TEST=parent", "DAYLOG_JOB_PID="+path)
	done := make(chan error, 1)
	go func() { done <- RunProcess(cmd) }()
	deadline := time.Now().Add(10 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		data, e := os.ReadFile(path)
		if e == nil {
			pid, _ = strconv.Atoi(string(data))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		cancel()
		t.Fatal("child was not started")
	}
	h, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	defer windows.CloseHandle(h)
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancellation unexpectedly succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("job did not stop")
	}
	result, e := windows.WaitForSingleObject(h, 5000)
	if e != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatal(fmt.Sprintf("descendant survived: %v %v", result, e))
	}
}
