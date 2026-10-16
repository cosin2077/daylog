package journal

import (
	"sort"
	"time"
)

const estimationVersion = "bounded_neighbors_v2"

// Estimate only inside adjacent, bounded observations. Inferred spans are kept
// separate from continuous observation evidence and never extend beyond a pair.
// Old reports are upgraded in memory; their stored interpretation is immutable.
func estimateReport(input Report) Report {
	if input.EstimationVersion == estimationVersion || input.SchemaVersion != 2 || input.AppSource == "" || len(input.TaskObservations) < 2 {
		return input
	}
	r := input
	r.Cards = append([]Card(nil), input.Cards...)
	var occupied []TimeSpan
	for i := range r.Cards {
		r.Cards[i].Spans = append([]TimeSpan(nil), input.Cards[i].Spans...)
		occupied = append(occupied, r.Cards[i].Spans...)
	}
	points := uniqueObservations(input.TaskObservations)
	cardAt := func(p TaskObservation) int {
		if p.TaskID == "" || p.At.Before(r.Start) || p.At.After(r.End) {
			return -1
		}
		best := -1
		var distance time.Duration
		for i, c := range input.Cards {
			if c.TaskID != p.TaskID || (c.Confidence != "medium" && c.Confidence != "high") {
				continue
			}
			// Reused screenshot observations can lie outside the old card's
			// keyframe bounds. The persisted task label, not that old bound,
			// identifies them; choose the nearest card when a task has phases.
			d := time.Duration(0)
			if p.At.Before(c.Start) {
				d = c.Start.Sub(p.At)
			}
			if p.At.After(c.End) {
				d = p.At.Sub(c.End)
			}
			if best < 0 || d < distance {
				best, distance = i, d
			}
		}
		return best
	}
	added := false
	add := func(index int, from, to time.Time) {
		if index < 0 {
			return
		}
		if from.Before(r.Start) {
			from = r.Start
		}
		if to.After(r.End) {
			to = r.End
		}
		for _, gap := range subtractSpans(from, to, occupied) {
			gap.Inferred = true
			c := &r.Cards[index]
			c.Spans = append(c.Spans, gap)
			occupied = append(occupied, gap)
			if gap.Start.Before(c.Start) {
				c.Start = gap.Start
			}
			if gap.End.After(c.End) {
				c.End = gap.End
			}
			added = true
		}
	}
	// A single recorded app activation provides a better boundary than the
	// midpoint. Multiple apps, unavailable states and round trips stay unknown.
	previousSample := -1
	for i, p := range points {
		if p.State != "sampled" {
			continue
		}
		if previousSample >= 0 && i > previousSample+1 {
			a, b := points[previousSample], p
			middle := points[previousSample+1 : i]
			valid := neighborGap(a, b) && a.App != b.App && a.App != "" && b.App != ""
			for _, event := range middle {
				valid = valid && event.State == "active" && event.App == b.App
			}
			left, right := cardAt(a), cardAt(b)
			valid = valid && !(a.TaskID != "" && left < 0) && !(b.TaskID != "" && right < 0)
			if valid {
				boundary := middle[0].At
				if left >= 0 && right >= 0 {
					add(left, a.At, boundary)
					add(right, boundary, b.At)
				} else {
					mid := a.At.Add(b.At.Sub(a.At) / 2)
					add(left, a.At, minTime(mid, boundary))
					add(right, maxTime(mid, boundary), b.At)
				}
			}
		}
		previousSample = i
	}
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		gap := b.At.Sub(a.At)
		if !neighborGap(a, b) {
			continue
		}
		if a.State != "sampled" || b.State != "sampled" || a.App == "" || b.App == "" {
			continue
		}
		left, right := cardAt(a), cardAt(b)
		if (a.TaskID != "" && left < 0) || (b.TaskID != "" && right < 0) {
			continue // Unsupported labels, including low confidence, are boundaries.
		}
		if a.App != b.App && (a.State != "sampled" || b.State != "sampled") {
			continue // Legacy app transitions may have lost activation boundaries.
		}
		if left < 0 || right < 0 {
			// With an unidentified picture, retain its half as unknown. Missing,
			// idle and unsupported historical endpoints do not qualify as pictures.
			if a.State != "sampled" || b.State != "sampled" || a.App != b.App {
				continue
			}
		}
		if left == right && left >= 0 {
			add(left, a.At, b.At)
		} else {
			middle := a.At.Add(gap / 2)
			add(left, a.At, middle)
			add(right, middle, b.At)
		}
	}
	if !added {
		return input
	}
	r.EstimationVersion = estimationVersion
	for i := range r.Cards {
		c := &r.Cards[i]
		sort.SliceStable(c.Spans, func(i, j int) bool { return c.Spans[i].Start.Before(c.Spans[j].Start) })
		var all, inferred []span
		for _, s := range c.Spans {
			all = append(all, span{s.Start, s.End})
			if s.Inferred {
				inferred = append(inferred, span{s.Start, s.End})
			}
		}
		c.EstimatedSeconds = covered(all)
		c.InferredSeconds = covered(inferred)
		if c.InferredSeconds > 0 {
			c.DurationMethod = estimationVersion
		}
	}
	r.UnknownSpans = subtractSpans(r.Start, r.End, occupied)
	r.UnknownSeconds = 0
	for _, s := range r.UnknownSpans {
		r.UnknownSeconds += int64(s.End.Sub(s.Start) / time.Second)
	}
	sort.SliceStable(r.Cards, func(i, j int) bool { return r.Cards[i].Start.Before(r.Cards[j].Start) })
	return r
}

func neighborGap(a, b TaskObservation) bool {
	gap := b.At.Sub(a.At)
	return a.MaxGapSeconds >= 30 && b.MaxGapSeconds >= 30 && a.MaxGapSeconds <= 600 && b.MaxGapSeconds <= 600 && gap > 0 && gap <= time.Duration(min(a.MaxGapSeconds, b.MaxGapSeconds, 300))*time.Second
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Conflicting labels at one timestamp must not create artificial continuity.
func uniqueObservations(input []TaskObservation) []TaskObservation {
	points := append([]TaskObservation(nil), input...)
	sort.SliceStable(points, func(i, j int) bool { return points[i].At.Before(points[j].At) })
	var result []TaskObservation
	for _, p := range points {
		if len(result) == 0 || !result[len(result)-1].At.Equal(p.At) {
			result = append(result, p)
			continue
		}
		last := &result[len(result)-1]
		if p.State == "active" && (last.State == "sampled" || last.State == "") && p.App == last.App {
			continue
		}
		if last.State == "active" && p.State == "sampled" && p.App == last.App {
			*last = p
			continue
		}
		if last.TaskID != p.TaskID || last.App != p.App || last.State != p.State {
			last.TaskID, last.State = "", "conflict"
		}
		last.MaxGapSeconds = min(last.MaxGapSeconds, p.MaxGapSeconds)
	}
	return result
}

func subtractSpans(start, end time.Time, occupied []TimeSpan) []TimeSpan {
	if !end.After(start) {
		return nil
	}
	parts := append([]TimeSpan(nil), occupied...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].Start.Before(parts[j].Start) })
	cursor := start
	var result []TimeSpan
	for _, p := range parts {
		if !p.End.After(cursor) || !p.End.After(p.Start) || !p.Start.Before(end) {
			continue
		}
		if p.Start.After(cursor) {
			result = append(result, TimeSpan{Start: cursor, End: p.Start})
		}
		if p.End.After(cursor) {
			cursor = p.End
		}
		if !cursor.Before(end) {
			return result
		}
	}
	if cursor.Before(end) {
		result = append(result, TimeSpan{Start: cursor, End: end})
	}
	return result
}
