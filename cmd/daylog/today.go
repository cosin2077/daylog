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
	explain := f.Bool("explain", false, "show timing method and limitations")
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
	view, reports, err := s.ExportDay(start)
	if err != nil {
		return err
	}
	count, total := view.PendingBatches, len(view.Activities)
	if *asJSON {
		return output(map[string]any{"date": *date, "reports": reports, "activities": view.Activities, "activity_count": total, "pending_batches": count, "draft_frames": view.DraftFrames, "work_time": view.WorkTime, "estimated_seconds": view.EstimatedSeconds, "unknown_seconds": view.UnknownSeconds})
	}
	weekdays := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	fmt.Printf("Daylog · %s %s · %d 项活动", start.Format("1月2日"), weekdays[start.Weekday()], total)
	if count > 0 {
		fmt.Printf(" · 还有 %d 段待整理", count)
	}
	fmt.Print("\n\n")
	if view.WorkTime.Available {
		fmt.Printf("电脑工作%s · 已归类 %s · 未归类 %s\n\n", workEstimateLabel(view.WorkTime), workDuration(view.WorkTime.ClassifiedSeconds), workDuration(view.WorkTime.UnclassifiedSeconds))
		if *explain {
			fmt.Printf("未工作 %s；采集不足 %s。未归类时间已包含在电脑工作估时中，不自动分配到具体任务。\n", workDuration(view.WorkTime.AwaySeconds), workDuration(view.WorkTime.MissingSeconds))
		}
	}
	if total > 0 {
		summary := view
		if summary.EstimatedSeconds > 0 {
			fmt.Printf("活动估计 %s", statusDuration(time.Duration(summary.EstimatedSeconds)*time.Second))
			if summary.InferredSeconds > 0 {
				fmt.Printf(" · 含区间推断 %s", statusDuration(time.Duration(summary.InferredSeconds)*time.Second))
			}
			fmt.Println()
		}
		if *explain && summary.UnknownSeconds > 0 {
			fmt.Printf("另有 %s 无法估时。\n", statusDuration(time.Duration(summary.UnknownSeconds)*time.Second))
		}
		if *explain && summary.UnknownSeconds > summary.EstimatedSeconds {
			fmt.Println("单帧、截图间隔过大、任务切换或识别不确定都可能无法估时；待整理记录另行列出。")
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
	shown := 0
	for _, card := range view.Activities {
		if !*full && shown >= *limit {
			continue
		}
		fmt.Printf("  %s  %s\n    %s", activityTime(card.Start, card.End), readable(card.Title), readable(card.Summary))
		if card.DurationMethod != "" {
			fmt.Printf("（%s）", observationDuration(card.EstimatedSeconds))
		}
		if card.InferredSeconds > 0 {
			fmt.Printf(" · 含区间推断 %s", statusDuration(time.Duration(card.InferredSeconds)*time.Second))
		}
		for i, part := range card.Parts {
			if i > 0 && part.Summary != card.Summary {
				fmt.Printf("\n    %s", readable(part.Summary))
			}
		}
		fmt.Println()
		shown++
	}
	if shown < total {
		fmt.Printf("\n已显示 %d / %d 条；查看完整日记：daylog today --date %s --full；也可用 --limit 调整。\n", shown, total, *date)
	}
	if total == 0 {
		writeEmptyToday(s, start)
	}
	if *explain {
		fmt.Println("\n时长来自截图观察估计；未估时表示记录不足，不能作为精确工时。")
	}
	return nil
}

func activityTime(start, end time.Time) string {
	start, end = start.Local(), end.Local()
	from, to := start.Format("15:04"), end.Format("15:04")
	if start.Format("2006-01-02") != end.Format("2006-01-02") {
		to = "次日 " + to
	}
	if from == to {
		return from
	}
	return from + "–" + to
}

func writeEmptyToday(s *journal.Store, date time.Time) {
	fmt.Println("这个日期还没有已整理的日记。")
	if status, err := journal.SendControl(s.Root, journal.Control{Op: "status"}); err == nil {
		if status.Paused {
			fmt.Println("后台已暂停；运行 daylog resume 继续。")
		} else if status.Error != "" {
			fmt.Println(recordingReason(status.Error))
		} else {
			fmt.Println("后台正在记录，待这段记录整理完成后再查看。")
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
