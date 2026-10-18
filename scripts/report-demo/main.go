// Generate an explicitly labelled HTML preview without reading user data.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"daylog/internal/journal"
)

func main() {
	if len(os.Args) != 2 && !(len(os.Args) == 3 && (os.Args[2] == "--failure" || os.Args[2] == "--unrecognized" || os.Args[2] == "--interpolated")) {
		panic("usage: go run ./scripts/report-demo OUTPUT.html [--failure|--unrecognized|--interpolated]")
	}
	now := time.Now()
	anchor := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.Local)
	tasks := []struct{ title, summary, category string }{
		{"梳理轻量记录工具的交互流程", "围绕安装、开始记录与查看报告整理操作路径，在文档中补充了首次授权和默认行为的说明。", "写代码"},
		{"实现按日期归档的截图存储", "编辑 Go 存储模块，检查旧截图引用的兼容读取，并为日期目录补充清理与空间统计。", "写代码"},
		{"阅读 ScreenCaptureKit 文档", "查阅原生截图接口与后台优先级说明，记录低频截图场景下需要验证的行为。", "查资料"},
		{"讨论日／周／月报告的展示", "讨论报告结构、记录空白的表达和时间线布局。页面中的讨论不能单独证明任务已完成。", "聊天沟通"},
		{"验证报告的跨日边界、长任务标题与手机端交互，保留完整上下文供下次复盘", "运行跨日、跨周和去重统计测试，检查日报、周报与月报切换，以及搜索和打印样式。", "写代码"},
		{"整理下一步事项", "在本地笔记中记录发布包、更新签名与后续验证事项。", "写东西"},
	}
	var reports []journal.Report
	for day := -45; day <= 0; day++ {
		date := anchor.AddDate(0, 0, day)
		if date.Weekday() == time.Sunday || day == -8 {
			continue
		}
		for i := 0; i < 6; i++ {
			at := date.Add(time.Duration(9*60+i*65) * time.Minute)
			task := tasks[(i+day+48)%len(tasks)]
			if day == 0 {
				task = tasks[i]
			}
			id := journal.ID(at)
			end := at.Add(time.Duration(12+(i*7)%26) * time.Minute)
			reports = append(reports, journal.Report{SchemaVersion: 2, AppSource: "window_server", Start: at, End: end.Add(7 * time.Minute), UnknownSpans: []journal.TimeSpan{{Start: end, End: end.Add(7 * time.Minute)}}, BatchID: id, Source: "agent:claude", CreatedAt: at.Add(20 * time.Minute), Cards: []journal.Card{{ActivityID: "act_" + id, TaskID: fmt.Sprintf("task_%d", (i+day+48)%len(tasks)), Apps: []string{"Demo Editor"}, DurationMethod: "bounded_observation_v1", Confidence: "medium", Spans: []journal.TimeSpan{{Start: at, End: end}}, Start: at, End: end, Title: task.title, Summary: task.summary, Category: task.category, Evidence: []string{id, journal.ID(at.Add(5 * time.Minute))}}}})
		}
	}
	// Explicit synthetic interruptions: code -> chat -> code -> feed -> notes -> code.
	at := anchor.Add(16 * time.Hour)
	r := journal.Report{SchemaVersion: 2, AppSource: "window_server", BatchID: "demo-fragments", Start: at, End: at.Add(20 * time.Minute), Source: "demo"}
	titles := []string{"修复登录问题", "回复群聊消息", "刷推荐内容", "记下临时事项", "处理日程提醒", "查看临时下载"}
	categories := []string{"写代码", "聊天沟通", "看内容", "写东西", "处理杂事", "处理杂事"}
	apps := []string{"com.microsoft.VSCode", "ru.keepcoder.Telegram", "com.google.Chrome", "com.apple.Notes", "com.apple.iCal", "com.apple.finder"}
	sequence := []int{0, 0, 0, 0, 0, 1, 1, 0, 0, 2, 3, 3, 0, 0, 4, 0, 5, 5, 0, 0}
	for task := 0; task < len(titles); task++ {
		c := journal.Card{TaskID: fmt.Sprintf("demo-task-%d", task), ActivityID: fmt.Sprintf("demo-act-%d", task), Title: titles[task], Summary: "演示活动，用来展示短暂插入与返回。", Category: categories[task], Apps: []string{apps[task]}, Confidence: "medium", DurationMethod: "bounded_observation_v1"}
		for i, label := range sequence {
			if label != task {
				continue
			}
			point := at.Add(time.Duration(i) * time.Minute)
			if c.Start.IsZero() {
				c.Start = point
			}
			c.End = point.Add(time.Nanosecond)
			c.Evidence = append(c.Evidence, fmt.Sprintf("demo-%d", i))
			if i > 0 && sequence[i-1] == task {
				c.Spans = append(c.Spans, journal.TimeSpan{Start: point.Add(-time.Minute), End: point})
				c.EstimatedSeconds += 60
			}
		}
		r.Cards = append(r.Cards, c)
	}
	for i, label := range sequence {
		point := at.Add(time.Duration(i) * time.Minute)
		r.TaskObservations = append(r.TaskObservations, journal.TaskObservation{At: point, State: "sampled", TaskID: fmt.Sprintf("demo-task-%d", label), App: apps[label], MaxGapSeconds: 120})
		if i > 0 && label != sequence[i-1] {
			r.UnknownSpans = append(r.UnknownSpans, journal.TimeSpan{Start: point.Add(-time.Minute), End: point})
		}
	}
	r.UnknownSpans = append(r.UnknownSpans, journal.TimeSpan{Start: at.Add(19 * time.Minute), End: r.End})
	for i := 0; i < 7; i++ {
		point := anchor.Add(17*time.Hour + time.Duration(i)*time.Minute)
		r.Cards = append(r.Cards, journal.Card{Title: fmt.Sprintf("单帧活动 %d", i+1), Summary: "合成单帧记录，不分配时长。", Category: "处理杂事", DurationMethod: "bounded_observation_v1", Start: point, End: point})
	}
	reports = append(reports, r)
	previous := at.AddDate(0, 0, -1)
	previousEnd := previous.Add(time.Minute)
	reports = append(reports, journal.Report{SchemaVersion: 2, AppSource: "window_server", BatchID: journal.ID(previous), Start: previous, End: previousEnd,
		Cards:            []journal.Card{{TaskID: "demo-prior", Title: "整理临时事项", Summary: "合成的前一天连续记录，用于展示切换对比。", Category: "处理杂事", Confidence: "medium", Start: previous, End: previousEnd, Spans: []journal.TimeSpan{{Start: previous, End: previousEnd}}}},
		TaskObservations: []journal.TaskObservation{{At: previous, State: "sampled", App: "com.apple.Notes", TaskID: "demo-prior", MaxGapSeconds: 120}, {At: previousEnd, State: "sampled", App: "com.apple.Notes", TaskID: "demo-prior", MaxGapSeconds: 120}},
	})
	pending := []journal.Batch{{Start: anchor.Add(17 * time.Hour), End: anchor.Add(17*time.Hour + 15*time.Minute)}}
	if len(os.Args) == 3 && os.Args[2] == "--failure" {
		attempt := anchor.Add(18 * time.Hour)
		pending = append(pending, journal.Batch{ID: journal.ID(attempt), Start: attempt, End: attempt.Add(15 * time.Minute), LastError: "claude:timeout", Attempts: 2, FailureRounds: 2, LastAttempt: &attempt})
	}
	o := journal.BuildOverview(anchor, reports, pending, journal.Batch{})
	if len(os.Args) == 3 && os.Args[2] != "--failure" {
		from := anchor.Add(9 * time.Hour)
		until := from.Add(3 * time.Minute)
		r := journal.Report{SchemaVersion: 2, AppSource: "synthetic_review", BatchID: journal.ID(from), Start: from, End: until, Cards: []journal.Card{}, UnknownSpans: []journal.TimeSpan{{Start: from, End: until}}, UnknownSeconds: 180}
		if os.Args[2] == "--interpolated" {
			r.UnknownSpans = nil
			r.UnknownSeconds = 0
			r.Cards = []journal.Card{{TaskID: "review-task", Title: "同一任务的两张关键帧", Summary: "中间的新截图未送模型辨认；这里保留有界插值。", Category: "写代码", Confidence: "medium", Start: from, End: until, EstimatedSeconds: 180, Spans: []journal.TimeSpan{{Start: from, End: until, Interpolated: true}}}}
			r.TaskObservations = []journal.TaskObservation{{At: from, App: "editor", TaskID: "review-task", State: "sampled", MaxGapSeconds: 180}, {At: until, App: "editor", TaskID: "review-task", State: "sampled", MaxGapSeconds: 180}}
		}
		o = journal.BuildOverview(anchor, []journal.Report{r}, nil, journal.Batch{})
	}
	o.Sample = true
	data, err := journal.RenderHTML(o, "day")
	if err != nil {
		panic(err)
	}
	if err = os.MkdirAll(filepath.Dir(os.Args[1]), 0700); err != nil {
		panic(err)
	}
	if err = os.WriteFile(os.Args[1], data, 0600); err != nil {
		panic(err)
	}
	fmt.Println(os.Args[1])
}
