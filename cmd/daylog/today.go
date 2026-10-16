package main

import (
	"errors"
	"fmt"
	"time"

	"daylog/internal/journal"
)

func todayCommand(s *journal.Store, args []string) error {
	f := flags("today")
	date := f.String("date", time.Now().Format("2006-01-02"), "local calendar day")
	asJSON := f.Bool("json", false, "complete JSON export")
	full := f.Bool("full", false, "show every activity in human output")
	limit := f.Int("limit", 20, "maximum visible activities in human output")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *limit < 1 || *limit > 2000 {
		return errors.New("--limit 必须为 1–2000；查看完整日记可用 --full")
	}
	start, err := time.ParseInLocation("2006-01-02", *date, time.Local)
	if err != nil {
		return errors.New("日期格式应为 YYYY-MM-DD")
	}
	end := start.AddDate(0, 0, 1)
	reports, err := s.Reports(start, end)
	if err != nil {
		return err
	}
	pending, err := s.Pending(0)
	if err != nil {
		return err
	}
	count, total := 0, 0
	for _, batch := range pending {
		if batch.Start.Before(end) && batch.End.After(start) {
			count++
		}
	}
	for _, report := range reports {
		total += len(report.Cards)
	}
	if *asJSON {
		return output(map[string]any{"date": *date, "reports": reports, "pending_batches": count})
	}
	fmt.Printf("Daylog · %s\n\n", *date)
	if total > 0 {
		for _, summary := range journal.BuildOverview(start, reports, pending, journal.Batch{}).Views {
			if summary.Period != "day" {
				continue
			}
			fmt.Printf("%d 个活动片段", total)
			if summary.EstimatedSeconds > 0 {
				fmt.Printf(" · 观察估计 %s", statusDuration(time.Duration(summary.EstimatedSeconds)*time.Second))
			}
			if summary.UnknownSeconds > 0 {
				fmt.Printf(" · 已分析时段内 %s 无法估时", statusDuration(time.Duration(summary.UnknownSeconds)*time.Second))
			}
			fmt.Println()
			if summary.UnknownSeconds > summary.EstimatedSeconds {
				fmt.Println("大部分已分析时段无法估时：单帧、采样间隔过大、任务切换或低置信识别都可能留下缺口。未分析批次另行列出；这不代表没有活动。")
			}
			tasks := 0
			for _, task := range summary.Tasks {
				if task.EstimatedSeconds <= 0 {
					continue
				}
				if tasks == 0 {
					fmt.Println("主要任务（观察估计）：")
				}
				fmt.Printf("  %s · %s\n", readable(task.Title), statusDuration(time.Duration(task.EstimatedSeconds)*time.Second))
				tasks++
				if tasks == 5 {
					break
				}
			}
			fmt.Println()
		}
	}
	shown := 0
	for _, report := range reports {
		for _, card := range report.Cards {
			if !*full && shown >= *limit {
				continue
			}
			fmt.Printf("  %s–%s  %s\n    %s", card.Start.Local().Format("15:04"), card.End.Local().Format("15:04"), readable(card.Title), readable(card.Summary))
			if card.DurationMethod != "" {
				fmt.Printf("（%s）", observationDuration(card.EstimatedSeconds))
			}
			fmt.Println()
			shown++
		}
	}
	if shown < total {
		fmt.Printf("\n已显示 %d / %d 条；查看完整日记：daylog today --date %s --full；也可用 --limit 调整。\n", shown, total, *date)
	}
	if total == 0 {
		writeEmptyToday(s, start)
	}
	if count > 0 {
		fmt.Printf("\n还有 %d 段活动尚未整理，日记暂不完整。\n", count)
	}
	return nil
}

func writeEmptyToday(s *journal.Store, date time.Time) {
	fmt.Println("这个日期还没有已整理的日记。")
	if status, err := journal.SendControl(s.Root, journal.Control{Op: "status"}); err == nil {
		if status.Paused {
			fmt.Println("后台已暂停；运行 daylog start 继续。")
		} else if status.Error != "" {
			fmt.Println(recordingReason(status.Error))
		} else {
			fmt.Println("后台正在记录，待本批整理完成后再查看。")
		}
	} else if stopped, err := recorderStopped(s.Root); err == nil && stopped {
		fmt.Println("后台已停止；运行 daylog start 开始记录。")
	} else {
		fmt.Println("暂无法确认后台状态，请用 daylog status 检查。")
	}
	// Offer an explicit date; never silently substitute yesterday for today.
	if reports, err := s.Reports(date.AddDate(0, 0, -7), date); err == nil && len(reports) > 0 {
		for i := len(reports) - 1; i >= 0; i-- {
			if len(reports[i].Cards) == 0 {
				continue
			}
			at := reports[i].Cards[len(reports[i].Cards)-1].Start
			if !at.IsZero() {
				fmt.Printf("最近七天有日记可看：daylog today --date %s\n", at.Local().Format("2006-01-02"))
				break
			}
		}
	}
}
