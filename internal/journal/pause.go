package journal

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

type PauseState struct {
	Paused bool       `json:"paused"`
	Until  *time.Time `json:"until,omitempty"`
}

// A pause must survive an updater/service restart before the first capture.
func (s *Store) SavePause(paused bool, until *time.Time) error {
	if !paused {
		until = nil
	}
	return s.locked(func() error {
		return WriteJSON(filepath.Join(s.Root, "pause.json"), PauseState{Paused: paused, Until: until})
	})
}

func (r *Runner) restorePause(now time.Time) error {
	var state PauseState
	if err := ReadJSON(filepath.Join(r.Store.Root, "pause.json"), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errors.New("无法读取暂停状态；运行 daylog start 明确恢复记录，或检查 pause.json")
	}
	if state.Paused && (state.Until == nil || now.Before(*state.Until)) {
		r.paused = true
		r.status.Paused = true
		if state.Until != nil {
			r.pauseUntil = *state.Until
			r.status.PauseUntil = state.Until
		}
	}
	return nil
}
