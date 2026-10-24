package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Transition records contain no window titles or keystrokes. Sample records
// reference local images; repeated samples preserve time without saving JPEGs.
func (r *Runner) observe(o Observation) error {
	if o.MaxGapSeconds == 0 {
		o.MaxGapSeconds = r.observationGap
		if o.MaxGapSeconds == 0 {
			o.MaxGapSeconds = r.Config.MaxGapSeconds
		}
	}
	if len(r.draft.Frames) > 0 {
		// A wake after hours of sleep must not create a many-hour batch: the
		// dated event log records the real wake, the batch closes at its bound.
		bounded := o
		limit := r.draft.Start.Add(time.Duration(r.Config.BatchSeconds) * time.Second)
		if bounded.At.After(limit) {
			bounded.At = limit
		}
		if len(r.draft.Observations) < 2048 {
			r.draft.Observations = append(r.draft.Observations, bounded)
		}
		if bounded.At.After(r.draft.End) {
			r.draft.End = bounded.At
		}
		if err := r.Store.SaveDraft(r.draft); err != nil {
			return err
		}
	}
	return r.Store.locked(func() error {
		dir := filepath.Join(r.Store.Root, "observations")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(dir, o.At.Local().Format("2006-01-02")+".jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		return json.NewEncoder(f).Encode(o)
	})
}

func (r *Runner) noteState(now time.Time, state, app string) {
	if state == r.observedState && app == r.observedApp {
		return
	}
	if err := r.observe(Observation{At: now.UTC(), App: app, State: state}); err != nil {
		r.setError("observation_write_failed")
		return
	}
	r.observedState = state
	r.observedApp = app
}
