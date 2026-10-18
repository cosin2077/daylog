package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func astraFeed(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	app := filepath.Join(dir, "Daylog.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0700)
	exe, _ := os.Executable()
	data, e := os.ReadFile(exe)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(app, "Contents", "MacOS", "daylog"), data, 0700)
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>io.daylog.agent</string><key>CFBundleExecutable</key><string>daylog</string><key>CFBundleShortVersionString</key><string>`+version+`</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`), 0600)
	if out, e := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", app).CombinedOutput(); e != nil {
		t.Fatalf("codesign %v %s", e, out)
	}
	archive, _ := os.Create(filepath.Join(dir, "app.tar.gz"))
	gz := gzip.NewWriter(archive)
	tw := tar.NewWriter(gz)
	e = filepath.Walk(app, func(p string, i os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		h, e := tar.FileInfoHeader(i, "")
		if e != nil {
			return e
		}
		h.Name, _ = filepath.Rel(dir, p)
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if i.Mode().IsRegular() {
			f, e := os.Open(p)
			if e != nil {
				return e
			}
			defer f.Close()
			_, e = io.Copy(tw, f)
			return e
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	tw.Close()
	gz.Close()
	archive.Close()
	blob, _ := os.ReadFile(filepath.Join(dir, "app.tar.gz"))
	sum := sha256.Sum256(blob)
	raw, _ := json.Marshal(Feed{Version: version, Artifacts: map[string]Artifact{runtime.GOOS + "-" + runtime.GOARCH: {URL: "app.tar.gz", SHA256: hex.EncodeToString(sum[:])}}})
	manifest := filepath.Join(dir, "release.json")
	os.WriteFile(manifest, raw, 0600)
	return manifest
}
func TestConcurrentMacUpdateRejectedUntilRestartCompletes(t *testing.T) {
	for _, phase := range []string{"before_swap", "after_swap"} {
		t.Run(phase, func(t *testing.T) { testConcurrentMacUpdate(t, phase) })
	}
}
func testConcurrentMacUpdate(t *testing.T, phase string) {
	if runtime.GOOS != "darwin" {
		t.Skip()
	}
	saved := developmentBuild
	developmentBuild = "true"
	defer func() { developmentBuild = saved }()
	oldFeed, newFeed := astraFeed(t, "1.0.0"), astraFeed(t, "2.0.0")
	app := filepath.Join(t.TempDir(), "Daylog.app")
	os.Mkdir(app, 0700)
	olderAtSwap := make(chan struct{})
	newerDone := make(chan struct{})
	olderDone := make(chan error, 1)
	go func() {
		gate := func() error { close(olderAtSwap); <-newerDone; return nil }
		var before, after func() error
		if phase == "before_swap" {
			before = gate
		} else {
			after = gate
		}
		_, e := Install(context.Background(), oldFeed, app, "0.9.0", false, before, after)
		olderDone <- e
	}()
	select {
	case <-olderAtSwap:
	case e := <-olderDone:
		t.Fatalf("first update failed before swap: %v", e)
	case <-time.After(20 * time.Second):
		close(newerDone)
		t.Fatal("update never reached swap")
	}
	_, newErr := Install(context.Background(), newFeed, app, "0.9.0", false, nil, nil)
	close(newerDone)
	oldErr := <-olderDone
	version, e := exec.Command("/usr/bin/plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", filepath.Join(app, "Contents", "Info.plist")).Output()
	t.Logf("olderErr=%v newerErr=%v finalVersion=%s readErr=%v", oldErr, newErr, version, e)
	if oldErr != nil || newErr == nil || !strings.Contains(newErr.Error(), "另一个更新") || strings.TrimSpace(string(version)) != "1.0.0" {
		t.Fatal("concurrent install was not rejected", oldErr, newErr, string(version))
	}
}
