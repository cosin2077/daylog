//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	asJSON, parseErr := jsonOption(args[0], args[1:])
	if parseErr != nil {
		return true, parseErr
	}
	handled, data, err := nativeAppResult(ctx, root, args)
	if !handled || err != nil {
		if handled && err != nil && args[0] == "doctor" {
			if asJSON {
				if writeErr := writeDoctorFailure(os.Stdout, root, err); writeErr != nil {
					return true, writeErr
				}
			}
		}
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
	data, err := nativeAppQuery(ctx, root, app, args, func(ctx context.Context, options []string) error {
		return exec.CommandContext(ctx, "/usr/bin/open", options...).Run()
	})
	return true, data, err
}

func nativeAppQuery(ctx context.Context, root, app string, args []string, launch func(context.Context, []string) error) ([]byte, error) {
	temp, err := os.MkdirTemp(root, "native-query-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)
	out := filepath.Join(temp, "result.json")
	stderr := filepath.Join(temp, "error.txt")
	for _, path := range []string{out, stderr} {
		if err = os.WriteFile(path, nil, 0600); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	options := []string{"-n", "-g", "-W", "--env", "DAYLOG_NATIVE_CONTEXT=1", "--stdout", out, "--stderr", stderr, app, "--args", "--data", root}
	options = append(options, args...)
	if err = launch(ctx, options); err != nil {
		code, reason := "native_app_launch_failed", "macOS 未能启动 Daylog 原生查询"
		if ctx.Err() == context.DeadlineExceeded {
			code, reason = "native_app_timeout", "Daylog 原生查询超时（最长等待 20 秒）"
		}
		return nil, nativeQueryFailure(code, reason, app, err)
	}
	data, err := os.ReadFile(out)
	if err != nil || len(data) == 0 {
		return nil, nativeQueryFailure("native_app_no_result", "Daylog 应用未返回诊断结果", app, err)
	}
	return data, nil
}

func nativeQueryFailure(code, reason, app string, cause error) error {
	return &commandError{cause: cause, code: code, message: fmt.Sprintf("%s。\n应用路径：%s\n下一步：在访达中打开此应用以查看系统提示，再重试 daylog doctor。\n可用 codesign --verify --deep --strict 检查上述应用的签名。查询失败本身不能说明录屏权限已丢失。", reason, readable(app))}
}

func writeDoctorFailure(w io.Writer, root string, err error) error {
	code := "native_app_unavailable"
	if detail, ok := err.(*commandError); ok && detail.code != "" {
		code = detail.code
	}
	return json.NewEncoder(w).Encode(map[string]any{"ok": false, "version": version, "data_dir": root, "error": map[string]string{"code": code, "message": err.Error()}, "note": "原生环境查询未完成，录屏权限及电源状态尚未确认"})
}

func openScreenSettings(ctx context.Context) error {
	return exec.CommandContext(ctx, "/usr/bin/open", "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture").Run()
}

func openReport(ctx context.Context, path string) error {
	return exec.CommandContext(ctx, "/usr/bin/open", path).Run()
}
