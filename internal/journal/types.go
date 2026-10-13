package journal

import "time"

type Frame struct {
	SHA256 string    `json:"sha256,omitempty"`
	ID     string    `json:"id"`
	At     time.Time `json:"at"`
	App    string    `json:"app"`
	File   string    `json:"file"`
	Reason string    `json:"reason,omitempty"`
	Change float64   `json:"change,omitempty"`
}

type Observation struct {
	At      time.Time `json:"at"`
	App     string    `json:"app,omitempty"`
	FrameID string    `json:"frame_id,omitempty"`
	State   string    `json:"state"`
}

type TimeSpan struct {
	Interpolated bool      `json:"interpolated,omitempty"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
}

type TaskContext struct {
	TaskID  string    `json:"task_id"`
	Title   string    `json:"title"`
	Summary string    `json:"summary"`
	End     time.Time `json:"end"`
}

type Batch struct {
	SupportGapSeconds int           `json:"support_gap_seconds,omitempty"`
	SchemaVersion     int           `json:"schema_version,omitempty"`
	Observations      []Observation `json:"observations,omitempty"`
	MaxGapSeconds     int           `json:"max_gap_seconds,omitempty"`
	OmittedFrames     int           `json:"omitted_frames,omitempty"`
	Context           []TaskContext `json:"context,omitempty"`
	ContextReady      bool          `json:"context_ready,omitempty"`
	ID                string        `json:"id"`
	Start             time.Time     `json:"start"`
	End               time.Time     `json:"end"`
	Frames            []Frame       `json:"frames"`
	Attempts          int           `json:"attempts"`
	LastAttempt       *time.Time    `json:"last_attempt,omitempty"`
	LastError         string        `json:"last_error,omitempty"`
}

type Card struct {
	ActivityID       string     `json:"activity_id,omitempty"`
	TaskID           string     `json:"task_id,omitempty"`
	Apps             []string   `json:"apps,omitempty"`
	Actions          []string   `json:"actions,omitempty"`
	Object           string     `json:"object,omitempty"`
	Confidence       string     `json:"confidence,omitempty"`
	Uncertainties    []string   `json:"uncertainties,omitempty"`
	Spans            []TimeSpan `json:"spans,omitempty"`
	EstimatedSeconds int64      `json:"estimated_seconds,omitempty"`
	DurationMethod   string     `json:"duration_method,omitempty"`
	Start            time.Time  `json:"start"`
	End              time.Time  `json:"end"`
	Title            string     `json:"title"`
	Summary          string     `json:"summary"`
	Category         string     `json:"category"`
	Evidence         []string   `json:"evidence"`
}

type Report struct {
	SchemaVersion  int        `json:"schema_version,omitempty"`
	Revision       int        `json:"revision,omitempty"`
	PromptVersion  string     `json:"prompt_version,omitempty"`
	Model          string     `json:"model,omitempty"`
	Start          time.Time  `json:"start,omitempty"`
	End            time.Time  `json:"end,omitempty"`
	UnknownSeconds int64      `json:"unknown_seconds,omitempty"`
	UnknownSpans   []TimeSpan `json:"unknown_spans,omitempty"`
	InputHash      string     `json:"input_hash,omitempty"`
	BatchID        string     `json:"batch_id"`
	Source         string     `json:"source"`
	CreatedAt      time.Time  `json:"created_at"`
	Cards          []Card     `json:"cards"`
}

type Status struct {
	PID              int        `json:"pid"`
	UpdatedAt        time.Time  `json:"updated_at"`
	StartedAt        time.Time  `json:"started_at"`
	LastCapture      *time.Time `json:"last_capture,omitempty"`
	LastSaved        *time.Time `json:"last_saved,omitempty"`
	Captures         int64      `json:"captures"`
	Saved            int64      `json:"saved_frames"`
	SkippedIdle      int64      `json:"skipped_idle"`
	SkippedBlocked   int64      `json:"skipped_blocked"`
	SkippedUnchanged int64      `json:"skipped_unchanged"`
	Paused           bool       `json:"paused"`
	PauseUntil       *time.Time `json:"pause_until,omitempty"`
	Pending          int        `json:"pending_batches"`
	StorageBytes     int64      `json:"storage_bytes"`
	Error            string     `json:"error,omitempty"`
}
