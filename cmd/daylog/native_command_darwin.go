//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// macOS attributes terminal-spawned captures to the responsible parent app.
// LaunchServices gives native queries the same identity as the background app.
func nativeCommand(ctx context.Context, root string, args []string) (bool, error) {
	if os.Getenv("DAYLOG_NATIVE_CONTEXT") == "1" || len(args) == 0 {
		return false, nil
	}
	if args[0] != "doctor" && args[0] != "windows" && args[0] != "permission" {
		return false, nil
	}
	handled, data, err := nativeAppResult(ctx, root, args)
	if !handled || err != nil {
		return handled, err
	}
	_, err = os.Stdout.Write(data)
	return true, err
}

func nativeAppResult(ctx context.Context, root string, args []string) (bool, []byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return false, nil, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return false, nil, err
	}
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(app, ".app") {
		return false, nil, nil
	}
	temp, err := os.MkdirTemp(root, "native-query-")
	if err != nil {
		return true, nil, err
	}
	defer os.RemoveAll(temp)
	out := filepath.Join(temp, "result.json")
	stderr := filepath.Join(temp, "error.txt")
	for _, path := range []string{out, stderr} {
		if err = os.WriteFile(path, nil, 0600); err != nil {
			return true, nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	options := []string{"-n", "-g", "-W", "--env", "DAYLOG_NATIVE_CONTEXT=1", "--stdout", out, "--stderr", stderr, app, "--args", "--data", root}
	options = append(options, args...)
	if exec.CommandContext(ctx, "/usr/bin/open", options...).Run() != nil {
		return true, nil, errors.New("native App launch failed or timed out")
	}
	data, err := os.ReadFile(out)
	if err != nil || len(data) == 0 {
		return true, nil, errors.New("native App returned no result; check permissions and application identity")
	}
	return true, data, nil
}

func openScreenSettings(ctx context.Context) error {
	return exec.CommandContext(ctx, "/usr/bin/open", "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture").Run()
}

func openReport(ctx context.Context, path string) error {
	return exec.CommandContext(ctx, "/usr/bin/open", path).Run()
}
