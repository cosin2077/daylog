package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallPreparesPrivateDaemonLogWithoutTruncating(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions; Windows data uses inherited DACL")
	}
	for _, existing := range []bool{false, true} {
		root, home := t.TempDir(), t.TempDir()
		path := filepath.Join(root, "daemon.log")
		if existing {
			if err := os.WriteFile(path, []byte("fixture-log"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := Install(home, root, "/fixture/daylog", false); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("daemon log is not private: %v", err)
		}
		if existing {
			data, _ := os.ReadFile(path)
			if string(data) != "fixture-log" {
				t.Fatal("existing log was truncated")
			}
		}
	}
}
