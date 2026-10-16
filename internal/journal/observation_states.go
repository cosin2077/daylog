package journal

// Older task observations omitted availability states and app activations.
// Recover those facts from the archived batch, without loading any image or
// rewriting history. Without the archive, old gaps stay unknown.
func (s *Store) observationStates(r Report) Report {
	if r.SchemaVersion != 2 || r.AppSource == "" || len(r.TaskObservations) == 0 || r.EstimationVersion == estimationVersion {
		return r
	}
	missing := false
	for _, p := range r.TaskObservations {
		missing = missing || p.State == ""
	}
	if !missing || !validID.MatchString(r.BatchID) {
		return r
	}
	var b Batch
	if ReadJSON(s.archivePath(r.BatchID), &b) != nil || b.ID != r.BatchID || b.AppSource != r.AppSource || validateObservations(b) != nil {
		return r
	}
	points := append([]TaskObservation(nil), r.TaskObservations...)
	for _, o := range b.Observations {
		for i := range points {
			if points[i].State == "" && points[i].At.Equal(o.At) && o.State == "sampled" && points[i].App == o.App {
				points[i].State = o.State
			}
		}
		if o.State != "sampled" {
			points = append(points, TaskObservation{At: o.At, App: o.App, State: o.State, MaxGapSeconds: b.MaxGapSeconds})
		}
	}
	r.TaskObservations = points
	return r
}
