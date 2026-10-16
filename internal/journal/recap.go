package journal

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ReviewSummary struct {
	Headline string `json:"headline"`
	Advice   string `json:"advice"`
	PeakHour *int   `json:"peak_hour,omitempty"`
}

type PeriodComparison struct {
	Label            string    `json:"label"`
	Start            time.Time `json:"start"`
	End              time.Time `json:"end"`
	CurrentEnd       time.Time `json:"current_end"`
	Partial          bool      `json:"partial"`
	Available        bool      `json:"available"`
	EstimatedSeconds int64     `json:"estimated_seconds"`
	LongestSeconds   int64     `json:"longest_seconds"`
	TaskSwitches     int       `json:"task_switches"`
	ComparablePairs  int       `json:"comparable_pairs"`
	Activities       int       `json:"activities"`
	RecordedDays     int       `json:"recorded_days"`
}

func buildComparison(kind string, anchor, now time.Time, reports []Report) *PeriodComparison {
	start, end, _ := PeriodBounds(kind, anchor)
	var previous time.Time
	switch kind {
	case "day":
		previous = start.AddDate(0, 0, -1)
	case "week":
		previous = start.AddDate(0, 0, -7)
	case "month":
		previous = start.AddDate(0, -1, 0)
	default:
		return nil
	}
	previousStart, previousEnd, _ := PeriodBounds(kind, previous)
	label := map[string]string{"day": "前一天", "week": "上周", "month": "上月"}[kind]
	currentEnd := end
	partial := now.Before(end)
	if partial {
		currentEnd = now.In(start.Location())
		if !currentEnd.After(start) {
			return &PeriodComparison{Label: label, Start: previousStart, End: previousStart, CurrentEnd: start, Partial: true}
		}
		day := start
		days := 0
		for day.AddDate(0, 0, 1).Before(currentEnd) || day.AddDate(0, 0, 1).Equal(currentEnd) {
			day = day.AddDate(0, 0, 1)
			days++
		}
		candidate := previousStart.AddDate(0, 0, days)
		candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), currentEnd.Hour(), currentEnd.Minute(), currentEnd.Second(), currentEnd.Nanosecond(), candidate.Location())
		if candidate.Before(previousEnd) {
			previousEnd = candidate
		}
		label += "同进度"
	}
	prior := buildPeriodRange(kind, previous, previousStart, previousEnd, reports, nil, Batch{})
	return &PeriodComparison{Label: label, Start: previousStart, End: previousEnd, CurrentEnd: currentEnd, Partial: partial, Available: len(prior.Activities) > 0, EstimatedSeconds: prior.EstimatedSeconds, LongestSeconds: prior.Continuity.LongestSeconds, TaskSwitches: prior.Continuity.TaskSwitches, ComparablePairs: prior.Continuity.ComparablePairs, Activities: len(prior.Activities), RecordedDays: prior.ActiveDays}
}

func buildReview(p PeriodReport) ReviewSummary {
	if len(p.Activities) == 0 {
		return ReviewSummary{Headline: "这个时段还没有已整理的活动。", Advice: "先开始记录，积累一些活动后再来回顾。"}
	}
	ordered := append([]CategorySummary(nil), p.Categories...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].EstimatedSeconds > ordered[j].EstimatedSeconds })
	var names []string
	for _, c := range ordered {
		if c.EstimatedSeconds > 0 && (len(names) == 0 || c.EstimatedSeconds*100 >= p.EstimatedSeconds*15) {
			names = append(names, c.Name)
		}
		if len(names) == 2 {
			break
		}
	}
	r := ReviewSummary{}
	if len(names) > 0 {
		r.Headline = "主要在" + strings.Join(names, "和") + "。"
	} else {
		r.Headline = fmt.Sprintf("记录了 %d 项活动，时长暂未估计。", len(p.Activities))
	}
	var hours [24]int
	for _, f := range p.Continuity.Fragments {
		hours[f.Start.In(p.Start.Location()).Hour()]++
	}
	peak, count := 0, 0
	for hour, n := range hours {
		if n > count {
			peak, count = hour, n
		}
	}
	if count >= 2 {
		r.PeakHour = &peak
		r.Headline += fmt.Sprintf("%d 点附近短时切换较多。", peak)
		r.Advice = fmt.Sprintf("下次在 %02d:00 前留 10 分钟集中处理临时事项，再回到主要任务。", peak)
	} else if p.Continuity.TaskSwitches > 0 {
		r.Advice = "下次开始主要任务前，用 5 分钟处理临时事项，再留出一段连续时间。"
	} else {
		r.Advice = "下次开始记录时先选一件主要任务，结束后再回顾这段时间。"
	}
	return r
}

type FragmentSource struct {
	Name             string   `json:"name"`
	Apps             []string `json:"apps"`
	TaskIDs          []string `json:"task_ids"`
	Count            int      `json:"count"`
	EstimatedSeconds int64    `json:"estimated_seconds"`
}

func displayApp(app string) string {
	name := filepath.Base(app)
	known := map[string]string{"com.tencent.xinwechat": "微信", "wechat.exe": "微信", "weixin.exe": "微信", "com.tencent.weworkmac": "企业微信", "wxwork.exe": "企业微信", "org.telegram.desktop": "Telegram", "telegram.exe": "Telegram", "com.google.chrome": "Chrome", "chrome.exe": "Chrome", "com.apple.safari": "Safari", "com.microsoft.edgemac": "Edge", "msedge.exe": "Edge", "com.microsoft.vscode": "VS Code", "code.exe": "VS Code", "com.openai.chat": "ChatGPT"}
	if v := known[strings.ToLower(name)]; v != "" {
		return v
	}
	return name
}

func fragmentSources(fragments []TimeFragment) []FragmentSource {
	byKey := map[string]*FragmentSource{}
	for _, f := range fragments {
		apps := append([]string(nil), f.SourceApps...)
		sort.Strings(apps)
		key := strings.ToLower(strings.Join(apps, "\n"))
		s := byKey[key]
		if s == nil {
			var names []string
			for _, app := range apps {
				names = append(names, displayApp(app))
			}
			name := strings.Join(names, "、")
			if name == "" {
				name = "来源未记录"
			}
			s = &FragmentSource{Name: name, Apps: apps}
			byKey[key] = s
		}
		s.Count++
		s.EstimatedSeconds += f.EstimatedSeconds
		found := false
		for _, id := range s.TaskIDs {
			found = found || id == f.TaskID
		}
		if !found {
			s.TaskIDs = append(s.TaskIDs, f.TaskID)
		}
	}
	var result []FragmentSource
	for _, s := range byKey {
		result = append(result, *s)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Name < result[j].Name
	})
	return result
}
