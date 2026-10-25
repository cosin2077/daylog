package journal

import "time"

// Older task observations omitted availability states and app activations.
// Recover those facts from the archived batch, without loading any image or
// rewriting history. Without the archive, old gaps stay unknown.
func (s *Store) observationStates(r Report) Report {
	if r.SchemaVersion != 2 || r.AppSource == "" || len(r.TaskObservations) == 0 || r.EstimationVersion == estimationVersion {
		return r
	}
	missing := false
	for _, p := range r.TaskObservations {
		missing = missing || p.State == "" || (p.State == "sampled" && p.TaskID == "")
	}
	if (!missing && r.EstimationVersion == "") || !validID.MatchString(r.BatchID) {
		return r
	}
	var b Batch
	if ReadJSON(s.archivePath(r.BatchID), &b) != nil || b.ID != r.BatchID || b.AppSource != r.AppSource || validateObservations(b) != nil {
		return r
	}
	points := append([]TaskObservation(nil), r.TaskObservations...)
	labels := map[string]string{}
	activities := map[string]string{}
	selected := map[string]bool{}
	for _, f := range b.Frames {
		selected[f.ID] = true
	}
	for _, card := range r.Cards {
		for _, id := range card.Evidence {
			labels[id] = card.TaskID
			activities[id] = card.ActivityID
		}
	}
	// Recover original keyframe bounds before removing obsolete inferred spans.
	r.Cards = append([]Card(nil), r.Cards...)
	for i := range r.Cards {
		card := &r.Cards[i]
		if card.InferredSeconds == 0 {
			continue
		}
		var start, end time.Time
		for _, frame := range b.Frames {
			for _, id := range card.Evidence {
				if frame.ID == id {
					if start.IsZero() || frame.At.Before(start) {
						start = frame.At
					}
					if end.IsZero() || !frame.At.Before(end) {
						end = frame.At.Add(time.Nanosecond)
					}
				}
			}
		}
		for _, span := range card.Spans {
			if !span.Inferred {
				if start.IsZero() || span.Start.Before(start) {
					start = span.Start
				}
				if span.End.After(end) {
					end = span.End
				}
			}
		}
		if !start.IsZero() {
			card.Start, card.End = start, end
		}
	}
	for _, o := range b.Observations {
		for i := range points {
			if (points[i].State == "" || points[i].State == "sampled") && points[i].At.Equal(o.At) && o.State == "sampled" && points[i].App == o.App {
				points[i].State = o.State
				points[i].Unselected = o.FrameID != "" && !selected[o.FrameID]
				if points[i].ActivityID == "" {
					points[i].ActivityID = activities[o.FrameID]
				}
				if points[i].TaskID == "" {
					points[i].TaskID = labels[o.FrameID]
				}
			}
		}
		if o.State != "sampled" {
			points = append(points, TaskObservation{At: o.At, App: o.App, State: o.State, MaxGapSeconds: b.MaxGapSeconds})
		}
	}
	r.TaskObservations = points
	r.EstimationVersion = "" // Archived evidence resolves old ambiguous labels.
	return r
}
