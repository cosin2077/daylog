package journal

import (
	"sort"
	"time"
)

type TaskReturn struct {
	TaskID   string    `json:"task_id"`
	Title    string    `json:"title"`
	ViaTitle string    `json:"via_title"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
}

// A short visit bracketed by another task on both sides. The surrounding
// transition gaps locate the visit; this continuity measure excludes inferred time.
type TimeFragment struct {
	SourceApps       []string  `json:"source_apps,omitempty"`
	TaskID           string    `json:"task_id"`
	Title            string    `json:"title"`
	Category         string    `json:"category"`
	FromTitle        string    `json:"from_title"`
	NextTitle        string    `json:"next_title"`
	Start            time.Time `json:"start"`
	End              time.Time `json:"end"`
	NextAt           time.Time `json:"next_at"`
	EstimatedSeconds int64     `json:"estimated_seconds"`
	Returned         bool      `json:"returned"`
}

type FragmentDestination struct {
	TaskID           string `json:"task_id"`
	Title            string `json:"title"`
	Count            int    `json:"count"`
	EstimatedSeconds int64  `json:"estimated_seconds"`
}

type ContinuitySummary struct {
	FragmentSources           []FragmentSource      `json:"fragment_sources,omitempty"`
	Fragments                 []TimeFragment        `json:"fragments,omitempty"`
	FragmentDestinations      []FragmentDestination `json:"fragment_destinations,omitempty"`
	FragmentSeconds           int64                 `json:"fragment_seconds"`
	LongestSeconds            int64                 `json:"longest_seconds"`
	LongestTitle              string                `json:"longest_title,omitempty"`
	LongestSpan               *TimeSpan             `json:"longest_span,omitempty"`
	Runs                      int                   `json:"runs"`
	ShortRuns                 int                   `json:"short_runs"`
	ComparablePairs           int                   `json:"comparable_pairs"`
	TaskSwitches              int                   `json:"task_switches"`
	AppSwitches               int                   `json:"app_switches"`
	BriefReturns              int                   `json:"brief_returns"`
	Returns                   []TaskReturn          `json:"returns,omitempty"`
	MissingObservationBatches int                   `json:"missing_observation_batches"`
	Suggestion                string                `json:"suggestion"`
}

// Separate two claims: touching evidence spans support duration; adjacent
// labelled observations support a switch. Neighbor inference is excluded here.
func buildContinuity(start, end time.Time, reports []Report) ContinuitySummary {
	var result ContinuitySummary
	type run struct {
		start, end time.Time
		task       string
		category   string
	}
	var parts []run
	var points []TaskObservation
	titles := map[string]string{}
	pointCategories := map[string]string{}
	for _, r := range reports {
		if !r.Start.Before(end) || !r.End.After(start) {
			continue
		}
		if r.SchemaVersion != 2 || r.AppSource == "" || len(r.TaskObservations) == 0 {
			result.MissingObservationBatches++
		}
		if r.SchemaVersion != 2 || r.AppSource == "" {
			continue
		}
		for _, c := range r.Cards {
			if c.TaskID == "" || c.Confidence == "low" {
				continue
			}
			titles[c.TaskID] = c.Title
			if c.TaskTitle != "" {
				titles[c.TaskID] = c.TaskTitle
			}
			for _, s := range c.Spans {
				if s.Inferred || s.Interpolated {
					continue
				}
				clipped, ok := clipSpan(s, start, end)
				if !ok {
					continue
				}
				// A week/month must not join two days into a long focus session.
				at := clipped.start.In(start.Location())
				for at.Before(clipped.end) {
					until := time.Date(at.Year(), at.Month(), at.Day()+1, 0, 0, 0, 0, at.Location())
					if until.After(clipped.end) {
						until = clipped.end
					}
					parts = append(parts, run{start: at, end: until, task: c.TaskID})
					at = until
				}
			}
		}
		for _, p := range r.TaskObservations {
			if p.State == "active" {
				continue
			}
			if !p.At.Before(start) && p.At.Before(end) {
				points = append(points, p)
				if index := observationCard(r.Cards, p); index >= 0 {
					pointCategories[p.At.Format(time.RFC3339Nano)+"|"+p.TaskID] = category(r.Cards[index].Category)
				}
			}
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].start.Before(parts[j].start) })
	// Conflicting reports do not prove continuous activity for either task.
	conflict := make([]bool, len(parts))
	for i := range parts {
		for j := i + 1; j < len(parts) && parts[j].start.Before(parts[i].end); j++ {
			if parts[i].task != parts[j].task {
				conflict[i], conflict[j] = true, true
			}
		}
	}
	var runs []run
	for i, p := range parts {
		if conflict[i] {
			continue
		}
		if len(runs) > 0 {
			last := &runs[len(runs)-1]
			if last.task == p.task && !p.start.After(last.end) && sameDay(last.start, p.start, start.Location()) {
				if p.end.After(last.end) {
					last.end = p.end
				}
				continue
			}
		}
		runs = append(runs, p)
	}
	result.Runs = len(runs)
	for _, r := range runs {
		n := int64(r.end.Sub(r.start) / time.Second)
		if n < 300 {
			result.ShortRuns++
		}
		if n > result.LongestSeconds {
			result.LongestSeconds = n
			result.LongestTitle = titles[r.task]
			result.LongestSpan = &TimeSpan{Start: r.start.In(start.Location()), End: r.end.In(start.Location())}
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].At.Before(points[j].At) })
	var unique []TaskObservation
	for _, p := range points {
		if len(unique) > 0 && unique[len(unique)-1].At.Equal(p.At) {
			last := &unique[len(unique)-1]
			if last.TaskID != p.TaskID {
				last.TaskID = ""
			}
			if last.App != p.App {
				last.App = ""
			}
			if p.MaxGapSeconds < last.MaxGapSeconds {
				last.MaxGapSeconds = p.MaxGapSeconds
			}
		} else {
			unique = append(unique, p)
		}
	}
	var visits []run
	for i, p := range unique {
		valid := p.TaskID != "" && titles[p.TaskID] != "" && p.MaxGapSeconds >= 20 && p.MaxGapSeconds <= 600
		connected := false
		if i > 0 && valid && len(visits) > 0 {
			prev := unique[i-1]
			limit := min(prev.MaxGapSeconds, p.MaxGapSeconds)
			connected = prev.TaskID != "" && limit >= 20 && p.At.Sub(prev.At) <= time.Duration(limit)*time.Second && sameDay(prev.At, p.At, start.Location())
		}
		if !connected {
			visits = nil
		}
		if !valid {
			continue
		}
		if connected {
			result.ComparablePairs++
			prev := unique[i-1]
			if prev.App != "" && p.App != "" && prev.App != p.App {
				result.AppSwitches++
			}
		}
		if len(visits) > 0 && visits[len(visits)-1].task == p.TaskID {
			visits[len(visits)-1].end = p.At
			continue
		}
		if connected {
			result.TaskSwitches++
		}
		visits = append(visits, run{start: p.At, end: p.At, task: p.TaskID, category: pointCategories[p.At.Format(time.RFC3339Nano)+"|"+p.TaskID]})
		if len(visits) > 3 {
			visits = visits[len(visits)-3:]
		}
		if len(visits) == 3 && p.At.Sub(visits[0].end) <= 5*time.Minute {
			middle := visits[1]
			var supported []span
			for _, r := range runs {
				if r.task != middle.task {
					continue
				}
				if part, ok := clipSpan(TimeSpan{Start: r.start, End: r.end}, middle.start, middle.end); ok {
					supported = append(supported, part)
				}
			}
			result.Fragments = append(result.Fragments, TimeFragment{TaskID: middle.task, Title: titles[middle.task], Category: category(middle.category), FromTitle: titles[visits[0].task], NextTitle: titles[p.TaskID], Start: middle.start.In(start.Location()), End: middle.end.In(start.Location()), NextAt: p.At.In(start.Location()), EstimatedSeconds: covered(supported), Returned: visits[0].task == p.TaskID})
		}
		if len(visits) == 3 && visits[0].task == p.TaskID && p.At.Sub(visits[0].end) <= 5*time.Minute {
			result.BriefReturns++
			result.Returns = append(result.Returns, TaskReturn{TaskID: p.TaskID, Title: titles[p.TaskID], ViaTitle: titles[visits[1].task], Start: visits[0].end.In(start.Location()), End: p.At.In(start.Location())})
			if len(result.Returns) > 20 {
				result.Returns = result.Returns[1:]
			}
		}
	}
	byTask := map[string]*FragmentDestination{}
	for i := range result.Fragments {
		f := &result.Fragments[i]
		seen := map[string]bool{}
		for _, point := range unique {
			if point.TaskID == f.TaskID && !point.At.Before(f.Start) && !point.At.After(f.End) && point.App != "" && !seen[point.App] {
				seen[point.App] = true
				f.SourceApps = append(f.SourceApps, point.App)
			}
		}
		d := byTask[f.TaskID]
		if d == nil {
			d = &FragmentDestination{TaskID: f.TaskID, Title: f.Title}
			byTask[f.TaskID] = d
		}
		d.Count++
		d.EstimatedSeconds += f.EstimatedSeconds
		result.FragmentSeconds += f.EstimatedSeconds
	}
	result.FragmentSources = fragmentSources(result.Fragments)
	for _, d := range byTask {
		result.FragmentDestinations = append(result.FragmentDestinations, *d)
	}
	sort.Slice(result.FragmentDestinations, func(i, j int) bool {
		a, b := result.FragmentDestinations[i], result.FragmentDestinations[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.EstimatedSeconds != b.EstimatedSeconds {
			return a.EstimatedSeconds > b.EstimatedSeconds
		}
		return a.TaskID < b.TaskID
	})
	switch {
	case result.BriefReturns > 0:
		result.Suggestion = "可以试着把临时消息或查阅集中到一个间歇处理，再比较下次是否少了来回切换。"
	case result.TaskSwitches >= 6:
		result.Suggestion = "可以先选一件事做一小段，其他念头记下来稍后处理；再比较任务切换是否减少。"
	default:
		result.Suggestion = "当前证据不足以判断是否分心。回顾主要任务是否值得这些时间，比单看切换次数更有用。"
	}
	return result
}

func sameDay(a, b time.Time, zone *time.Location) bool {
	return a.In(zone).Format("2006-01-02") == b.In(zone).Format("2006-01-02")
}
