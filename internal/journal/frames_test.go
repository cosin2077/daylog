package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFramesUseLocalDateAndReadLegacyLayout(t *testing.T) {
	oldLocation := time.Local
	time.Local = time.FixedZone("capture-zone", 8*3600)
	defer func() { time.Local = oldLocation }()
	s := testStore(t)
	data := imageData(t)
	at := time.Date(2026, 10, 12, 16, 30, 0, 0, time.UTC)
	f, err := s.SaveFrame(at, "fixture", data, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if f.File != "frames/2026-10-13/"+f.ID+".jpg" {
		t.Fatalf("wrong local date folder: %s", f.File)
	}
	path, err := s.FramePath(f)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Dir(path))
	if info.Mode().Perm() != 0700 {
		t.Fatal("date folder is not private")
	}
	legacy := filepath.Join(s.Root, "frames", f.ID+".jpg")
	if err = os.Rename(path, legacy); err != nil {
		t.Fatal(err)
	}
	f.File = "frames/" + f.ID + ".jpg"
	if got, err := s.FramePath(f); err != nil || got != legacy {
		t.Fatal("legacy evidence unreadable", err)
	}
	if _, err = s.SaveFrame(at, "fixture", data, DefaultConfig()); err == nil {
		t.Fatal("legacy frame ID duplicated")
	}
	second, err := s.SaveFrame(at.Add(24*time.Hour), "fixture", data, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.RawBytes(); err != nil || got != int64(2*len(data)) {
		t.Fatalf("dated/legacy accounting wrong: %d %v", got, err)
	}
	bad := second
	bad.File = "frames/../" + bad.ID + ".jpg"
	if _, err = s.FramePath(bad); err == nil {
		t.Fatal("path traversal accepted")
	}
	path, _ = s.FramePath(second)
	os.Remove(path)
	os.Remove(filepath.Dir(path))
	outside := t.TempDir()
	if err = os.Symlink(outside, filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FramePath(second); err == nil {
		t.Fatal("symlink date directory accepted")
	}
	if _, err = s.SaveFrame(at.Add(24*time.Hour+time.Minute), "fixture", data, DefaultConfig()); err == nil {
		t.Fatal("wrote through symlink directory")
	}
}
