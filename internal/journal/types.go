package journal

import "time"

type Frame struct {
	ID   string    `json:"id"`
	At   time.Time `json:"at"`
	App  string    `json:"app"`
	File string    `json:"file"`
}

type Batch struct {
	ID          string     `json:"id"`
	Start       time.Time  `json:"start"`
	End         time.Time  `json:"end"`
	Frames      []Frame    `json:"frames"`
	Attempts    int        `json:"attempts"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
}

type Card struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Title    string    `json:"title"`
	Summary  string    `json:"summary"`
	Category string    `json:"category"`
	Evidence []string  `json:"evidence"`
}

type Report struct {
	BatchID   string    `json:"batch_id"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	Cards     []Card    `json:"cards"`
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
