package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Model output has no timestamps. Only local observations determine duration.
type Interpretation struct {
	SchemaVersion int       `json:"schema_version"`
	BatchID       string    `json:"batch_id"`
	Segments      []Segment `json:"segments"`
}
type Segment struct {
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	Category      string   `json:"category"`
	Object        string   `json:"object"`
	Actions       []string `json:"actions"`
	Confidence    string   `json:"confidence"`
	TaskID        string   `json:"task_id"`
	Evidence      []string `json:"evidence"`
	Uncertainties []string `json:"uncertainties"`
}

// Accept presentation differences; never invent missing screenshot evidence.
func decodeInterpretation(b Batch, data []byte) (Interpretation, error) {
	var in Interpretation
	if len(data) > 1<<20 {
		return in, errors.New("analysis exceeds size limit")
	}
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, "```") {
		line, body, ok := strings.Cut(text, "\n")
		language := strings.TrimSpace(strings.TrimPrefix(line, "```"))
		if ok && (language == "" || strings.EqualFold(language, "json")) && strings.HasSuffix(body, "```") {
			text = strings.TrimSpace(strings.TrimSuffix(body, "```"))
		}
	}
	d := json.NewDecoder(strings.NewReader(text))
	// Unknown fields are ignored, including model-supplied times. Durations are
	// always derived from local observations, never from these ignored fields.
	if err := d.Decode(&in); err != nil {
		return in, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return in, errors.New("trailing analysis content")
	}
	if in.SchemaVersion == 0 {
		in.SchemaVersion = 2
	}
	if in.BatchID == "" {
		in.BatchID = b.ID
	}
	if in.SchemaVersion != 2 || in.BatchID != b.ID || in.Segments == nil || len(in.Segments) > 12 {
		return in, errors.New("invalid v2 batch or segment count")
	}
	for i := range in.Segments {
		g := &in.Segments[i]
		g.Title = strings.TrimSpace(g.Title)
		g.Summary = strings.TrimSpace(g.Summary)
		if g.Title == "" || g.Summary == "" {
			return in, errors.New("segment title and summary required")
		}
		g.Category = category(g.Category)
		g.Confidence = strings.ToLower(strings.TrimSpace(g.Confidence))
		if g.Confidence == "" {
			g.Confidence = "medium"
		}
		if g.Actions == nil {
			g.Actions = []string{}
		}
		if g.Uncertainties == nil {
			g.Uncertainties = []string{}
		}
		seen := map[string]bool{}
		evidence := []string{}
		for _, id := range g.Evidence {
			if !seen[id] {
				evidence = append(evidence, id)
				seen[id] = true
			}
		}
		g.Evidence = evidence
	}
	return in, nil
}

// Persist the exact bounded context given to the model, so commit validates the
// same allowlist even if another batch is processed in the meantime.
func (s *Store) PrepareAnalysis(id string) (Batch, error) {
	var b Batch
	err := s.locked(func() error {
		var err error
		b, err = s.Batch(id)
		if err != nil || b.SchemaVersion != 2 || b.ContextReady {
			return err
		}
		reports, err := s.Reports(b.Start.Add(-30*time.Minute), b.Start)
		if err != nil {
			return err
		}
		var candidates []TaskContext
		for _, r := range reports {
			for _, c := range r.Cards {
				if c.TaskID != "" && !c.End.After(b.Start) {
					candidates = append(candidates, TaskContext{c.TaskID, c.Title, c.Summary, c.End})
				}
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].End.After(candidates[j].End) })
		seen := map[string]bool{}
		remaining := 1500
		for _, c := range candidates {
			if seen[c.TaskID] {
				continue
			}
			seen[c.TaskID] = true
			c.Title = truncate(c.Title, 120)
			c.Summary = truncate(c.Summary, 240)
			size := utf8.RuneCountInString(c.Title + c.Summary)
			if size > remaining {
				break
			}
			remaining -= size
			b.Context = append(b.Context, c)
			if len(b.Context) == 5 {
				break
			}
		}
		b.ContextReady = true
		// Preparing an archived batch is read-only.
		if _, err = os.Stat(s.reportPath(id)); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return WriteJSON(filepath.Join(s.Root, "batches", id+".json"), b)
	})
	return b, err
}
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func inputMetadata(b Batch) []byte {
	// Exclude paths, retry errors and local operational details from model input.
	type inputFrame struct {
		ID  string     `json:"id"`
		App string     `json:"app,omitempty"`
		At  *time.Time `json:"at,omitempty"`
	}
	frames := make([]inputFrame, 0, len(b.Frames))
	for _, f := range b.Frames {
		app := f.App
		if b.SchemaVersion == 2 && b.AppSource == "" && !strings.HasPrefix(app, "window:") {
			app = ""
		}
		frame := inputFrame{ID: f.ID, App: app}
		if b.SchemaVersion != 2 {
			at := f.At
			frame.At = &at
		}
		frames = append(frames, frame)
	}
	data, _ := json.Marshal(struct {
		BatchID string        `json:"batch_id"`
		Start   time.Time     `json:"start"`
		End     time.Time     `json:"end"`
		Frames  []inputFrame  `json:"frames"`
		Context []TaskContext `json:"context,omitempty"`
	}{b.ID, b.Start, b.End, frames, b.Context})
	return data
}

func deriveReport(b Batch, in Interpretation, model string) (Report, error) {
	r := Report{AppSource: b.AppSource, SchemaVersion: 2, Revision: 1, PromptVersion: "activity-v2.2", Model: model, BatchID: b.ID, Start: b.Start, End: b.End, Cards: []Card{}}
	if b.SchemaVersion != 2 || in.SchemaVersion != 2 || in.BatchID != b.ID || in.Segments == nil || len(in.Segments) > 12 {
		return r, errors.New("invalid v2 batch or segment count")
	}
	if err := validateObservations(b); err != nil {
		return r, err
	}
	frames := map[string]Frame{}
	for _, f := range b.Frames {
		frames[f.ID] = f
	}
	allowedTasks := map[string]bool{}
	for _, c := range b.Context {
		allowedTasks[c.TaskID] = true
	}
	assigned := map[string]int{}
	for i, g := range in.Segments {
		g.Category = category(g.Category)
		if strings.TrimSpace(g.Title) == "" || utf8.RuneCountInString(g.Title) > 120 || utf8.RuneCountInString(g.Summary) > 1200 || utf8.RuneCountInString(g.Object) > 240 || g.Actions == nil || g.Uncertainties == nil || len(g.Actions) > 8 || len(g.Uncertainties) > 8 || len(g.Evidence) == 0 || len(g.Evidence) > len(b.Frames) {
			return r, errors.New("invalid segment bounds")
		}
		if g.Confidence != "high" && g.Confidence != "medium" && g.Confidence != "low" {
			return r, errors.New("invalid confidence")
		}
		for _, text := range append(append([]string{}, g.Actions...), g.Uncertainties...) {
			if utf8.RuneCountInString(text) > 240 {
				return r, errors.New("segment detail too long")
			}
		}
		if g.TaskID != "" && (!allowedTasks[g.TaskID] || g.Confidence == "low") {
			return r, errors.New("task link absent from context or unsupported")
		}
		card := Card{ActivityID: fmt.Sprintf("act_%s_%02d", b.ID, i), TaskID: g.TaskID, Title: g.Title, Summary: g.Summary, Category: g.Category, Object: g.Object, Actions: g.Actions, Confidence: g.Confidence, Uncertainties: g.Uncertainties, Evidence: g.Evidence, DurationMethod: "bounded_observation_v1"}
		if card.TaskID == "" {
			card.TaskID = fmt.Sprintf("task_%s_%02d", b.ID, i)
		}
		apps := map[string]bool{}
		for _, id := range g.Evidence {
			f, ok := frames[id]
			if !ok {
				return r, errors.New("unknown frame")
			}
			if _, ok = assigned[id]; ok {
				return r, errors.New("frame assigned more than once")
			}
			assigned[id] = i
			if card.Start.IsZero() || f.At.Before(card.Start) {
				card.Start = f.At
			}
			if !f.At.Before(card.End) {
				card.End = f.At.Add(time.Nanosecond)
			}
			if !apps[f.App] && (b.AppSource != "" || strings.HasPrefix(f.App, "window:")) {
				card.Apps = append(card.Apps, f.App)
				apps[f.App] = true
			}
		}
		if b.AppSource == "" {
			card.Uncertainties = append(card.Uncertainties, "旧版前台应用标识可能未反映真实切换，时长仅供参考")
		}
		r.Cards = append(r.Cards, card)
	}
	// Derive supported spans first. Bounded neighbor inference records its
	// assumptions separately after this pass; unavailable gaps remain unknown.
	labels, inferred := observationLabels(b, assigned)
	if b.AppSource != "" {
		for i, o := range b.Observations {
			// Preserve app activation as timing information. Without a picture
			// it does not identify a task; availability gaps remain distinct.
			point := TaskObservation{At: o.At, App: o.App, State: o.State, MaxGapSeconds: b.MaxGapSeconds}
			if label := labels[i]; label >= 0 && r.Cards[label].Confidence != "low" {
				point.TaskID = r.Cards[label].TaskID
			}
			r.TaskObservations = append(r.TaskObservations, point)
		}
	}
	var known []TimeSpan
	for j := 1; j < len(b.Observations); j++ {
		prev, next := b.Observations[j-1], b.Observations[j]
		a, z := labels[j-1], labels[j]
		aok, zok := a >= 0, z >= 0
		gap := next.At.Sub(prev.At)
		if !aok || !zok || a != z || prev.State != "sampled" || next.State != "sampled" || prev.App != next.App || gap <= 0 || gap > time.Duration(b.MaxGapSeconds)*time.Second || r.Cards[a].Confidence == "low" {
			continue
		}
		part := TimeSpan{Start: prev.At, End: next.At, Interpolated: inferred[j-1] || inferred[j]}
		r.Cards[a].Spans = appendSpan(r.Cards[a].Spans, part)
		known = appendSpan(known, part)
		if next.At.After(r.Cards[a].End) {
			r.Cards[a].End = next.At
		}
	}
	for i := range r.Cards {
		for _, p := range r.Cards[i].Spans {
			r.Cards[i].EstimatedSeconds += int64(p.End.Sub(p.Start) / time.Second)
		}
	}
	cursor := b.Start
	for _, p := range known {
		if p.Start.After(cursor) {
			r.UnknownSpans = append(r.UnknownSpans, TimeSpan{Start: cursor, End: p.Start})
		}
		cursor = p.End
	}
	if cursor.Before(b.End) {
		r.UnknownSpans = append(r.UnknownSpans, TimeSpan{Start: cursor, End: b.End})
	}
	for _, p := range r.UnknownSpans {
		r.UnknownSeconds += int64(p.End.Sub(p.Start) / time.Second)
	}
	sort.SliceStable(r.Cards, func(i, j int) bool { return r.Cards[i].Start.Before(r.Cards[j].Start) })
	hashData, _ := json.Marshal(struct {
		Metadata      json.RawMessage
		Observations  []Observation
		MaxGap        int
		Prompt, Model string
		Estimation    string
	}{inputMetadata(b), b.Observations, b.MaxGapSeconds, "activity-v2.2", model, estimationVersion})
	hash := sha256.Sum256(hashData)
	r.InputHash = hex.EncodeToString(hash[:])
	r = estimateReport(r)
	r.EstimationVersion = estimationVersion
	return r, nil
}
func appendSpan(spans []TimeSpan, p TimeSpan) []TimeSpan {
	if len(spans) > 0 && spans[len(spans)-1].End.Equal(p.Start) && spans[len(spans)-1].Interpolated == p.Interpolated && spans[len(spans)-1].Inferred == p.Inferred {
		spans[len(spans)-1].End = p.End
		return spans
	}
	return append(spans, p)
}

func validateObservations(b Batch) error {
	if b.SupportGapSeconds < 0 || b.SupportGapSeconds > 600 {
		return errors.New("invalid support gap")
	}
	if b.MaxGapSeconds < 30 || b.MaxGapSeconds > 600 || len(b.Observations) > 2048 {
		return errors.New("invalid observation bounds")
	}
	prev := b.Start
	for _, o := range b.Observations {
		if o.At.Before(prev) || o.At.Before(b.Start) || o.At.After(b.End) {
			return errors.New("invalid observation order")
		}
		prev = o.At
	}
	return nil
}

func (s *Store) CommitAnalysis(id string, data []byte, source, model string) error {
	b, err := s.Batch(id)
	if err != nil {
		return err
	}
	if b.SchemaVersion != 2 {
		r, err := parseAgentReport(data)
		if err != nil {
			return err
		}
		if r.BatchID != id {
			return errors.New("wrong batch")
		}
		return s.Commit(r, source)
	}
	in, err := decodeInterpretation(b, data)
	if err != nil {
		return err
	}
	return s.locked(func() error {
		b, err := s.Batch(id)
		if err != nil {
			return err
		}
		if _, err = os.Stat(s.reportPath(id)); err == nil {
			return errors.New("batch already analyzed")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		r, err := deriveReport(b, in, model)
		if err != nil {
			return err
		}
		r.Source = source
		r.CreatedAt = time.Now().UTC()
		path := s.reportPath(id)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err = WriteJSON(path, r); err != nil {
			return err
		}
		return s.archiveBatch(id)
	})
}

func checkFrameDigest(f Frame, data []byte) error {
	if f.SHA256 == "" {
		return nil
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != f.SHA256 {
		return errors.New("screenshot changed since capture")
	}
	return nil
}

// Infer only between two matching keyframes in an uninterrupted sampled run.
// This is an explicit estimate, never extrapolation into missing/locked time.
func observationLabels(b Batch, assigned map[string]int) ([]int, []bool) {
	labels := make([]int, len(b.Observations))
	inferred := make([]bool, len(labels))
	anchor := -1
	supportGap := b.SupportGapSeconds
	if supportGap == 0 {
		supportGap = b.MaxGapSeconds
	}
	for i, o := range b.Observations {
		labels[i] = -1
		if label, ok := assigned[o.FrameID]; ok && o.State == "sampled" {
			labels[i] = label
		}
		if i == 0 || o.State != "sampled" || b.Observations[i-1].State != "sampled" || b.Observations[i-1].App != o.App || o.At.Sub(b.Observations[i-1].At) > time.Duration(b.MaxGapSeconds)*time.Second {
			anchor = -1
		}
		if labels[i] < 0 {
			continue
		}
		if anchor >= 0 && labels[anchor] == labels[i] && o.At.Sub(b.Observations[anchor].At) <= time.Duration(supportGap)*time.Second {
			for j := anchor + 1; j < i; j++ {
				labels[j] = labels[i]
				inferred[j] = true
			}
		}
		anchor = i
	}
	return labels, inferred
}
