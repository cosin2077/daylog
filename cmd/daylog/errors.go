package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

type commandError struct {
	cause   error
	message string
	code    string
}

func (e *commandError) Error() string { return e.message }
func (e *commandError) Unwrap() error { return e.cause }

// Keep library errors available to callers while presenting a stable Chinese
// explanation. Provider responses stay in the private analysis log.
func explainError(err error) error {
	if err == nil {
		return nil
	}
	var explained *commandError
	if errors.As(err, &explained) {
		return err
	}
	message := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		message = "操作超时；请检查网络和 Agent 状态后重试"
	case errors.Is(err, context.Canceled):
		message = "操作已取消，已有记录仍保留"
	case strings.HasPrefix(message, "flag provided but not defined: "):
		message = "未知选项：" + readable(strings.TrimPrefix(message, "flag provided but not defined: ")) + "；使用 daylog help --advanced 查看用法"
	case strings.HasPrefix(message, "flag needs an argument: "):
		message = "选项缺少参数：" + readable(strings.TrimPrefix(message, "flag needs an argument: "))
	case strings.HasPrefix(message, "invalid value "):
		message = "选项的值无效；请检查数字、布尔值或使用 daylog help --advanced 查看用法"
	case message == "--data needs a directory":
		message = "--data 后需要指定数据目录"
	default:
		for _, r := range message {
			if unicode.Is(unicode.Han, r) {
				return err
			}
		}
		var path *os.PathError
		if errors.As(err, &path) {
			message = "无法访问文件：" + readable(path.Path) + "；请检查路径和文件权限"
		} else {
			message = "操作未完成；请检查命令参数与运行环境，使用 daylog help --advanced 查看用法"
		}
	}
	return &commandError{cause: err, message: message, code: "command_failed"}
}

var commands = []string{"start", "stop", "status", "today", "report", "config", "doctor", "permission", "windows", "agents", "pending", "analyze", "pause", "resume", "init", "run", "commit", "auth", "service", "update", "version", "prompt", "notion"}

func unknownCommand(name string) error {
	best, score := "", 3
	for _, command := range commands {
		if distance := editDistance(name, command); distance < score {
			best, score = command, distance
		}
	}
	if best != "" {
		return fmt.Errorf("未知命令 %q；是否想输入 daylog %s？用 daylog -h 查看帮助", readable(name), best)
	}
	return fmt.Errorf("未知命令 %q；用 daylog -h 查看帮助", readable(name))
}

func editDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	row := make([]int, len(y)+1)
	for j := range row {
		row[j] = j
	}
	for i, r := range x {
		previous := row[0]
		row[0] = i + 1
		for j, s := range y {
			old, cost := row[j+1], 0
			if r != s {
				cost = 1
			}
			row[j+1] = min(row[j+1]+1, row[j]+1, previous+cost)
			previous = old
		}
	}
	return row[len(y)]
}
