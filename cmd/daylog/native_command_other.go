//go:build !darwin

package main

import (
	"context"
	"errors"
)

func openReport(context.Context, string) error {
	return errors.New("请用浏览器打开已生成的 HTML 文件")
}

func nativeCommand(context.Context, string, []string) (bool, error)           { return false, nil }
func nativeAppResult(context.Context, string, []string) (bool, []byte, error) { return false, nil, nil }
func openScreenSettings(context.Context) error                                { return errors.New("Daylog 录屏需要 macOS 14+") }
