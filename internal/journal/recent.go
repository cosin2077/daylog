package journal

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

func (s *Store) LatestActivityDate() (time.Time, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "reports"))
	if err != nil {
		return time.Time{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 8 {
			continue
		}
		if _, err := time.Parse("20060102", entry.Name()); err != nil {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.Root, "reports", entry.Name()))
		if err != nil {
			return time.Time{}, err
		}
		var latest time.Time
		for _, file := range files {
			if filepath.Ext(file.Name()) != ".json" {
				continue
			}
			var report Report
			if err := ReadJSON(filepath.Join(s.Root, "reports", entry.Name(), file.Name()), &report); err != nil {
				return time.Time{}, err
			}
			for _, card := range report.Cards {
				at := card.Start
				if card.End.After(card.Start) {
					at = card.End.Add(-time.Nanosecond)
				}
				if !card.End.Before(card.Start) && at.After(latest) {
					latest = at
				}
			}
		}
		if !latest.IsZero() {
			return latest.In(time.Local), nil
		}
	}
	return time.Time{}, nil
}
