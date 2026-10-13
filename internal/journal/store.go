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
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

var validID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z$`)
var ErrStorageFull = errors.New("raw storage limit reached; analyze pending batches or increase max_storage_bytes")
var ErrQueueFull = errors.New("pending queue limit reached; analyze pending batches before recording more")

type Store struct{ Root string }

func Open(root string) (*Store, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("data directory must be a private directory (0700), not a symlink")
	}
	for _, sub := range []string{"frames", "batches", "archive", "reports", "usage"} {
		if err = os.MkdirAll(filepath.Join(root, sub), 0700); err != nil {
			return nil, err
		}
		info, err := os.Lstat(filepath.Join(root, sub))
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("storage directories must not be symlinks")
		}
	}
	return &Store{Root: root}, nil
}

func ID(t time.Time) string { return t.UTC().Format("20060102T150405.000000000Z") }

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".daylog-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func ReadJSON(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 2<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return errors.New("JSON must contain exactly one object")
	}
	return nil
}

func (s *Store) locked(fn func() error) error {
	f, err := os.OpenFile(filepath.Join(s.Root, "store.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			return errors.New("storage is busy")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (s *Store) Init() error {
	return s.locked(func() error {
		path := filepath.Join(s.Root, "config.json")
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return WriteJSON(path, DefaultConfig())
	})
}

func (s *Store) FramePath(f Frame) (string, error) {
	parts := strings.Split(f.File, "/")
	valid := len(parts) == 2 && parts[0] == "frames" && parts[1] == f.ID+".jpg"
	if len(parts) == 3 && parts[0] == "frames" && parts[2] == f.ID+".jpg" {
		day, err := time.Parse("2006-01-02", parts[1])
		valid = err == nil && day.Format("2006-01-02") == parts[1]
	}
	if !validID.MatchString(f.ID) || !valid {
		return "", errors.New("invalid frame reference")
	}
	for _, dir := range []string{filepath.Join(s.Root, "frames"), filepath.Dir(filepath.Join(s.Root, filepath.FromSlash(f.File)))} {
		info, err := os.Lstat(dir)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", errors.New("frame directory must not be a symlink")
		}
	}
	p := filepath.Join(s.Root, filepath.FromSlash(f.File))
	info, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return "", errors.New("frame must be a regular JPEG <=2 MiB")
	}
	return p, nil
}

func (s *Store) SaveFrame(now time.Time, app string, data []byte, c Config) (Frame, error) {
	digest := sha256.Sum256(data)
	f := Frame{ID: ID(now), At: now.UTC(), App: app, SHA256: hex.EncodeToString(digest[:])}
	f.File = "frames/" + now.Local().Format("2006-01-02") + "/" + f.ID + ".jpg"
	if len(data) == 0 || len(data) > 2<<20 {
		return f, errors.New("capture must be <=2 MiB")
	}
	err := s.locked(func() error {
		base, err := os.Lstat(filepath.Join(s.Root, "frames"))
		if err != nil {
			return err
		}
		if !base.IsDir() {
			return errors.New("frame directory must not be a symlink")
		}
		if _, err := os.Lstat(filepath.Join(s.Root, "frames", f.ID+".jpg")); err == nil {
			return errors.New("capture timestamp already exists in legacy storage")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		dir := filepath.Dir(filepath.Join(s.Root, filepath.FromSlash(f.File)))
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("frame directory must not be a symlink")
		}
		if _, err := os.Stat(filepath.Join(s.Root, filepath.FromSlash(f.File))); err == nil {
			return errors.New("capture timestamp already exists")
		}
		size, err := s.RawBytes()
		if err != nil {
			return err
		}
		if size+int64(len(data)) > c.MaxStorageBytes {
			return ErrStorageFull
		}
		return writeAtomic(filepath.Join(s.Root, filepath.FromSlash(f.File)), data)
	})
	return f, err
}

func (s *Store) RawBytes() (int64, error) {
	files, err := s.frameFiles()
	if err != nil {
		return 0, err
	}
	var size int64
	for _, path := range files {
		info, err := os.Lstat(path)
		if err != nil {
			return 0, err
		}
		size += info.Size()
	}
	return size, nil
}

// Walk dated folders and the legacy flat layout without following symlinks.
func (s *Store) frameFiles() ([]string, error) {
	var files []string
	err := filepath.WalkDir(filepath.Join(s.Root, "frames"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in frame storage")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("non-regular file in frame storage")
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

func (s *Store) SaveDraft(b Batch) error {
	if !validID.MatchString(b.ID) || len(b.Frames) == 0 || len(b.Frames) > 512 || !b.End.After(b.Start) {
		return errors.New("invalid draft")
	}
	return s.locked(func() error { return WriteJSON(filepath.Join(s.Root, "draft.json"), b) })
}

func (s *Store) SealDraft(b Batch, c Config) error {
	return s.locked(func() error {
		if !validID.MatchString(b.ID) || len(b.Frames) == 0 || !b.End.After(b.Start) {
			return errors.New("invalid draft")
		}
		path := filepath.Join(s.Root, "batches", b.ID+".json")
		// A crash after publishing the batch but before removing draft.json
		// must not reset attempt counters or resurrect a completed batch.
		for _, existing := range []string{path, s.reportPath(b.ID), s.archivePath(b.ID)} {
			if _, err := os.Stat(existing); err == nil {
				return os.Remove(filepath.Join(s.Root, "draft.json"))
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		pending, err := s.Pending(0)
		if err != nil {
			return err
		}
		if len(pending) >= c.MaxPendingBatches {
			return ErrQueueFull
		}
		selected := SelectFrames(b.Frames, c.MaxImages)
		if b.SchemaVersion == 2 {
			if err := validateObservations(b); err != nil {
				return err
			}
			selected = SelectKeyFrames(b.Frames, c.TargetImages, c.MaxImages)
			b.OmittedFrames = len(b.Frames) - len(selected)
		}
		b.Frames = selected
		if err = WriteJSON(path, b); err != nil {
			return err
		}
		return os.Remove(filepath.Join(s.Root, "draft.json"))
	})
}

func (s *Store) Batch(id string) (Batch, error) {
	var b Batch
	if !validID.MatchString(id) {
		return b, errors.New("invalid batch ID")
	}
	err := ReadJSON(filepath.Join(s.Root, "batches", id+".json"), &b)
	if errors.Is(err, os.ErrNotExist) {
		err = ReadJSON(s.archivePath(id), &b)
	}
	if err == nil && (b.ID != id || len(b.Frames) > 12 || !b.End.After(b.Start)) {
		err = errors.New("invalid batch manifest")
	}
	return b, err
}

func (s *Store) Pending(limit int) ([]Batch, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "batches"))
	if err != nil {
		return nil, err
	}
	out := []Batch{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validID.MatchString(id) {
			return nil, errors.New("invalid queue filename")
		}
		if _, err = os.Stat(s.reportPath(id)); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		b, err := s.Batch(id)
		if err != nil {
			return nil, fmt.Errorf("invalid batch manifest: %w", err)
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func ValidateReport(b Batch, r Report) error {
	if r.BatchID != b.ID || len(r.Cards) == 0 || len(r.Cards) > 8 {
		return errors.New("report requires matching batch_id and 1..8 cards")
	}
	allowed := map[string]time.Time{}
	for _, f := range b.Frames {
		allowed[f.ID] = f.At
	}
	last := b.Start
	for _, card := range r.Cards {
		if card.Start.Before(b.Start) || card.End.After(b.End) || !card.End.After(card.Start) || card.Start.Before(last) {
			return errors.New("cards must be ordered, non-overlapping and within the batch window")
		}
		if strings.TrimSpace(card.Title) == "" || utf8.RuneCountInString(card.Title) > 120 || utf8.RuneCountInString(card.Summary) > 1200 || utf8.RuneCountInString(card.Category) > 60 {
			return errors.New("invalid card text lengths")
		}
		if len(card.Evidence) == 0 || len(card.Evidence) > len(b.Frames) {
			return errors.New("each card needs bounded screenshot evidence")
		}
		seen := map[string]bool{}
		for _, id := range card.Evidence {
			at, ok := allowed[id]
			if !ok || seen[id] || at.Before(card.Start) || at.After(card.End) {
				return errors.New("unknown or duplicate evidence ID")
			}
			seen[id] = true
		}
		last = card.End
	}
	return nil
}

func (s *Store) Commit(r Report, source string) error {
	return s.locked(func() error {
		b, err := s.Batch(r.BatchID)
		if err != nil {
			return err
		}
		if b.SchemaVersion == 2 || r.SchemaVersion == 2 {
			return errors.New("v2 batches require evidence-only interpretation via CommitAnalysis")
		}
		if err = ValidateReport(b, r); err != nil {
			return err
		}
		path := s.reportPath(b.ID)
		if _, err = os.Stat(path); err == nil {
			return errors.New("batch already has a report; refusing overwrite")
		}
		// Legacy output cannot claim v2-derived metadata or inject task context.
		for i, c := range r.Cards {
			r.Cards[i] = Card{Start: c.Start, End: c.End, Title: c.Title, Summary: c.Summary, Category: c.Category, Evidence: c.Evidence}
		}
		r = Report{BatchID: r.BatchID, Cards: r.Cards}
		r.Source = source
		r.CreatedAt = time.Now().UTC()
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err = WriteJSON(path, r); err != nil {
			return err
		}
		return s.archiveBatch(b.ID)
	})
}

func (s *Store) Reports(start, end time.Time) ([]Report, error) {
	out := []Report{}
	for _, day := range daysBetween(start.Add(-time.Hour), end) {
		dir := filepath.Join(s.Root, "reports", day)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			var r Report
			if err = ReadJSON(filepath.Join(dir, entry.Name()), &r); err != nil {
				return nil, err
			}
			cards := []Card{}
			for _, card := range r.Cards {
				if card.Start.Before(end) && card.End.After(start) {
					cards = append(cards, card)
				}
			}
			if len(cards) > 0 || (r.SchemaVersion == 2 && r.Start.Before(end) && r.End.After(start)) {
				r.Cards = cards
				out = append(out, r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Start, out[j].Start
		if len(out[i].Cards) > 0 {
			a = out[i].Cards[0].Start
		}
		if len(out[j].Cards) > 0 {
			b = out[j].Cards[0].Start
		}
		return a.Before(b)
	})
	return out, nil
}

func (s *Store) reportPath(id string) string {
	return filepath.Join(s.Root, "reports", id[:8], id+".json")
}
func (s *Store) archivePath(id string) string {
	return filepath.Join(s.Root, "archive", id[:8], id+".json")
}
func (s *Store) archiveBatch(id string) error {
	path := s.archivePath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	err := os.Rename(filepath.Join(s.Root, "batches", id+".json"), path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func daysBetween(start, end time.Time) []string {
	out := []string{}
	day := start.UTC()
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	for day.Before(end) {
		out = append(out, day.Format("20060102"))
		day = day.AddDate(0, 0, 1)
	}
	return out
}

// Pending and live draft evidence is protected. Unselected draft images become
// orphans and are reclaimed after sealing, keeping the raw cap enforceable.
func (s *Store) Cleanup(c Config, now time.Time) error {
	return s.locked(func() error {
		protected := map[string]bool{}
		pending, err := s.Pending(0)
		if err != nil {
			return err
		}
		for _, b := range pending {
			for _, f := range b.Frames {
				protected[f.ID] = true
			}
		}
		var draft Batch
		if err = ReadJSON(filepath.Join(s.Root, "draft.json"), &draft); err == nil {
			for _, f := range draft.Frames {
				protected[f.ID] = true
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		files, err := s.frameFiles()
		if err != nil {
			return err
		}
		cutoff := now.AddDate(0, 0, -c.RawRetentionDays)
		owned := map[string]bool{}
		batches, err := os.ReadDir(filepath.Join(s.Root, "batches"))
		if err != nil {
			return err
		}
		for _, entry := range batches {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			b, readErr := s.Batch(strings.TrimSuffix(entry.Name(), ".json"))
			if readErr != nil {
				return readErr
			}
			for _, f := range b.Frames {
				owned[f.ID] = true
			}
			if _, err = os.Stat(s.reportPath(b.ID)); err == nil {
				if err = s.archiveBatch(b.ID); err != nil {
					return err
				}
			}
		}
		for _, day := range daysBetween(cutoff.AddDate(0, 0, -1), now.AddDate(0, 0, 1)) {
			dir := filepath.Join(s.Root, "archive", day)
			items, err := os.ReadDir(dir)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			for _, item := range items {
				if !strings.HasSuffix(item.Name(), ".json") {
					continue
				}
				var b Batch
				if err = ReadJSON(filepath.Join(dir, item.Name()), &b); err != nil {
					return err
				}
				for _, f := range b.Frames {
					owned[f.ID] = true
				}
			}
		}
		for _, path := range files {
			id := strings.TrimSuffix(filepath.Base(path), ".jpg")
			if protected[id] {
				continue
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			// Keep processed evidence for the configured retention; remove only
			// abandoned/unselected frames immediately, after a crash grace period.
			if info.ModTime().Before(cutoff) || (!owned[id] && now.Sub(info.ModTime()) > time.Minute) {
				if err = os.Remove(path); err != nil {
					return err
				}
			}
		}
		// Only remove empty date folders. Pending evidence keeps its directory.
		dateDirs, err := os.ReadDir(filepath.Join(s.Root, "frames"))
		if err != nil {
			return err
		}
		for _, dir := range dateDirs {
			if !dir.IsDir() {
				continue
			}
			if _, err := time.Parse("2006-01-02", dir.Name()); err == nil {
				_ = os.Remove(filepath.Join(s.Root, "frames", dir.Name()))
			}
		}
		observationDir := filepath.Join(s.Root, "observations")
		entries, readErr := os.ReadDir(observationDir)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		observationCutoff := now.Local().AddDate(0, 0, -c.ReportRetentionDays).Format("2006-01-02")
		for _, entry := range entries {
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
				day := strings.TrimSuffix(entry.Name(), ".jsonl")
				if _, err := time.Parse("2006-01-02", day); err == nil && day < observationCutoff {
					if err = os.Remove(filepath.Join(observationDir, entry.Name())); err != nil {
						return err
					}
				}
			}
		}
		textCutoff := now.UTC().AddDate(0, 0, -c.ReportRetentionDays).Format("20060102")
		cachedHTML := filepath.Join(s.Root, "reports", "report.html")
		if info, err := os.Lstat(cachedHTML); err == nil && info.ModTime().Before(now.AddDate(0, 0, -c.ReportRetentionDays)) {
			if err = os.Remove(cachedHTML); err != nil {
				return err
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, sub := range []string{"reports", "archive"} {
			items, err := os.ReadDir(filepath.Join(s.Root, sub))
			if err != nil {
				return err
			}
			for _, item := range items {
				if item.IsDir() && len(item.Name()) == 8 && item.Name() < textCutoff {
					if err = os.RemoveAll(filepath.Join(s.Root, sub, item.Name())); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func (s *Store) ReserveAttempt(id string, c Config, now time.Time) (Batch, error) {
	return s.reserveAttempt(id, c, now, false)
}
func (s *Store) reserveAttempt(id string, c Config, now time.Time, withinFallback bool) (Batch, error) {
	var result Batch
	err := s.locked(func() error {
		b, err := s.Batch(id)
		if err != nil {
			return err
		}
		if _, err = os.Stat(s.reportPath(id)); err == nil {
			return errors.New("batch already analyzed")
		}
		if b.Attempts >= 3 {
			return errors.New("batch exhausted three API attempts; use agent analysis")
		}
		if !withinFallback && b.LastAttempt != nil && now.Sub(*b.LastAttempt) < 15*time.Minute {
			return errors.New("batch retry is cooling down")
		}
		day := now.Local().Format("2006-01-02")
		path := filepath.Join(s.Root, "usage", day+".json")
		var usage DailyUsage
		if err = ReadJSON(path, &usage); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if usage.Requests >= c.AI.MaxRequestsPerDay {
			return errors.New("daily API request budget reached")
		}
		if usage.SubmittedImages+len(b.Frames) > c.MaxImagesPerDay {
			return errors.New("daily image budget reached")
		}
		if usage.KnownUsageCalls+usage.UnknownUsageCalls < usage.Requests {
			usage.UnknownUsageCalls = usage.Requests - usage.KnownUsageCalls
		}
		usage.Requests++
		usage.SubmittedImages += len(b.Frames)
		usage.UnknownUsageCalls++
		if err = WriteJSON(path, usage); err != nil {
			return err
		}
		b.Attempts++
		at := now.UTC()
		b.LastAttempt = &at
		b.LastError = ""
		if err = WriteJSON(filepath.Join(s.Root, "batches", id+".json"), b); err != nil {
			return err
		}
		result = b
		return nil
	})
	return result, err
}

func (s *Store) Failed(id, kind string) error {
	return s.locked(func() error {
		b, err := s.Batch(id)
		if err != nil {
			return err
		}
		b.LastError = kind
		at := time.Now().UTC()
		b.LastAttempt = &at
		return WriteJSON(filepath.Join(s.Root, "batches", id+".json"), b)
	})
}
