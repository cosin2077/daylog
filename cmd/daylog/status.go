package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"daylog/internal/journal"
)

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
	if err != nil {
		if *asJSON {
			return err
		}
		return errors.New("未连接到后台；尚未启动时运行 daylog start，若已启动可用 daylog doctor 检查")
	}
	if *asJSON {
		return output(status)
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
