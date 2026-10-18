package main

import (
	"context"
	"daylog/internal/platform"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"daylog/internal/journal"
	"daylog/internal/native"
	"daylog/internal/service"
)

func installedExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func migrateDefaultData(ctx context.Context, root, command string) error {
	if platform.IsWindows {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	old := filepath.Join(home, "Library", "Application Support", "Daylog")
	if _, err = os.Lstat(old); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	// Check conflicts before changing a running service.
	if _, err = os.Lstat(root); err == nil {
		_, err = journal.MigrateRoot(old, root)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	previous, probe := journal.SendControl(old, journal.Control{Op: "status"})
	// A read-only query during migration must never resume a paused recorder.
	wasRunning := probe == nil && !previous.Paused
	login := service.StartsAtLogin(home, old)
	if err = stopRuntime(ctx, old); err != nil {
		return err
	}
	if _, err = journal.MigrateRoot(old, root); err != nil {
		return err
	}
	if err = removeServiceFile(home, old); err != nil {
		return err
	}
	if command != "stop" && (login || wasRunning && command != "start") {
		exe, err := installedExecutable()
		if err != nil {
			return err
		}
		if _, err = service.Install(home, root, exe, login); err != nil {
			return err
		}
		if wasRunning && command != "start" {
			return service.Start(home, root)
		}
	}
	return nil
}

func removeServiceFile(home, root string) error {
	return service.Uninstall(home, root)
}

func stopRuntime(ctx context.Context, root string) error {
	_, _ = journal.SendControl(root, journal.Control{Op: "stop"})
	if err := service.Stop(root); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(root, "daemon.lock"), os.O_RDWR, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		if err = platform.TryLock(f); err == nil {
			_ = platform.Unlock(f)
			return nil
		}
		if !errors.Is(err, platform.ErrBusy) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("记录进程仍在收尾，请稍后再试")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type startActions struct {
	permission func(context.Context, string) error
	install    func() error
	launch     func() error
	status     func() (journal.Status, error)
	resume     func() error
	stop       func() error
	clearPause func() error
}

// The same entry point handles a first run, an already running daemon and a
// paused daemon. Permission denial must not register or launch a recorder.
func startWith(ctx context.Context, root string, a startActions) (journal.Status, error) {
	status, probeErr := a.status()
	needsLaunch := probeErr != nil || status.Error == "screen_permission_required"
	if !needsLaunch {
		if status.Paused {
			if err := a.resume(); err != nil {
				return status, err
			}
		} else {
			return status, nil
		}
	} else {
		if err := a.permission(ctx, root); err != nil {
			return journal.Status{}, err
		}
		if probeErr == nil {
			if err := a.stop(); err != nil {
				return journal.Status{}, err
			}
		}
		if err := a.install(); err != nil {
			return journal.Status{}, err
		}
		if a.clearPause != nil {
			if err := a.clearPause(); err != nil {
				return journal.Status{}, err
			}
		}
		if err := a.launch(); err != nil {
			return journal.Status{}, err
		}
	}
	return waitRuntimeReady(ctx, a.status, false)
}

// A launchctl success is only a request acknowledgement. Require the new
// daemon to publish its first status before reporting a successful restart.
func waitRuntimeReady(ctx context.Context, probe func() (journal.Status, error), allowPaused bool) (journal.Status, error) {
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		status, err := probe()
		if err == nil && (allowPaused || !status.Paused) && !status.UpdatedAt.IsZero() {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return journal.Status{}, ctx.Err()
		case <-deadline.C:
			return journal.Status{}, errors.New("后台尚未就绪，请运行 daylog start 重试")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func startRecording(ctx context.Context, s *journal.Store, login, asJSON bool) error {
	if _, err := journal.ReadConfig(s.Root); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	exe, err := installedExecutable()
	if err != nil {
		return err
	}
	status, err := startWith(ctx, s.Root, startActions{
		permission: ensurePermission,
		clearPause: func() error { return s.SavePause(false, nil) },
		install: func() error {
			_, err := service.Install(home, s.Root, exe, login || service.StartsAtLogin(home, s.Root))
			return err
		},
		launch: func() error { return service.Start(home, s.Root) },
		status: func() (journal.Status, error) { return journal.SendControl(s.Root, journal.Control{Op: "status"}) },
		resume: func() error { _, err := journal.SendControl(s.Root, journal.Control{Op: "resume"}); return err },
		stop:   func() error { return stopRuntime(ctx, s.Root) },
	})
	if err != nil {
		return err
	}
	if login && !service.StartsAtLogin(home, s.Root) {
		if _, err = service.Install(home, s.Root, exe, true); err != nil {
			return err
		}
	}
	if asJSON {
		return output(map[string]any{"state": "running", "data_dir": s.Root, "status": status})
	}
	fmt.Println("Daylog 已在后台运行。用 daylog today 查看今天，用 daylog stop 停止。")
	if status.Error != "" {
		fmt.Println("当前等待：" + recordingReason(status.Error))
	}
	return nil
}

func queryNativeEnvironment(ctx context.Context, root string) (native.Environment, error) {
	handled, data, err := nativeAppResult(ctx, root, []string{"doctor", "--json"})
	if !handled {
		return (native.Desktop{}).Environment(ctx)
	}
	if err != nil {
		return native.Environment{}, err
	}
	var result struct {
		Environment native.Environment `json:"environment"`
	}
	err = json.Unmarshal(data, &result)
	return result.Environment, err
}

func ensurePermission(ctx context.Context, root string) error {
	env, err := queryNativeEnvironment(ctx, root)
	if err != nil || env.Permission {
		return err
	}
	fmt.Fprintln(os.Stderr, "首次使用需要 macOS 录屏授权。请在系统设置中开启 Daylog；完成后会自动继续。")
	if handled, _, err := nativeAppResult(ctx, root, []string{"permission", "--json"}); err != nil {
		return err
	} else if !handled {
		native.RequestPermission()
	}
	if err = openScreenSettings(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		env, err = queryNativeEnvironment(ctx, root)
		if err == nil && env.Permission {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("尚未获得录屏授权；开启后运行 daylog start。若开关已开启，请移除旧 Daylog 条目并重新添加应用")
		case <-time.After(3 * time.Second):
		}
	}
}

func recordingReason(reason string) string {
	switch reason {
	case "screen_locked":
		return "解锁后自动继续"
	case "screen_asleep":
		return "屏幕唤醒后自动继续"
	case "low_battery":
		return "电量过低，充电后自动继续"
	case "thermal_pressure":
		return "系统温度恢复后自动继续"
	case "screen_permission_required":
		return "录屏权限尚未生效，请重新运行 daylog start"
	case "queue_full":
		return "待整理记录已达上限"
	default:
		return "采集暂不可用，可运行 daylog doctor 检查"
	}
}
