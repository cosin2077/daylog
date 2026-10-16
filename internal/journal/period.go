package journal

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Activity struct {
	Parts               []ActivityPart `json:"parts,omitempty"`
	InferredSeconds     int64          `json:"inferred_seconds"`
	LegacyCategory      string         `json:"legacy_category,omitempty"`
	Uncertainties       []string       `json:"uncertainties,omitempty"`
	Spans               []TimeSpan     `json:"spans,omitempty"`
	Apps                []string       `json:"apps,omitempty"`
	InterpolatedSeconds int64          `json:"interpolated_seconds"`
	TaskID              string         `json:"task_id,omitempty"`
	ActivityID          string         `json:"activity_id,omitempty"`
	EstimatedSeconds    int64          `json:"estimated_seconds"`
	DurationMethod      string         `json:"duration_method"`
	Confidence          string         `json:"confidence,omitempty"`
	Start               time.Time      `json:"start"`
	End                 time.Time      `json:"end"`
	Title               string         `json:"title"`
	Summary             string         `json:"summary"`
	Category            string         `json:"category"`
	Evidence            []string       `json:"evidence"`
	BatchID             string         `json:"batch_id"`
	Source              string         `json:"source"`
}

type CategorySummary struct {
	EstimatedSeconds int64  `json:"estimated_seconds"`
	Name             string `json:"name"`
	Count            int    `json:"count"`
}

type DaySummary struct {
	Date            string `json:"date"`
	Activities      int    `json:"activities"`
	CoverageSeconds int64  `json:"coverage_seconds"`
	PendingBatches  int    `json:"pending_batches"`
}

type TaskSummary struct {
	TaskID           string `json:"task_id"`
	Title            string `json:"title"`
	EstimatedSeconds int64  `json:"estimated_seconds"`
}

type AnalysisFailure struct {
	BatchID        string    `json:"batch_id"`
	Reason         string    `json:"reason"`
	FailureRounds  int       `json:"failure_rounds"`
	RetryAfter     time.Time `json:"retry_after"`
	NeedsAttention bool      `json:"needs_attention"`
}
type TimeBucket struct {
	Start            time.Time         `json:"start"`
	End              time.Time         `json:"end"`
	EstimatedSeconds int64             `json:"estimated_seconds"`
	Categories       []CategorySummary `json:"categories"`
}

type PeriodReport struct {
	Rhythm             []RhythmBlock     `json:"rhythm,omitempty"`
	InferredSeconds    int64             `json:"inferred_seconds"`
	Comparison         *PeriodComparison `json:"comparison,omitempty"`
	Review             ReviewSummary     `json:"review"`
	InterruptedBatches int               `json:"interrupted_batches"`
	Continuity         ContinuitySummary `json:"continuity"`
	UnknownSpans       []TimeSpan        `json:"unknown_spans,omitempty"`
	Failures           []AnalysisFailure `json:"failures"`
	Distribution       []TimeBucket      `json:"distribution"`

	LegacyAppMetadata     bool              `json:"legacy_app_metadata"`
	InterpolatedSeconds   int64             `json:"interpolated_seconds"`
	EstimatedSeconds      int64             `json:"estimated_seconds"`
	UnknownSeconds        int64             `json:"unknown_seconds"`
	LegacyCoverageSeconds int64             `json:"legacy_coverage_seconds"`
	Tasks                 []TaskSummary     `json:"tasks"`
	Period                string            `json:"period"`
	Anchor                string            `json:"anchor"`
	Start                 time.Time         `json:"start"`
	End                   time.Time         `json:"end"`
	Summary               string            `json:"summary"`
	Activities            []Activity        `json:"activities"`
	Categories            []CategorySummary `json:"categories"`
	Days                  []DaySummary      `json:"days"`
	CoverageSeconds       int64             `json:"coverage_seconds"`
	ActiveDays            int               `json:"active_days"`
	AnalyzedBatches       int               `json:"analyzed_batches"`
	EvidenceCount         int               `json:"evidence_count"`
	PendingBatches        int               `json:"pending_batches"`
	FailedBatches         int               `json:"failed_batches"`
	DraftFrames           int               `json:"draft_frames"`
}

type Overview struct {
	Periods     []PeriodReport `json:"periods,omitempty"`
	Calendar    []PeriodReport `json:"calendar,omitempty"`
	Anchor      string         `json:"anchor"`
	GeneratedAt time.Time      `json:"generated_at"`
	Timezone    string         `json:"timezone"`
	Sample      bool           `json:"sample"`
	Views       []PeriodReport `json:"views"`
}

func PeriodBounds(period string, anchor time.Time) (time.Time, time.Time, error) {
	start := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, anchor.Location())
	switch period {
	case "day":
		return start, start.AddDate(0, 0, 1), nil
	case "week":
		start = start.AddDate(0, 0, -((int(start.Weekday()) + 6) % 7))
		return start, start.AddDate(0, 0, 7), nil
	case "month":
		start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
		return start, start.AddDate(0, 1, 0), nil
	default:
		return time.Time{}, time.Time{}, errors.New("period 必须为 day、week 或 month")
	}
}

func (s *Store) Overview(anchor time.Time) (Overview, error) {
	var overview Overview
	err := s.locked(func() error {
		start, end, _ := PeriodBounds("month", anchor)
		start = start.AddDate(0, -2, 0)
		weekStart, weekEnd, _ := PeriodBounds("week", anchor)
		if weekStart.Before(start) {
			start = weekStart
		}
		if weekEnd.After(end) {
			end = weekEnd
		}
		reports, err := s.Reports(start, end)
		if err != nil {
			return err
		}
		pending, err := s.Pending(0)
		if err != nil {
			return err
		}
		var draft Batch
		if err = ReadJSON(filepath.Join(s.Root, "draft.json"), &draft); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		overview = BuildOverview(anchor, reports, pending, draft)
		return nil
	})
	return overview, err
}

// Period summaries reuse validated activity cards; they make no model calls.
func BuildOverview(anchor time.Time, reports []Report, pending []Batch, draft Batch) Overview {
	zone := anchor.Location().String()
	if zone == "Local" {
		zone = "本机时区"
	}
	o := Overview{Anchor: anchor.Format("2006-01-02"), GeneratedAt: time.Now().In(anchor.Location()), Timezone: zone, Views: []PeriodReport{}}
	for _, kind := range []string{"day", "week", "month"} {
		view := buildPeriod(kind, anchor, reports, pending, draft)
		view.Comparison = buildComparison(kind, anchor, o.GeneratedAt, reports)
		o.Views = append(o.Views, view)
	}
	start, end, _ := PeriodBounds("month", anchor)
	start = start.AddDate(0, -1, 0)
	navigationMonthStart := start
	start, _, _ = PeriodBounds("week", start)
	weekStart, weekEnd, _ := PeriodBounds("week", anchor)
	if weekStart.Before(start) {
		start = weekStart
	}
	if weekEnd.After(end) {
		end = weekEnd
	}
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		view := buildPeriod("day", day, reports, pending, draft)
		view.Comparison = buildComparison("day", day, o.GeneratedAt, reports)
		o.Calendar = append(o.Calendar, view)
	}
	_, monthEnd, _ := PeriodBounds("month", anchor)
	for month := navigationMonthStart; month.Before(monthEnd); month = month.AddDate(0, 1, 0) {
		monthStart, _, _ := PeriodBounds("month", month)
		view := buildPeriod("month", monthStart, reports, pending, draft)
		view.Comparison = buildComparison("month", monthStart, o.GeneratedAt, reports)
		o.Periods = append(o.Periods, view)
	}
	firstWeek, _, _ := PeriodBounds("week", start)
	for week := firstWeek; week.Before(end); week = week.AddDate(0, 0, 7) {
		view := buildPeriod("week", week, reports, pending, draft)
		view.Comparison = buildComparison("week", week, o.GeneratedAt, reports)
		o.Periods = append(o.Periods, view)
	}
	return o
}

type span struct{ start, end time.Time }

func covered(spans []span) int64 {
	if len(spans) == 0 {
		return 0
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	start, end := spans[0].start, spans[0].end
	var total time.Duration
	for _, s := range spans[1:] {
		if s.start.After(end) {
			total += end.Sub(start)
			start, end = s.start, s.end
		} else if s.end.After(end) {
			end = s.end
		}
	}
	total += end.Sub(start)
	return int64(total / time.Second)
}

func category(raw string) string {
	switch name := strings.TrimSpace(raw); name {
	case "写代码", "写东西", "查资料", "聊天沟通", "看内容", "玩游戏", "处理杂事", "未细分":
		return name
	case "沟通":
		return "聊天沟通"
	}
	return "未细分"
}

var activityCategories = []string{"写代码", "写东西", "查资料", "聊天沟通", "看内容", "玩游戏", "处理杂事", "未细分"}

func buildPeriod(kind string, anchor time.Time, reports []Report, pending []Batch, draft Batch) PeriodReport {
	start, end, _ := PeriodBounds(kind, anchor)
	return buildPeriodRange(kind, anchor, start, end, reports, pending, draft)
}

func buildPeriodRange(kind string, anchor, start, end time.Time, reports []Report, pending []Batch, draft Batch) PeriodReport {
	prepared := make([]Report, len(reports))
	for i, r := range reports {
		prepared[i] = estimateReport(r)
	}
	reports = prepared
	p := PeriodReport{Period: kind, Anchor: anchor.Format("2006-01-02"), Start: start, End: end, Activities: []Activity{}, Categories: []CategorySummary{}, Days: []DaySummary{}}
	byDay := map[string][]span{}
	dayCount := map[string]int{}
	dayPending := map[string]int{}
	counts := map[string]int{}
	evidence := map[string]bool{}
	batches := map[string]bool{}
	var spans, estimated, legacy, unknown, interpolated, inferred []span
	categorySpans := map[string][]span{}
	taskSpans := map[string][]span{}
	taskTitles := map[string]string{}
	taskTitleSeconds := map[string]int64{}
	for _, r := range reports {
		if r.SchemaVersion == 2 && r.AppSource == "" && r.Start.Before(end) && r.End.After(start) {
			p.LegacyAppMetadata = true
		}
		if r.SchemaVersion == 2 && r.Start.Before(end) && r.End.After(start) {
			batches[r.BatchID] = true
		}
		for _, part := range r.UnknownSpans {
			if p, ok := clipSpan(part, start, end); ok {
				unknown = append(unknown, p)
			}
		}
		for _, c := range r.Cards {
			from, to := c.Start.In(start.Location()), c.End.In(start.Location())
			if !activityInRange(c.Start, c.End, start, end) {
				continue
			}
			if from.Before(start) {
				from = start
			}
			if to.After(end) {
				to = end
			}
			batches[r.BatchID] = true
			for _, id := range c.Evidence {
				evidence[id] = true
			}
			// A card crossing midnight is shown as two date-local fragments.
			instant := from.Equal(to)
			for from.Before(to) || instant {
				midnight := time.Date(from.Year(), from.Month(), from.Day()+1, 0, 0, 0, 0, from.Location())
				until := to
				if until.After(midnight) {
					until = midnight
				}
				a := Activity{Apps: append([]string(nil), c.Apps...), Start: from, End: until, Title: c.Title, Summary: c.Summary, Category: category(c.Category), Evidence: append([]string{}, c.Evidence...), BatchID: r.BatchID, Source: r.Source, TaskID: c.TaskID, ActivityID: c.ActivityID, DurationMethod: c.DurationMethod, Confidence: c.Confidence}
				a.Uncertainties = append([]string(nil), c.Uncertainties...)
				if c.Category != a.Category {
					a.LegacyCategory = c.Category
				}
				day := from.Format("2006-01-02")
				var parts []span
				if r.SchemaVersion == 2 {
					for _, part := range c.Spans {
						if clipped, ok := clipSpan(part, from, until); ok {
							parts = append(parts, clipped)
							a.Spans = append(a.Spans, TimeSpan{Start: clipped.start.In(start.Location()), End: clipped.end.In(start.Location()), Interpolated: part.Interpolated, Inferred: part.Inferred})
							if part.Inferred {
								inferred = append(inferred, clipped)
								a.InferredSeconds += int64(clipped.end.Sub(clipped.start) / time.Second)
							}
							if part.Interpolated {
								interpolated = append(interpolated, clipped)
								a.InterpolatedSeconds += int64(clipped.end.Sub(clipped.start) / time.Second)
							}
						}
					}
					a.EstimatedSeconds = covered(append([]span(nil), parts...))
					estimated = append(estimated, parts...)
					categorySpans[a.Category] = append(categorySpans[a.Category], parts...)
					if c.TaskID != "" {
						taskSpans[c.TaskID] = append(taskSpans[c.TaskID], parts...)
						if _, exists := taskTitles[c.TaskID]; !exists || a.EstimatedSeconds > taskTitleSeconds[c.TaskID] {
							taskTitles[c.TaskID] = c.Title
							taskTitleSeconds[c.TaskID] = a.EstimatedSeconds
						}
					}
				} else {
					parts = []span{{from, until}}
					legacy = append(legacy, parts...)
					a.DurationMethod = "legacy_model_range"
				}
				p.Activities = append(p.Activities, a)
				spans = append(spans, parts...)
				byDay[day] = append(byDay[day], parts...)

				dayCount[day]++
				counts[a.Category]++
				from = until
				if instant {
					break
				}
			}
		}
	}
	sort.SliceStable(p.Activities, func(i, j int) bool { return p.Activities[i].Start.Before(p.Activities[j].Start) })
	p.Activities = mergeActivities(p.Activities)
	dayCount, counts = map[string]int{}, map[string]int{}
	for _, a := range p.Activities {
		dayCount[a.Start.Format("2006-01-02")]++
		counts[a.Category]++
	}
	for _, b := range pending {
		if b.Start.Before(end) && b.End.After(start) {
			p.PendingBatches++
			if PreparationState(b) == "interrupted" {
				p.InterruptedBatches++
			}
			if PreparationState(b) == "failed" {
				p.FailedBatches++
				p.Failures = append(p.Failures, AnalysisFailure{b.ID, FailureReason(b.LastError), b.FailureRounds, RetryTime(b).In(start.Location()), b.FailureRounds > 6})
			}
			for day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location()); day.Before(end); day = day.AddDate(0, 0, 1) {
				if b.Start.Before(day.AddDate(0, 0, 1)) && b.End.After(day) {
					dayPending[day.Format("2006-01-02")]++
				}
			}
		}
	}
	for _, f := range draft.Frames {
		if !f.At.Before(start) && f.At.Before(end) {
			p.DraftFrames++
		}
	}
	p.CoverageSeconds = covered(spans)
	p.EstimatedSeconds = covered(estimated)
	p.InterpolatedSeconds = covered(interpolated)
	p.InferredSeconds = covered(inferred)
	p.UnknownSeconds = covered(unknown)
	for _, part := range unknown {
		if len(p.UnknownSpans) > 0 && !part.start.After(p.UnknownSpans[len(p.UnknownSpans)-1].End) {
			if part.end.After(p.UnknownSpans[len(p.UnknownSpans)-1].End) {
				p.UnknownSpans[len(p.UnknownSpans)-1].End = part.end.In(start.Location())
			}
		} else {
			p.UnknownSpans = append(p.UnknownSpans, TimeSpan{Start: part.start.In(start.Location()), End: part.end.In(start.Location())})
		}
	}
	p.LegacyCoverageSeconds = covered(legacy)
	p.Tasks = []TaskSummary{}
	for id, title := range taskTitles {
		p.Tasks = append(p.Tasks, TaskSummary{id, title, covered(taskSpans[id])})
	}
	sort.Slice(p.Tasks, func(i, j int) bool {
		if p.Tasks[i].EstimatedSeconds == p.Tasks[j].EstimatedSeconds {
			return p.Tasks[i].TaskID < p.Tasks[j].TaskID
		}
		return p.Tasks[i].EstimatedSeconds > p.Tasks[j].EstimatedSeconds
	})
	p.AnalyzedBatches = len(batches)
	p.EvidenceCount = len(evidence)
	for day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location()); day.Before(end); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		seconds := covered(byDay[key])
		if dayCount[key] > 0 {
			p.ActiveDays++
		}
		p.Days = append(p.Days, DaySummary{Date: key, Activities: dayCount[key], CoverageSeconds: seconds, PendingBatches: dayPending[key]})
	}
	for _, name := range activityCategories {
		p.Categories = append(p.Categories, CategorySummary{Name: name, Count: counts[name], EstimatedSeconds: covered(append([]span(nil), categorySpans[name]...))})
	}
	// Bucket only actual observation spans. Never spread card bounding time
	// across gaps; hourly/day boundaries use the report's calendar timezone.
	for at := start; at.Before(end); {
		until := at.AddDate(0, 0, 1)
		if kind == "day" {
			until = at.Add(time.Hour)
		}
		if until.After(end) {
			until = end
		}
		bucket := TimeBucket{Start: at, End: until, Categories: []CategorySummary{}}
		var all []span
		for _, name := range activityCategories {
			var parts []span
			for _, part := range categorySpans[name] {
				if clipped, ok := clipSpan(TimeSpan{Start: part.start, End: part.end}, at, until); ok {
					parts = append(parts, clipped)
					all = append(all, clipped)
				}
			}
			bucket.Categories = append(bucket.Categories, CategorySummary{Name: name, EstimatedSeconds: covered(parts)})
		}
		bucket.EstimatedSeconds = covered(all)
		p.Distribution = append(p.Distribution, bucket)
		at = until
	}
	p.Continuity = buildContinuity(start, end, reports)
	p.Rhythm = buildRhythm(p.Activities)
	p.Review = buildReview(p)
	p.Summary = p.Review.Headline
	return p
}

func activityInRange(from, to, start, end time.Time) bool {
	if to.Before(from) {
		return false
	}
	if from.Equal(to) {
		return !from.Before(start) && from.Before(end)
	}
	return from.Before(end) && to.After(start)
}

func clipSpan(p TimeSpan, start, end time.Time) (span, bool) {
	if p.Start.Before(start) {
		p.Start = start
	}
	if p.End.After(end) {
		p.End = end
	}
	return span{p.Start, p.End}, p.End.After(p.Start)
}

func (s *Store) RangeOverview(start, end time.Time) (Overview, error) {
	var out Overview
	if !end.After(start) || end.Sub(start) > 366*24*time.Hour {
		return out, errors.New("range must be positive and at most 366 days")
	}
	err := s.locked(func() error {
		reports, err := s.Reports(start, end)
		if err != nil {
			return err
		}
		pending, err := s.Pending(0)
		if err != nil {
			return err
		}
		var draft Batch
		if err = ReadJSON(filepath.Join(s.Root, "draft.json"), &draft); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		out = Overview{Anchor: start.Format("2006-01-02"), GeneratedAt: time.Now().In(start.Location()), Timezone: start.Location().String(), Views: []PeriodReport{buildPeriodRange("range", start, start, end, reports, pending, draft)}}
		return nil
	})
	return out, err
}
