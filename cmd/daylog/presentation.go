package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
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
	mode := "仅记录，自动整理已关闭"
	if c.AI.Enabled {
		mode = "自定义 API · " + readable(c.AI.Model)
		if c.AI.FallbackAfterAgents {
			mode = "本机 Agent · " + strings.Join(c.Agent.Order, " → ") + " → API 回退"
		}
	} else if c.Agent.Enabled {
		mode = "本机 Agent · " + strings.Join(c.Agent.Order, " → ")
	}
	fmt.Fprintln(w, "\n常用")
	fmt.Fprintln(w, "  整理方式  "+mode)
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
	fmt.Fprintf(w, "  整理分段  约每 %s · 目标 %d 张 / 最多 %d 张\n", statusDuration(time.Duration(c.BatchSeconds)*time.Second), min(c.TargetImages, c.MaxImages), c.MaxImages)
	fmt.Fprintf(w, "  每日额度  %d 次整理尝试 · %d 张图片\n", c.AI.MaxRequestsPerDay, c.MaxImagesPerDay)
	fmt.Fprintf(w, "  电池整理  %s · 电量不高于 %d%% 时暂停\n", enabled(c.Power.AnalyzeOnBattery), c.Power.PauseBelowPercent)
	fmt.Fprintln(w, "\n高级")
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
	fmt.Fprintf(w, "  存储上限  %s · 待整理最多 %d 段\n", statusBytes(c.MaxStorageBytes), c.MaxPendingBatches)
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
	fmt.Fprintf(w, "  整理日志  %s · 保留 %d 天 · 上限 %s\n", enabled(c.Diagnostics.Enabled), c.Diagnostics.RetentionDays, statusBytes(c.Diagnostics.MaxBytes))
	fmt.Fprintln(w, "\n修改方法：daylog config -h；结构化配置（密钥隐藏）：daylog config --json。")
}

func writeDoctor(w io.Writer, root string, env native.Environment, c journal.Config, valid bool) {
	fmt.Fprintf(w, "Daylog %s · 运行检查\n", version)
	permission := "未授权，请运行 daylog start 按系统提示开启"
	if env.Permission {
		permission = "已授权"
	}
	if runtime.GOOS == "windows" {
		doctorRow(w, "捕获方式", "当前用户桌面")
	} else {
		doctorRow(w, "录屏权限", permission)
	}
	power := "外接电源"
	if env.OnBattery {
		power = "电池供电"
	}
	if env.BatteryPercent >= 0 {
		power += fmt.Sprintf(" · %d%%", env.BatteryPercent)
	}
	doctorRow(w, "电源状态", power)
	doctorRow(w, "低电量模式", enabled(env.LowPower))
	thermal := "未知"
	if env.Thermal >= 0 && env.Thermal < 4 {
		thermal = []string{"正常", "偏热", "较高", "严重"}[env.Thermal]
	}
	doctorRow(w, "系统热状态", thermal)
	screen := "可用"
	if env.Locked {
		screen = "已锁定"
	} else if env.ScreenOff {
		screen = "屏幕休眠"
	}
	doctorRow(w, "屏幕状态", screen)
	if valid {
		doctorRow(w, "配置检查", "正常")
		policy := journal.PowerPolicy(c, env)
		text := "允许整理"
		if !policy.CanAnalyze {
			text = "暂缓整理"
		}
		if policy.PauseReason != "" {
			text += " · " + recordingReason(policy.PauseReason)
		} else if env.OnBattery && !c.Power.AnalyzeOnBattery {
			text += " · 电池供电，接通电源后再处理"
		}
		doctorRow(w, "整理策略", text)
	} else {
		doctorRow(w, "配置检查", "无效，请检查 config.json；暂不能判断整理策略")
	}
	for _, model := range doctorModels(root, c) {
		text := model.State
		if model.LastAt != nil {
			text += " · " + model.LastAt.Local().Format("01-02 15:04")
		}
		doctorRow(w, model.Name, text)
	}
	doctorRow(w, "数据目录", root)
	fmt.Fprintln(w, "\n模型显示最近结果；本次未截图或联网测试。")
	fmt.Fprintln(w, "查看待整理原因：daylog pending；手动重试：daylog analyze --retry。")
}

func writeAgents(w io.Writer, agents []journal.AgentInfo) {
	fmt.Fprintln(w, "Daylog · 本机 Agent")
	for _, a := range agents {
		state := "未安装"
		if a.Installed {
			if a.Supported {
				state = "已安装 · 支持图片识别适配"
			} else {
				state = "已安装 · 暂无图片识别适配"
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
		fmt.Fprintln(w, "没有待整理的记录。")
		return
	}
	fmt.Fprintf(w, "Daylog · 待整理 %d 段", total)
	if len(batches) < total {
		fmt.Fprintf(w, "（显示 %d 段）", len(batches))
	}
	fmt.Fprintln(w)
	if len(batches) < total {
		fmt.Fprintf(w, "查看全部：daylog pending --limit %d\n", total)
	}
	for _, b := range batches {
		fmt.Fprintf(w, "\n  %s — %s · %d 张截图\n", b.Start.Local().Format("01-02 15:04"), b.End.Local().Format("15:04"), len(b.Frames))
		if b.LastError != "" {
			if journal.PreparationState(b) == "interrupted" {
				fmt.Fprintln(w, "  已中断；后台运行后会继续整理")
			} else {
				fmt.Fprintln(w, "  暂未成功："+journal.FailureReason(b.LastError))
			}
		} else {
			fmt.Fprintln(w, "  等待整理")
		}
		if next := journal.RetryTime(b); next.After(time.Now()) {
			fmt.Fprintln(w, "  下次尝试："+next.Local().Format("15:04:05"))
		}
	}
	fmt.Fprintln(w, "\n立即整理一段：daylog analyze；需要编号或调用详情时用 --json。")
}

func analysisReason(reason string) string {
	if strings.Contains(reason, "upstream_timeout") {
		return "模型调用超时，期间发生过上游重试；请检查 Agent 的网络和模型配置，或用 daylog config --agent-timeout 调整时限"
	}
	if strings.Contains(reason, "timeout") {
		return "模型调用超时；请检查 Agent 的网络和模型状态，详情见 logs/llm；可用 daylog config --agent-timeout 调整时限"
	}
	if strings.Contains(reason, "canceled") {
		return "整理已中断；不计失败，后台运行后会继续处理"
	}
	if strings.Contains(reason, "missing_result") {
		return "Agent 没有返回完整结果；请检查其非交互调用和模型状态，详情见 logs/llm"
	}
	if strings.Contains(reason, "agent_error") {
		return "Agent 返回失败结果；请检查登录、网络及模型状态，详情见 logs/llm"
	}
	if strings.Contains(reason, "output_limit") {
		return "Agent 输出超过大小限制，未保存为日记；详情见 logs/llm"
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
		return readable(strings.TrimPrefix(reason, "agent_failed:")) + " 整理未成功，请检查登录状态和模型的图片能力"
	}
	return "整理暂未完成，可用 daylog doctor 检查运行环境"
}

func observationDuration(seconds int64) string {
	if seconds <= 0 {
		return "观察已记录，时长未估"
	}
	if seconds < 60 {
		return fmt.Sprintf("观察估计约 %d 秒", seconds)
	}
	return fmt.Sprintf("观察估计约 %d 分钟", seconds/60)
}
