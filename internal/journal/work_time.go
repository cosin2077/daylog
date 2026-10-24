package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// WorkTime separates evidence of computer activity from knowledge of its task.
// All durations remain estimates; missing samples never prove absence.
type WorkTime struct {
	Available           bool       `json:"available"`
	EstimatedSeconds    int64      `json:"estimated_seconds"`
	ClassifiedSeconds   int64      `json:"classified_seconds"`
	UnclassifiedSeconds int64      `json:"unclassified_seconds"`
	AwaySeconds         int64      `json:"away_seconds"`
	MissingSeconds      int64      `json:"missing_seconds"`
	UnclassifiedSpans   []TimeSpan `json:"unclassified_spans,omitempty"`
}

type workPoint struct {
	at    time.Time
	state string
	gap   int
	cut   bool
	raw   bool
}

type workLedger struct {
	points  []workPoint
	work    []span
	away    []span
	gaps    []span
	blocked []span
	windows []span
}

func workingState(state string) bool {
	// Old task estimation used "uncertain" for an available screenshot whose
	// task label could not be recovered. It is not a capture failure.
	return state == "sampled" || state == "active" || state == "uncertain"
}
func awayState(state string) bool {
	return state == "idle" || state == "screen_locked" || state == "screen_asleep"
}

func newWorkLedger(reports []Report, pending []Batch, draft Batch, observations []Observation) *workLedger {
	l := &workLedger{}
	for _, r := range reports {
		if r.SchemaVersion == 2 && r.End.After(r.Start) {
			l.windows = append(l.windows, span{r.Start, r.End})
		}
		var stream []workPoint
		for _, p := range r.TaskObservations {
			stream = append(stream, workPoint{p.At, p.State, p.MaxGapSeconds, p.State == "missing", false})
		}
		l.addStream(stream)
	}
	for _, b := range append(append([]Batch(nil), pending...), draft) {
		if b.SchemaVersion == 2 && b.End.After(b.Start) {
			l.windows = append(l.windows, span{b.Start, b.End})
		}
		var stream []workPoint
		for _, p := range b.Observations {
			gap := p.MaxGapSeconds
			if gap == 0 {
				gap = b.MaxGapSeconds
			}
			stream = append(stream, workPoint{p.At, p.State, gap, p.State == "missing", false})
		}
		l.addStream(stream)
	}
	// Raw events come last, preserving their observed state at a shared instant.
	var raw []workPoint
	for _, p := range observations {
		raw = append(raw, workPoint{p.At, p.State, p.MaxGapSeconds, p.State == "missing", true})
	}
	l.addStream(raw)
	sort.SliceStable(l.points, func(i, j int) bool { return l.points[i].at.Before(l.points[j].at) })
	points := l.points[:0]
	for _, p := range l.points {
		if p.at.IsZero() {
			continue
		}
		if p.gap < 20 || p.gap > 600 {
			p.gap = 300 // Older event logs omitted this bound.
		}
		if len(points) > 0 && points[len(points)-1].at.Equal(p.at) {
			previous := points[len(points)-1]
			p.gap = min(p.gap, previous.gap)
			switch {
			case p.raw:
				// The actual ordered log wins over a projected report boundary.
			case previous.raw:
				p.state, p.cut, p.raw = previous.state, previous.cut, true
			default:
				p.state = conservativeWorkState(previous.state, p.state)
				p.cut = p.cut || previous.cut
			}
			points[len(points)-1] = p
		} else {
			points = append(points, p)
		}
	}
	l.points = points
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		part := span{a.at, b.at}
		l.windows = append(l.windows, part)
		switch {
		case awayState(a.state):
			l.away = append(l.away, part)
		case workingState(a.state) && !b.cut && b.at.Sub(a.at) <= time.Duration(min(a.gap, b.gap))*time.Second:
			l.work = append(l.work, part)
		default:
			l.gaps = append(l.gaps, part)
			if b.cut || !workingState(a.state) && a.state != "" {
				l.blocked = append(l.blocked, part)
			}
		}
	}
	l.work = mergedWorkSpans(l.work)
	l.away = mergedWorkSpans(l.away)
	l.gaps = mergedWorkSpans(l.gaps)
	l.blocked = mergedWorkSpans(l.blocked)
	l.windows = mergedWorkSpans(l.windows)
	return l
}

// Collapse each ordered source before combining reports. A transient missing
// then active event can share an instant; unrelated overlapping reports have
// no such ordering and must preserve the more restrictive availability fact.
func (l *workLedger) addStream(stream []workPoint) {
	points := make(map[time.Time]workPoint)
	for _, p := range stream {
		p.at = p.at.UTC()
		if old, ok := points[p.at]; ok {
			p.cut = p.cut || old.cut
			if old.gap > 0 && (p.gap == 0 || old.gap < p.gap) {
				p.gap = old.gap
			}
		}
		points[p.at] = p
	}
	for _, p := range points {
		l.points = append(l.points, p)
	}
}

func conservativeWorkState(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if workingState(a) != workingState(b) {
		if workingState(a) {
			return b
		}
		return a
	}
	if awayState(a) != awayState(b) {
		if awayState(a) {
			return a
		}
		return b
	}
	if a < b {
		return a
	}
	return b
}

func clippedWorkSpans(parts []span, start, end time.Time) []span {
	var out []span
	for _, p := range parts {
		if part, ok := clipSpan(TimeSpan{Start: p.start, End: p.end}, start, end); ok {
			out = append(out, part)
		}
	}
	return out
}

func (l *workLedger) summary(start, end time.Time, classified []span) WorkTime {
	work := clippedWorkSpans(l.work, start, end)
	away := clippedWorkSpans(l.away, start, end)
	gaps := clippedWorkSpans(l.gaps, start, end)
	classified = clippedWorkSpans(classified, start, end)
	// Retain old, already-supported task estimates when event metadata is absent.
	// Explicit unavailable intervals always take precedence over that fallback.
	var excluded []TimeSpan
	for _, p := range append(append([]span(nil), away...), clippedWorkSpans(l.blocked, start, end)...) {
		excluded = append(excluded, TimeSpan{Start: p.start, End: p.end})
	}
	var available []span
	for _, p := range work {
		for _, allowed := range subtractSpans(p.start, p.end, excluded) {
			available = append(available, span{allowed.Start, allowed.End})
		}
	}
	work = available
	for _, p := range classified {
		for _, allowed := range subtractSpans(p.start, p.end, excluded) {
			work = append(work, span{allowed.Start, allowed.End})
		}
	}
	var assigned []span
	for _, task := range classified {
		assigned = append(assigned, clippedWorkSpans(work, task.start, task.end)...)
	}
	result := WorkTime{Available: len(work)+len(away)+len(gaps) > 0, EstimatedSeconds: covered(work), ClassifiedSeconds: covered(assigned), AwaySeconds: covered(away)}
	result.UnclassifiedSeconds = result.EstimatedSeconds - result.ClassifiedSeconds
	var taskSpans []TimeSpan
	for _, p := range classified {
		taskSpans = append(taskSpans, TimeSpan{Start: p.start, End: p.end})
	}
	// Merge computer intervals before subtracting task attribution, avoiding
	// duplicate rows from overlapping batches or repeated evidence.
	for _, p := range mergedWorkSpans(work) {
		for _, unknown := range subtractSpans(p.start, p.end, taskSpans) {
			unknown.Start, unknown.End = unknown.Start.In(start.Location()), unknown.End.In(start.Location())
			result.UnclassifiedSpans = append(result.UnclassifiedSpans, unknown)
		}
	}
	var accounted []TimeSpan
	for _, p := range append(append([]span(nil), work...), away...) {
		accounted = append(accounted, TimeSpan{Start: p.start, End: p.end})
	}
	var missing []span
	for _, p := range clippedWorkSpans(l.windows, start, end) {
		for _, part := range subtractSpans(p.start, p.end, accounted) {
			missing = append(missing, span{part.Start, part.End})
		}
	}
	result.MissingSeconds = covered(missing)
	result.Available = result.Available || result.MissingSeconds > 0
	return result
}

// ReportWorkTime uses only committed sources, suitable for durable exports.
func ReportWorkTime(reports []Report, start, end time.Time) WorkTime {
	var classified []span
	ledger := &workLedger{}
	for _, r := range reports {
		if r.SchemaVersion != 2 || !r.End.After(r.Start) {
			continue
		}
		part := newWorkLedger([]Report{r}, nil, Batch{}, nil)
		// A durable export owns this committed window, not the interval to
		// another batch. Availability events outside it may be uncommitted.
		ledger.work = append(ledger.work, clippedWorkSpans(part.work, r.Start, r.End)...)
		ledger.away = append(ledger.away, clippedWorkSpans(part.away, r.Start, r.End)...)
		ledger.gaps = append(ledger.gaps, clippedWorkSpans(part.gaps, r.Start, r.End)...)
		ledger.blocked = append(ledger.blocked, clippedWorkSpans(part.blocked, r.Start, r.End)...)
		ledger.windows = append(ledger.windows, span{r.Start, r.End})
		for _, c := range r.Cards {
			for _, p := range c.Spans {
				if value, ok := clipSpan(p, r.Start, r.End); ok {
					classified = append(classified, value)
				}
			}
		}
	}
	return ledger.summary(start, end, classified)
}

func mergedWorkSpans(parts []span) []span {
	parts = append([]span(nil), parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].start.Before(parts[j].start) })
	var out []span
	for _, p := range parts {
		if len(out) == 0 || p.start.After(out[len(out)-1].end) {
			out = append(out, p)
		} else if p.end.After(out[len(out)-1].end) {
			out[len(out)-1].end = p.end
		}
	}
	return out
}

// ReadObservations reads timing metadata only. An in-progress final JSONL write
// is ignored until complete; malformed completed records remain an error.
func (s *Store) ReadObservations(start, end time.Time) ([]Observation, error) {
	var out []Observation
	for _, date := range daysBetween(start.Add(-24*time.Hour), end.Add(24*time.Hour)) {
		name := date[:4] + "-" + date[4:6] + "-" + date[6:8] + ".jsonl"
		f, err := os.Open(filepath.Join(s.Root, "observations", name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer f.Close()
			reader := bufio.NewReaderSize(f, 16<<10)
			for {
				line, readErr := reader.ReadSlice('\n')
				if errors.Is(readErr, io.EOF) {
					return nil
				}
				if readErr != nil {
					return errors.New("无法读取采样时间记录")
				}
				var p Observation
				if json.Unmarshal(line, &p) != nil || p.At.IsZero() {
					return errors.New("采样时间记录无效，请运行 daylog doctor 检查")
				}
				if !p.At.Before(start.Add(-24*time.Hour)) && p.At.Before(end.Add(24*time.Hour)) {
					out = append(out, p)
				}
			}
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
