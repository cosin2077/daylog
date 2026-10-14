//go:build windows

package main

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
)

func nativeCommand(context.Context, string, []string) (bool, error)           { return false, nil }
func nativeAppResult(context.Context, string, []string) (bool, []byte, error) { return false, nil, nil }
func openScreenSettings(context.Context) error {
	return errors.New("请在已解锁的 Windows 用户桌面运行 Daylog")
}
func openReport(ctx context.Context, path string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	return windows.ShellExecute(0, nil, p, nil, nil, windows.SW_SHOWNORMAL)
}
