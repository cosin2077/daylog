package journal

import (
	"sort"
	"time"
)

type ActivityPart struct {
	BatchID    string    `json:"batch_id"`
	ActivityID string    `json:"activity_id,omitempty"`
	Source     string    `json:"source"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
}

type RhythmBlock struct {
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	Category        string    `json:"category"`
	InferredSeconds int64     `json:"inferred_seconds"`
}

// Only touching entries with the same stable task and category can coalesce.
// Every constituent keeps its original explanation and provenance in parts.
func mergeActivities(input []Activity) []Activity {
	var result []Activity
	for _, a := range input {
		if len(result) == 0 {
			result = append(result, a)
			continue
		}
		last := &result[len(result)-1]
		if a.TaskID == "" || a.TaskID != last.TaskID || a.Category != last.Category || a.Start.After(last.End) || !sameDay(last.Start, a.Start, last.Start.Location()) {
			result = append(result, a)
			continue
		}
		part := func(v Activity) ActivityPart {
			return ActivityPart{v.BatchID, v.ActivityID, v.Source, v.Title, v.Summary, v.Start, v.End}
		}
		if len(last.Parts) == 0 {
			last.Parts = []ActivityPart{part(*last)}
		}
		last.Parts = append(last.Parts, part(a))
		if a.End.After(last.End) {
			last.End = a.End
		}
		last.Spans = append(append([]TimeSpan(nil), last.Spans...), a.Spans...)
		last.Apps = uniqueStrings(last.Apps, a.Apps)
		last.Evidence = uniqueStrings(last.Evidence, a.Evidence)
		last.Uncertainties = uniqueStrings(last.Uncertainties, a.Uncertainties)
		var all, inferred, interpolated []span
		for _, p := range last.Spans {
			all = append(all, span{p.Start, p.End})
			if p.Inferred {
				inferred = append(inferred, span{p.Start, p.End})
			}
			if p.Interpolated {
				interpolated = append(interpolated, span{p.Start, p.End})
			}
		}
		last.EstimatedSeconds, last.InferredSeconds, last.InterpolatedSeconds = covered(all), covered(inferred), covered(interpolated)
	}
	return result
}

func uniqueStrings(a, b []string) []string {
	result := append([]string(nil), a...)
	seen := map[string]bool{}
	for _, value := range a {
		seen[value] = true
	}
	for _, value := range b {
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result
}

// Merge only adjacent time of the same category, never paint over a real gap.
// This is a distribution layer; continuous task evidence is a separate layer.
func buildRhythm(activities []Activity) []RhythmBlock {
	var input []RhythmBlock
	for _, a := range activities {
		for _, p := range a.Spans {
			inferred := int64(0)
			if p.Inferred {
				inferred = int64(p.End.Sub(p.Start) / time.Second)
			}
			input = append(input, RhythmBlock{p.Start, p.End, a.Category, inferred})
		}
	}
	sort.SliceStable(input, func(i, j int) bool { return input[i].Start.Before(input[j].Start) })
	var blocks []RhythmBlock
	for _, p := range input {
		if len(blocks) > 0 {
			last := &blocks[len(blocks)-1]
			if last.Category == p.Category && last.End.Equal(p.Start) && sameDay(last.Start, p.Start, last.Start.Location()) {
				last.End = p.End
				last.InferredSeconds += p.InferredSeconds
				continue
			}
		}
		blocks = append(blocks, p)
	}
	return blocks
}
