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
	if len(os.Args) != 2 {
		panic("usage: go run ./scripts/report-demo OUTPUT.html")
	}
	anchor := time.Date(2026, 10, 13, 0, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	tasks := []struct{ title, summary, category string }{
		{"梳理轻量记录工具的交互流程", "围绕安装、开始记录与查看报告整理操作路径，在文档中补充了首次授权和默认行为的说明。", "工作"},
		{"实现按日期归档的截图存储", "编辑 Go 存储模块，检查旧截图引用的兼容读取，并为日期目录补充清理与空间统计。", "工作"},
		{"阅读 ScreenCaptureKit 文档", "查阅原生截图接口与后台优先级说明，记录低频截图场景下需要验证的行为。", "学习"},
		{"讨论日／周／月报告的展示", "讨论报告结构、记录空白的表达和时间线布局。页面中的讨论不能单独证明任务已完成。", "沟通"},
		{"验证报告的边界与交互", "运行跨日、跨周和去重统计测试，检查日报、周报与月报切换，以及搜索和打印样式。", "工作"},
		{"整理下一步事项", "在本地笔记中记录发布包、更新签名与后续验证事项。", "其他"},
	}
	var reports []journal.Report
	for day := -12; day <= 0; day++ {
		date := anchor.AddDate(0, 0, day)
		if date.Weekday() == time.Sunday || day == -8 {
			continue
		}
		for i := 0; i < 6; i++ {
			at := date.Add(time.Duration(9*60+i*65) * time.Minute)
			task := tasks[(i+day+18)%len(tasks)]
			if day == 0 {
				task = tasks[i]
			}
			id := journal.ID(at)
			reports = append(reports, journal.Report{BatchID: id, Source: "agent:claude", CreatedAt: at.Add(20 * time.Minute), Cards: []journal.Card{{Start: at, End: at.Add(time.Duration(12+(i*7)%26) * time.Minute), Title: task.title, Summary: task.summary, Category: task.category, Evidence: []string{id, journal.ID(at.Add(5 * time.Minute))}}}})
		}
	}
	pending := []journal.Batch{{Start: anchor.Add(17 * time.Hour), End: anchor.Add(17*time.Hour + 15*time.Minute)}}
	o := journal.BuildOverview(anchor, reports, pending, journal.Batch{})
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
