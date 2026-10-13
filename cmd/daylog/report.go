package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"daylog/internal/journal"
)

const reportHelp = `Daylog — 可视化报告

  daylog report                       打开报告，在页面中切换日／周／月
  daylog report --period week          默认展示本周
  daylog report --period month         默认展示本月
  daylog report --date 2026-10-13       查看指定日期所在的日／周／月
  daylog report --no-open              生成 HTML，输出路径而不打开浏览器
  daylog report --period week --json   输出结构化周报供 Agent 使用

  daylog report --from 2026-10-13T14:00:00+08:00 --to 2026-10-13T16:00:00+08:00 [--json]

报告复用已分析的记录，不触发新的模型请求。
HTML 保存在 ~/.daylog/reports/report.html，每次生成替换上次快照。
`

func reportCommand(ctx context.Context, s *journal.Store, args []string) error {
	f := flags("report")
	period := f.String("period", "day", "day, week or month")
	date := f.String("date", time.Now().Format("2006-01-02"), "local date within the requested period")
	noOpen := f.Bool("no-open", false, "write HTML without opening a browser")
	from := f.String("from", "", "range start, RFC3339 with timezone")
	to := f.String("to", "", "range end, RFC3339 with timezone")
	asJSON := f.Bool("json", false, "output structured period summary instead of HTML")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("用法：daylog report [--period day|week|month]")
	}
	if *from != "" || *to != "" {
		start, e1 := time.Parse(time.RFC3339, *from)
		end, e2 := time.Parse(time.RFC3339, *to)
		if e1 != nil || e2 != nil {
			return errors.New("--from 和 --to 须同时使用带时区的 RFC3339 时间")
		}
		conflict := false
		f.Visit(func(flag *flag.Flag) {
			if flag.Name == "date" || flag.Name == "period" {
				conflict = true
			}
		})
		if conflict {
			return errors.New("--from/--to 不能与 --date/--period 混用")
		}
		overview, err := s.RangeOverview(start.In(time.Local), end.In(time.Local))
		if err != nil {
			return err
		}
		if *asJSON {
			return output(overview.Views[0])
		}
		return presentOverview(ctx, s, overview, "range", *noOpen)
	}
	anchor, err := time.ParseInLocation("2006-01-02", *date, time.Local)
	if err != nil {
		return errors.New("日期格式应为 YYYY-MM-DD")
	}
	if _, _, err = journal.PeriodBounds(*period, anchor); err != nil {
		return err
	}
	overview, err := s.Overview(anchor)
	if err != nil {
		return err
	}
	if *asJSON {
		for _, v := range overview.Views {
			if v.Period == *period {
				return output(v)
			}
		}
	}
	return presentOverview(ctx, s, overview, *period, *noOpen)
}

func presentOverview(ctx context.Context, s *journal.Store, overview journal.Overview, period string, noOpen bool) error {
	path, err := s.WriteHTML(overview, period)
	if err != nil {
		return err
	}
	fmt.Println("报告已生成：" + path)
	if !noOpen {
		if err = openReport(ctx, path); err != nil {
			return fmt.Errorf("报告已保存，自动打开失败：%w", err)
		}
	}
	return nil
}
