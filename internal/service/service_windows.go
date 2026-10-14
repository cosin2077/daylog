//go:build windows

package service

import (
	"daylog/internal/platform"
	"encoding/json"
	"errors"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

type configuration struct {
	Executable string `json:"executable"`
	Root       string `json:"root"`
	Login      bool   `json:"login"`
}

func Path(home, root string) string { return filepath.Join(root, "service.json") }
func Preview(executable, root string, login bool) []byte {
	b, _ := json.MarshalIndent(configuration{executable, root, login}, "", "  ")
	return append(b, '\n')
}
func StartsAtLogin(home, root string) bool {
	k, e := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if e != nil {
		return false
	}
	defer k.Close()
	v, _, e := k.GetStringValue(Label(root))
	return e == nil && v != ""
}
func Install(home, root, executable string, login bool) (string, error) {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(root) {
		return "", errors.New("absolute installation paths required")
	}
	if e := platform.PrivateDir(root); e != nil {
		return "", e
	}
	p := Path(home, root)
	temp, e := os.CreateTemp(root, ".service-*")
	if e != nil {
		return "", e
	}
	defer os.Remove(temp.Name())
	_, e = temp.Write(Preview(executable, root, login))
	ce := temp.Close()
	if e != nil {
		return "", e
	}
	if ce != nil {
		return "", ce
	}
	if e = os.Rename(temp.Name(), p); e != nil {
		return "", e
	}
	k, _, e := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if e != nil {
		return "", e
	}
	defer k.Close()
	if login {
		args := []string{executable, "--data", root, "start"}
		for i := range args {
			args[i] = windows.EscapeArg(args[i])
		}
		e = k.SetStringValue(Label(root), strings.Join(args, " "))
	} else {
		e = k.DeleteValue(Label(root))
		if errors.Is(e, registry.ErrNotExist) {
			e = nil
		}
	}
	return p, e
}
func IsLoaded(root string) bool {
	c, e := platform.DialControl(root)
	if e != nil {
		return false
	}
	c.Close()
	return true
}
func Start(home, root string) error {
	if IsLoaded(root) {
		return nil
	}
	raw, e := os.ReadFile(Path(home, root))
	if e != nil {
		return e
	}
	var c configuration
	if e = json.Unmarshal(raw, &c); e != nil {
		return e
	}
	if c.Root != root || !filepath.IsAbs(c.Executable) {
		return errors.New("invalid background configuration")
	}
	log, e := os.OpenFile(filepath.Join(root, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd := exec.Command(c.Executable, "--data", root, "run")
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW}
	if e = cmd.Start(); e != nil {
		return e
	}
	return cmd.Process.Release()
}
func Stop(root string) error {
	c, e := platform.DialControl(root)
	if e != nil {
		return nil
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if e = json.NewEncoder(c).Encode(map[string]string{"op": "stop"}); e != nil {
		return e
	}
	var response json.RawMessage
	return json.NewDecoder(c).Decode(&response)
}
func Uninstall(home, root string) error {
	k, e := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if e == nil {
		e = k.DeleteValue(Label(root))
		k.Close()
	}
	if e != nil && !errors.Is(e, registry.ErrNotExist) {
		return e
	}
	e = os.Remove(Path(home, root))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	return e
}
