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
	verbose := f.Bool("verbose", false, "show full status details")
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
		status.StorageBytes, err = s.RawBytes()
		if err != nil {
			return err
		}
	}
	// Restore recent success information even immediately after a daemon restart.
	now := time.Now()
	pending, pendingErr := s.Pending(0)
	if pendingErr != nil {
		return pendingErr
	}
	status.Pending = len(pending)
	status.FailedBatches = 0
	status.InterruptedBatches = 0
	status.AttentionBatches = 0
	for _, batch := range pending {
		switch journal.PreparationState(batch) {
		case "interrupted":
			status.InterruptedBatches++
		case "failed":
			status.FailedBatches++
			if batch.FailureRounds > 6 {
				status.AttentionBatches++
			}
		}
	}
	view, todayReports, reportErr := s.ExportDay(now)
	if reportErr == nil {
		status.TodayEstimatedSeconds = view.EstimatedSeconds
		status.TodayWorkTime = view.WorkTime
	}
	reports := todayReports
	if len(reports) == 0 {
		reports, reportErr = s.Reports(now.Add(-48*time.Hour), now.Add(time.Minute))
	}
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
	if !*verbose {
		writeCompactStatus(os.Stdout, status, now, running)
		return nil
	}
	writeStatus(os.Stdout, status, now, running)
	return nil
}

func writeStatus(w io.Writer, s journal.Status, now time.Time, running bool) {
	state := "● Daylog · 后台运行"
	if !running {
		state = "○ Daylog · 已停止"
	} else if s.Paused {
		state = "Ⅱ Daylog · 已暂停"
	} else if s.Error != "" {
		state = "! Daylog · 采集等待恢复"
	}
	fmt.Fprintln(w, state)
	writeWorkStatus(w, s.TodayWorkTime)
	if s.IdleAfterSeconds > 0 {
		fmt.Fprintf(w, "  无输入阈值  %s\n", workDuration(int64(s.IdleAfterSeconds)))
	}
	if s.Paused {
		if s.PauseUntil != nil {
			fmt.Fprintf(w, "  自动恢复  %s\n", s.PauseUntil.Local().Format("01-02 15:04"))
		} else {
			fmt.Fprintln(w, "  继续记录  daylog resume")
		}
	} else if s.Error != "" {
		fmt.Fprintln(w, "  当前状态  "+recordingReason(s.Error))
	}
	uptime := "未知"
	if !s.StartedAt.IsZero() {
		uptime = statusDuration(now.Sub(s.StartedAt))
	}
	if running {
		fmt.Fprintf(w, "  已运行    %s · PID %d\n", uptime, s.PID)
		fmt.Fprintf(w, "  本次截图  检查 %d 次 · 保存 %d 张\n", s.Captures, s.Saved)
		fmt.Fprintf(w, "  本次跳过  相似 %d 次 · 空闲 %d 次 · 隐私排除 %d 次\n", s.SkippedUnchanged, s.SkippedIdle, s.SkippedBlocked)
	}
	fmt.Fprintf(w, "  最近截图  %s\n", statusTime(s.LastCapture, now))
	fmt.Fprintf(w, "  最近保存  %s\n", statusTime(s.LastSaved, now))
	if s.Pending == 0 {
		fmt.Fprintln(w, "  待整理    无待处理记录")
	} else {
		fmt.Fprintf(w, "  待整理    %d 段\n", s.Pending)
	}
	if s.AnalysisState != "" {
		state := map[string]string{"running": "正在整理", "idle": "等待新记录", "waiting": "等待新记录或重试时间", "retry_wait": "整理暂未成功，等待重试", "interrupted": "已中断，后台运行后会继续整理", "power_paused": "按电源/屏幕策略暂缓", "budget_exhausted": "今日额度已达上限，次日自动恢复", "disabled": "自动整理已关闭"}[s.AnalysisState]
		if state == "" {
			state = "等待调度"
		}
		fmt.Fprintln(w, "  整理状态  "+state)
	}
	if s.FailedBatches > 0 {
		fmt.Fprintf(w, "  整理失败  %d 段，截图保留，后台会稍后重试\n", s.FailedBatches)
		fmt.Fprintln(w, "  手动重试  daylog analyze --retry（或 pending 查看记录）")
	}
	if s.AttentionBatches > 0 {
		fmt.Fprintf(w, "  需要关注  %d 段已连续重试失败超过 5 次；请用 daylog doctor 检查\n", s.AttentionBatches)
	}
	if s.AnalysisError != "" {
		fmt.Fprintln(w, "  整理提示  "+analysisReason(s.AnalysisError))
	}
	if s.NextRetry != nil {
		fmt.Fprintln(w, "  下次重试  "+s.NextRetry.Local().Format("15:04:05"))
	}
	fmt.Fprintln(w, "  最近整理  "+statusTime(s.LastAnalysis, now))
	if s.ReportGeneratedAt != nil {
		fmt.Fprintln(w, "  报告快照  "+statusTime(s.ReportGeneratedAt, now))
		if s.ReportHasNewAnalysis {
			fmt.Fprintln(w, "            已有新整理结果，运行 daylog report 刷新")
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
	format := "15:04"
	if at.Local().Format("2006-01-02") != now.Local().Format("2006-01-02") {
		format = "01-02 15:04"
	}
	return at.Local().Format(format) + "（" + relative + "）"
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
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value, unit := float64(size), 0
	for value >= 1000 && unit < len(units)-1 {
		value /= 1000
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", size)
	}
	return fmt.Sprintf("%.2f %s", value, units[unit])
}

func writeCompactStatus(w io.Writer, s journal.Status, now time.Time, running bool) {
	state := "正在记录"
	if !running {
		state = "已停止"
	} else if s.Paused {
		state = "已暂停"
	} else if s.Error != "" {
		state = recordingReason(s.Error)
	}
	fmt.Fprintln(w, "Daylog · "+state)
	writeWorkStatus(w, s.TodayWorkTime)
	if s.Paused && s.PauseUntil != nil {
		fmt.Fprintln(w, "  自动恢复  "+s.PauseUntil.Local().Format("15:04"))
	}
	fmt.Fprintln(w, "  最近截图  "+statusTime(s.LastCapture, now))
	fmt.Fprintf(w, "  待整理    %d 段", s.Pending)
	if s.InterruptedBatches > 0 {
		fmt.Fprintf(w, " · %d 段已中断，后台会继续处理", s.InterruptedBatches)
	}
	if s.FailedBatches > 0 {
		fmt.Fprintf(w, " · %d 段暂未成功", s.FailedBatches)
	}
	fmt.Fprintln(w)
	report := "尚未生成，运行 daylog report"
	if s.ReportGeneratedAt != nil {
		report = "已生成"
		if s.ReportHasNewAnalysis {
			report = "有新结果，运行 daylog report 刷新"
		}
	}
	fmt.Fprintln(w, "  报告      "+report)
	next := "daylog today 查看活动"
	if !running {
		next = "daylog start 开始记录"
	} else if s.Paused {
		next = "daylog resume 恢复记录"
	} else if s.FailedBatches > 0 {
		next = "daylog pending 查看原因"
	}
	fmt.Fprintln(w, "  下一步    "+next)
}

func workDuration(seconds int64) string {
	if seconds <= 0 {
		return "0 秒"
	}
	return statusDuration(time.Duration(seconds) * time.Second)
}

func workEstimateLabel(w journal.WorkTime) string {
	if w.EstimatedSeconds > 0 {
		return "约 " + workDuration(w.EstimatedSeconds)
	}
	if w.AwaySeconds > 0 && w.MissingSeconds == 0 {
		return "0 秒"
	}
	return "暂未估时"
}

func writeWorkStatus(w io.Writer, timing journal.WorkTime) {
	fmt.Fprintln(w, "  今天工作  "+workEstimateLabel(timing))
	if timing.Available {
		fmt.Fprintf(w, "  其中      已归类 %s · 未归类 %s\n", workDuration(timing.ClassifiedSeconds), workDuration(timing.UnclassifiedSeconds))
	}
}
