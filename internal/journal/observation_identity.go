package journal

import "time"

// A goal can contain several actions. Prefer the persisted evidence assignment;
// ambiguous old observations stay unknown rather than choosing the first card.
func observationCard(cards []Card, p TaskObservation) int {
	if p.TaskID == "" {
		return -1
	}
	best := -1
	var distance time.Duration
	ambiguous := false
	for i, c := range cards {
		if c.TaskID != p.TaskID || c.Confidence == "low" {
			continue
		}
		if p.ActivityID != "" {
			if c.ActivityID == p.ActivityID {
				return i
			}
			continue
		}
		d := time.Duration(0)
		if p.At.Before(c.Start) {
			d = c.Start.Sub(p.At)
		}
		if p.At.After(c.End) {
			d = p.At.Sub(c.End)
		}
		if best < 0 || d < distance {
			best, distance, ambiguous = i, d, false
		} else if d == distance {
			ambiguous = true
		}
	}
	if p.ActivityID != "" || ambiguous {
		return -1
	}
	return best
}
