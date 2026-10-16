package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"daylog/internal/journal"
	"daylog/internal/platform"
)

type statusResult struct {
	journal.Status
	Running bool   `json:"running"`
	State   string `json:"state"`
}

func recorderStopped(root string) (bool, error) {
	f, err := os.OpenFile(filepath.Join(root, "daemon.lock"), os.O_RDWR, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	err = platform.TryLock(f)
	if errors.Is(err, platform.ErrBusy) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, platform.Unlock(f)
}

func statusCommand(s *journal.Store, args []string) error {
	f := flags("status")
	asJSON := f.Bool("json", false, "output machine-readable status")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("用法：daylog status [--json]")
	}
	status, err := journal.SendControl(s.Root, journal.Control{Op: "status"})
	running := err == nil
	if err != nil {
		stopped, checkErr := recorderStopped(s.Root)
		if checkErr != nil {
			return checkErr
		}
		if !stopped {
			err := errors.New("后台仍持有运行锁，但控制连接失败；请稍后重试 daylog status，持续失败时运行 daylog doctor")
			if *asJSON {
				if outputErr := output(map[string]any{"running": nil, "state": "unreachable", "error": err.Error()}); outputErr != nil {
					return outputErr
				}
			}
			return err
		}
		pending, pendingErr := s.Pending(0)
		if pendingErr != nil {
			return pendingErr
		}
		status.Pending = len(pending)
		for _, batch := range pending {
			if batch.LastError != "" {
				status.FailedBatches++
			}
		}
		status.StorageBytes, err = s.RawBytes()
		if err != nil {
			return err
		}
	}
	// Restore recent success information even immediately after a daemon restart.
	now := time.Now()
	reports, reportErr := s.Reports(now.Add(-48*time.Hour), now.Add(time.Minute))
	if reportErr == nil {
		for _, r := range reports {
			if status.LastAnalysis == nil || r.CreatedAt.After(*status.LastAnalysis) {
				at := r.CreatedAt
				status.LastAnalysis = &at
			}
		}
	}
	if info, e := os.Stat(filepath.Join(s.Root, "reports", "report.html")); e == nil {
		at := info.ModTime()
		status.ReportGeneratedAt = &at
		status.ReportHasNewAnalysis = status.LastAnalysis != nil && status.LastAnalysis.After(at)
	}
	if *asJSON {
		state := "stopped"
		if running {
			state = "running"
			if status.Paused {
				state = "paused"
			}
		}
		return output(statusResult{Status: status, Running: running, State: state})
	}
	if !running {
		fmt.Fprintln(os.Stdout, "○ Daylog · 已停止")
		fmt.Fprintln(os.Stdout, "  最近整理  "+statusTime(status.LastAnalysis, now))
		fmt.Fprintf(os.Stdout, "  待整理    %d 批\n  截图占用  %s（磁盘）\n", status.Pending, statusBytes(status.StorageBytes))
		fmt.Fprintln(os.Stdout, "  再次开始  daylog start")
		return nil
	}
	writeStatus(os.Stdout, status, time.Now())
	return nil
}

func writeStatus(w io.Writer, s journal.Status, now time.Time) {
	state := "● Daylog · 后台运行"
	if s.Paused {
		state = "Ⅱ Daylog · 已暂停"
	} else if s.Error != "" {
		state = "! Daylog · 采集等待恢复"
	}
	fmt.Fprintln(w, state)
	if s.Paused {
		if s.PauseUntil != nil {
			fmt.Fprintf(w, "  自动恢复  %s\n", s.PauseUntil.Local().Format("2006-01-02 15:04:05 MST"))
		} else {
			fmt.Fprintln(w, "  继续记录  daylog start")
		}
	} else if s.Error != "" {
		fmt.Fprintln(w, "  当前状态  "+recordingReason(s.Error))
	}
	uptime := "未知"
	if !s.StartedAt.IsZero() {
		uptime = statusDuration(now.Sub(s.StartedAt))
	}
	fmt.Fprintf(w, "  已运行    %s · PID %d\n", uptime, s.PID)
	fmt.Fprintf(w, "  本次采集  %d 次采样 · %d 张留图\n", s.Captures, s.Saved)
	fmt.Fprintf(w, "  本次跳过  相似 %d 次 · 空闲 %d 次 · 隐私排除 %d 次\n", s.SkippedUnchanged, s.SkippedIdle, s.SkippedBlocked)
	fmt.Fprintf(w, "  最近截图  %s\n", statusTime(s.LastCapture, now))
	fmt.Fprintf(w, "  最近留图  %s\n", statusTime(s.LastSaved, now))
	if s.Pending == 0 {
		fmt.Fprintln(w, "  待整理    无待处理批次")
	} else {
		fmt.Fprintf(w, "  待整理    %d 批\n", s.Pending)
	}
	if s.AnalysisState != "" {
		state := map[string]string{"running": "正在整理", "idle": "等待新批次", "waiting": "等待新批次或重试时间", "retry_wait": "分析失败，等待重试", "power_paused": "按电源/屏幕策略暂缓", "budget_exhausted": "今日预算已达上限，次日自动恢复", "disabled": "自动分析已关闭"}[s.AnalysisState]
		if state == "" {
			state = "等待调度"
		}
		fmt.Fprintln(w, "  分析状态  "+state)
	}
	if s.FailedBatches > 0 {
		fmt.Fprintf(w, "  分析失败  %d 批，保留证据并自动退避重试\n", s.FailedBatches)
		fmt.Fprintln(w, "  手动重试  daylog analyze --retry（或 pending 查看批次）")
	}
	if s.AttentionBatches > 0 {
		fmt.Fprintf(w, "  需要关注  %d 批已连续重试失败超过 5 次；请查看 logs/llm 或 report\n", s.AttentionBatches)
	}
	if s.AnalysisError != "" {
		fmt.Fprintln(w, "  分析提示  "+analysisReason(s.AnalysisError))
	}
	if s.NextRetry != nil {
		fmt.Fprintln(w, "  下次重试  "+s.NextRetry.Local().Format("15:04:05"))
	}
	fmt.Fprintln(w, "  最近整理  "+statusTime(s.LastAnalysis, now))
	if s.ReportGeneratedAt != nil {
		fmt.Fprintln(w, "  报告快照  "+statusTime(s.ReportGeneratedAt, now))
		if s.ReportHasNewAnalysis {
			fmt.Fprintln(w, "            已有新分析结果，运行 daylog report 刷新")
		}
	}
	fmt.Fprintf(w, "  截图占用  %s（磁盘）\n", statusBytes(s.StorageBytes))
	fmt.Fprintf(w, "  状态更新  %s\n", statusTime(&s.UpdatedAt, now))
}

func statusTime(at *time.Time, now time.Time) string {
	if at == nil || at.IsZero() {
		return "暂无"
	}
	relative := "刚刚"
	if elapsed := now.Sub(*at); elapsed >= time.Second {
		relative = statusDuration(elapsed) + "前"
	} else if elapsed < -time.Second {
		relative = "请检查系统时间"
	}
	return at.Local().Format("2006-01-02 15:04:05 MST") + "（" + relative + "）"
}

func statusDuration(d time.Duration) string {
	if d < time.Second {
		return "不足 1 秒"
	}
	seconds := int64(d / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%d 秒", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%d 分 %d 秒", seconds/60, seconds%60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%d 小时 %d 分", seconds/3600, seconds%3600/60)
	}
	return fmt.Sprintf("%d 天 %d 小时", seconds/86400, seconds%86400/3600)
}

func statusBytes(size int64) string {
	if size < 0 {
		return "未知"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value, unit := float64(size), 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", size)
	}
	return fmt.Sprintf("%.2f %s", value, units[unit])
}
