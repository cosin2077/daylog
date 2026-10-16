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
	scope := map[string]string{"day": "这一天", "week": "这一周", "month": "这个月", "range": "这段时间"}[p.Period]
	if scope == "" {
		scope = "这段时间"
	}
	next := map[string]string{"day": "下次", "week": "下周", "month": "下个月", "range": "下次"}[p.Period]
	if next == "" {
		next = "下次"
	}
	if len(p.Activities) == 0 {
		return ReviewSummary{Headline: scope + "还没有已整理的活动。", Advice: next + "先积累一些记录，再回顾主要任务和连续时段。"}
	}
	r := ReviewSummary{}
	for _, task := range p.Tasks {
		if task.EstimatedSeconds > 0 {
			title := task.Title
			if len([]rune(title)) > 48 {
				title = truncate(title, 48) + "…"
			}
			r.Headline = scope + "，最多时间在「" + title + "」。"
			break
		}
	}
	if r.Headline == "" {
		ordered := append([]CategorySummary(nil), p.Categories...)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].EstimatedSeconds > ordered[j].EstimatedSeconds })
		for _, c := range ordered {
			if c.Name != "未细分" && c.EstimatedSeconds > 0 {
				r.Headline = scope + "可归类的活动以" + c.Name + "为主。"
				break
			}
		}
		if r.Headline == "" {
			r.Headline = fmt.Sprintf("%s记录了 %d 项活动，暂不足以概括主线。", scope, len(p.Activities))
		}
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
	}
	if (p.Period == "week" || p.Period == "month") && p.ActiveDays < 3 {
		r.Advice = fmt.Sprintf("%s只有 %d 天记录，先不判断长期习惯。%s在相近时段记录几天，再比较连续时段和来回切换。", scope, p.ActiveDays, next)
	} else if count >= 2 {
		r.Advice = fmt.Sprintf("%s的短时切换较多出现在 %02d:00 附近。%s在这个时段前留 10 分钟集中处理临时事项，再比较来回切换是否减少。", scope, peak, next)
	} else if p.Period == "week" {
		r.Advice = "下周为排行中的主要任务预留两段连续时间；周末对照每天的记录，看看哪些时段更容易连续推进。"
	} else if p.Period == "month" {
		r.Advice = "下个月每周为主要任务预留两段连续时间；月底结合日历对照记录天数与最长连续时段。"
	} else {
		r.Advice = "下次开始主要任务前，用 5 分钟处理临时事项，再留出一段连续时间。"
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
	known := map[string]string{"com.tencent.xinwechat": "微信", "wechat.exe": "微信", "weixin.exe": "微信", "com.tencent.weworkmac": "企业微信", "wxwork.exe": "企业微信", "org.telegram.desktop": "Telegram", "ru.keepcoder.telegram": "Telegram", "com.hnc.discord": "Discord", "com.apple.terminal": "终端", "com.apple.ical": "日历", "com.apple.finder": "访达", "com.apple.notes": "备忘录", "com.todesktop.230313mzl4w4u92": "Cursor", "com.microsoft.teams2": "Teams", "telegram.exe": "Telegram", "com.google.chrome": "Chrome", "chrome.exe": "Chrome", "com.apple.safari": "Safari", "com.microsoft.edgemac": "Edge", "msedge.exe": "Edge", "com.microsoft.vscode": "VS Code", "code.exe": "VS Code", "com.openai.chat": "ChatGPT"}
	if v := known[strings.ToLower(name)]; v != "" {
		return v
	}
	name = strings.TrimSuffix(name, ".app")
	if strings.HasSuffix(strings.ToLower(name), ".exe") {
		name = name[:len(name)-4]
	}
	for _, prefix := range []string{"com.", "org.", "net.", "io.", "app.", "dev.", "ru."} {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			parts := strings.Split(name, ".")
			if len(parts) > 2 && parts[len(parts)-1] != "" {
				return parts[len(parts)-1]
			}
		}
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
