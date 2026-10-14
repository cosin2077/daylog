package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"daylog/internal/journal"
	"daylog/internal/native"
)

func jsonOption(command string, args []string) (bool, error) {
	f := flags(command)
	asJSON := f.Bool("json", false, "output machine-readable JSON")
	if err := f.Parse(args); err != nil {
		return false, err
	}
	if f.NArg() != 0 {
		return false, errors.New("用法：daylog " + command + " [--json]")
	}
	return *asJSON, nil
}

func result(asJSON bool, value any, message string) error {
	if asJSON {
		return output(value)
	}
	fmt.Println(message)
	return nil
}

func readable(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}

func enabled(value bool) string {
	if value {
		return "开启"
	}
	return "关闭"
}

func writeConfig(w io.Writer, root string, c journal.Config) {
	fmt.Fprintln(w, "Daylog · 当前配置")
	fmt.Fprintln(w, "  配置文件  "+filepath.Join(root, "config.json"))
	mode := "仅记录，自动分析已关闭"
	if c.AI.Enabled {
		mode = "自定义 API · " + readable(c.AI.Model)
		if c.AI.FallbackAfterAgents {
			mode = "本机 Agent · " + strings.Join(c.Agent.Order, " → ") + " → API 回退"
		}
	} else if c.Agent.Enabled {
		mode = "本机 Agent · " + strings.Join(c.Agent.Order, " → ")
	}
	fmt.Fprintln(w, "  分析方式  "+mode)
	if c.AI.Enabled {
		fmt.Fprintln(w, "  API 地址  "+readable(c.AI.Endpoint))
		fmt.Fprintln(w, "  API 模型  "+readable(strings.Join(append([]string{c.AI.Model}, c.AI.FallbackModels...), " → ")))
	}
	key := "未在配置文件中设置；可使用环境变量或系统钥匙串"
	if c.AI.APIKey != "" {
		key = "已在配置文件中设置（内容隐藏）"
	}
	fmt.Fprintln(w, "  API 密钥  "+key)
	if c.AI.KeyEnv != "" {
		fmt.Fprintln(w, "  密钥变量  "+readable(c.AI.KeyEnv))
	}
	fmt.Fprintf(w, "  截图间隔  插电至少 %s · 电池至少 %s\n", statusDuration(time.Duration(c.MinSaveSeconds)*time.Second), statusDuration(time.Duration(max(c.MinSaveSeconds, c.Power.BatteryIntervalSeconds))*time.Second))
	fmt.Fprintf(w, "  分析批次  约每 %s · 目标 %d 张 / 最多 %d 张\n", statusDuration(time.Duration(c.BatchSeconds)*time.Second), min(c.TargetImages, c.MaxImages), c.MaxImages)
	fmt.Fprintf(w, "  每日预算  %d 次调用尝试 · %d 张提交图片\n", c.AI.MaxRequestsPerDay, c.MaxImagesPerDay)
	fmt.Fprintf(w, "  电池分析  %s · 电量不高于 %d%% 时暂停\n", enabled(c.Power.AnalyzeOnBattery), c.Power.PauseBelowPercent)
	target := "主屏幕"
	if c.WindowID != 0 {
		target = fmt.Sprintf("窗口 %d", c.WindowID)
	}
	fmt.Fprintln(w, "  截图范围  "+target)
	if c.IdleSeconds == 0 {
		fmt.Fprintln(w, "  空闲暂停  关闭")
	} else {
		fmt.Fprintf(w, "  空闲暂停  无输入达到 %s\n", statusDuration(time.Duration(c.IdleSeconds)*time.Second))
	}
	fmt.Fprintf(w, "  图像设置  高度 %d px · JPEG 质量 %d\n", c.Height, c.Quality)
	fmt.Fprintf(w, "  保存期限  已处理原图按文件年龄保留 %d 天 · 日记 %d 天\n", c.RawRetentionDays, c.ReportRetentionDays)
	fmt.Fprintf(w, "  存储上限  %s · 待处理最多 %d 批\n", statusBytes(c.MaxStorageBytes), c.MaxPendingBatches)
	blocked := "无"
	if len(c.BlockedApps) > 0 {
		blocked = readable(strings.Join(c.BlockedApps, "、"))
	}
	fmt.Fprintln(w, "  排除应用  "+blocked)
	updateSource := "尚未设置"
	if c.Update.Source != "" {
		updateSource = readable(c.Update.Source)
	}
	fmt.Fprintln(w, "  更新来源  "+updateSource)
	fmt.Fprintln(w, "  失败通知  "+enabled(c.Diagnostics.FailureNotifications)+" · 连续重试失败超过 5 次后通知一次")
	fmt.Fprintf(w, "  分析日志  %s · 保留 %d 天 · 上限 %s\n", enabled(c.Diagnostics.Enabled), c.Diagnostics.RetentionDays, statusBytes(c.Diagnostics.MaxBytes))
	fmt.Fprintln(w, "\n修改方法：daylog config -h；完整结构化配置：daylog config --json。")
}

func writeDoctor(w io.Writer, root string, env native.Environment, c journal.Config, valid bool) {
	fmt.Fprintf(w, "Daylog %s · 运行检查\n", version)
	permission := "未授权，请运行 daylog start 按系统提示开启"
	if env.Permission {
		permission = "已授权"
	}
	fmt.Fprintln(w, "  录屏权限  "+permission)
	power := "外接电源"
	if env.OnBattery {
		power = "电池供电"
	}
	if env.BatteryPercent >= 0 {
		power += fmt.Sprintf(" · %d%%", env.BatteryPercent)
	}
	fmt.Fprintln(w, "  电源状态  "+power)
	fmt.Fprintln(w, "  低电量模式  "+enabled(env.LowPower))
	thermal := "未知"
	if env.Thermal >= 0 && env.Thermal < 4 {
		thermal = []string{"正常", "偏热", "较高", "严重"}[env.Thermal]
	}
	fmt.Fprintln(w, "  系统热状态  "+thermal)
	screen := "可用"
	if env.Locked {
		screen = "已锁定"
	} else if env.ScreenOff {
		screen = "屏幕休眠"
	}
	fmt.Fprintln(w, "  屏幕状态  "+screen)
	if valid {
		fmt.Fprintln(w, "  配置检查  正常")
		policy := journal.PowerPolicy(c, env)
		analysis := "电源策略允许（不代表已启用分析或正在分析）"
		if !policy.CanAnalyze {
			analysis = "暂缓分析"
		}
		if policy.PauseReason != "" {
			analysis += " · " + recordingReason(policy.PauseReason)
		} else if env.OnBattery && !c.Power.AnalyzeOnBattery {
			analysis += " · 电池供电，接通电源后再处理"
		}
		fmt.Fprintln(w, "  分析策略  "+analysis)
	} else {
		fmt.Fprintln(w, "  配置检查  无效，请检查 config.json；暂不能判断分析策略")
	}
	fmt.Fprintln(w, "  数据目录  "+root)
	fmt.Fprintln(w, "\n这次检查没有截图；授权状态不代表实际捕获已经验证。")
}

func writeAgents(w io.Writer, agents []journal.AgentInfo) {
	fmt.Fprintln(w, "Daylog · 本机 Agent")
	for _, a := range agents {
		state := "未安装"
		if a.Installed {
			if a.Supported {
				state = "已安装 · 支持图片分析适配"
			} else {
				state = "已安装 · 暂无图片分析适配"
			}
		}
		fmt.Fprintf(w, "  %-12s %s\n", a.Name, state)
	}
	fmt.Fprintln(w, "\n检测到命令不代表已登录或模型支持图片；实际调用后才能确认。")
}

func writeWindows(w io.Writer, windows []native.Window) {
	if len(windows) == 0 {
		fmt.Fprintln(w, "暂无可捕获窗口。请打开目标窗口，并确认录屏权限。")
		return
	}
	fmt.Fprintf(w, "Daylog · 可捕获窗口（%d 个）\n", len(windows))
	for _, window := range windows {
		title := readable(window.Title)
		if title == "" {
			title = "无标题"
		}
		fmt.Fprintf(w, "  %d · %s\n    %s\n", window.ID, readable(window.App), title)
	}
	fmt.Fprintln(w, "\n可将窗口编号填入 config.json 的 window_id；应用重启后编号可能变化。")
}

func writePending(w io.Writer, batches []journal.Batch, total int) {
	if total == 0 {
		fmt.Fprintln(w, "没有待整理的批次。")
		return
	}
	fmt.Fprintf(w, "Daylog · 待整理 %d 批（显示 %d 批）\n", total, len(batches))
	for _, b := range batches {
		fmt.Fprintf(w, "\n  %s — %s\n", b.Start.Local().Format("2006-01-02 15:04"), b.End.Local().Format("2006-01-02 15:04"))
		fmt.Fprintf(w, "  %d 张关键帧 · 已尝试 %d 次\n  批次：%s\n", len(b.Frames), b.Attempts, b.ID)
		if b.LastError != "" {
			fmt.Fprintln(w, "  状态："+analysisReason(b.LastError))
			fmt.Fprintf(w, "  失败轮次：%d · 手动重试：daylog analyze --batch %s --retry\n", b.FailureRounds, b.ID)
		}
		if next := journal.RetryTime(b); next.After(time.Now()) {
			fmt.Fprintln(w, "  下次可重试："+next.Local().Format("15:04:05"))
		}
	}
	fmt.Fprintln(w, "\n后台会在策略允许时整理；也可用 daylog analyze 手动处理一批。")
}

func analysisReason(reason string) string {
	if strings.Contains(reason, "timeout") {
		return "模型调用超时；错误详情已记录到 logs/llm，失败批次会退避重试"
	}
	if strings.Contains(reason, "cli_exit") {
		return "Agent 命令未正常退出；可查看 logs/llm 中的脱敏 stderr"
	}
	if strings.Contains(reason, "validation_failed") || strings.Contains(reason, "invalid_json") {
		return "模型结果未通过校验；响应和错误原因已记录到 logs/llm"
	}
	if strings.Contains(reason, "daily analysis budget") {
		return "今日调用或图片预算已达上限，次日自动恢复"
	}
	if strings.Contains(reason, "cooling") {
		return "正在等待重试间隔"
	}
	switch reason {
	case "no_compatible_local_agent":
		return "未找到可用的本机 Agent，可用 daylog agents 检查"
	case "evidence_unavailable":
		return "截图证据暂不可用，请检查本地文件是否仍存在"
	case "network_or_timeout":
		return "模型请求未成功或已超时，截图仍保留"
	case "http_error":
		return "模型接口返回错误，请检查模型配置和账户状态"
	case "incomplete_response", "response_size":
		return "模型返回内容不完整或过大，未保存为日记"
	case "invalid_report", "invalid_json", "wrong_batch", "response_shape":
		return "模型结果未通过格式或证据校验，未保存为日记"
	}
	if strings.HasPrefix(reason, "agent_failed:") {
		return readable(strings.TrimPrefix(reason, "agent_failed:")) + " 分析未成功，请检查登录状态和模型的图片能力"
	}
	return "整理暂未完成，可用 daylog doctor 检查运行环境"
}
