package journal

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ExportDay takes one metadata snapshot under the store lock. Integrations do
// not need to inspect screenshots or race a summary against a separate read.
func (s *Store) ExportDay(day time.Time) (PeriodReport, []Report, error) {
	var view PeriodReport
	var reports []Report
	err := s.locked(func() error {
		start, end, _ := PeriodBounds("day", day)
		var err error
		reports, err = s.Reports(start, end)
		if err != nil {
			return err
		}
		pending, err := s.Pending(0)
		if err != nil {
			return err
		}
		var draft Batch
		if err = ReadJSON(filepath.Join(s.Root, "draft.json"), &draft); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		view = buildPeriodRange("day", day, start, end, reports, pending, draft)
		return nil
	})
	return view, reports, err
}
