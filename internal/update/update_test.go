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
)

func archiveFixture(t *testing.T, name string, kind byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "update.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	header := &tar.Header{Name: name, Mode: 0644, Typeflag: kind}
	if kind == tar.TypeSymlink {
		header.Linkname = "/tmp/escape"
	} else {
		header.Size = 4
	}
	if err = writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if kind != tar.TypeSymlink {
		writer.Write([]byte("data"))
	}
	writer.Close()
	gz.Close()
	file.Close()
	return path
}

func TestDevelopmentFeedCannotBePublic(t *testing.T) {
	for _, source := range []string{"https://github.com/owner/repo/release.json", "http://127.0.0.1.evil/release.json", "file:///tmp/release.json"} {
		if developmentSource(source) {
			t.Fatal("public or unsupported feed accepted as development")
		}
	}
	for _, source := range []string{"/tmp/release.json", "http://127.0.0.1:1234/release.json"} {
		if !developmentSource(source) {
			t.Fatal("local development feed rejected")
		}
	}
}

func TestValidAdHocSignatureCannotReplacePinnedRelease(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires macOS codesign")
	}
	dir := t.TempDir()
	app := filepath.Join(dir, "Daylog.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0700)
	executable, _ := os.Executable()
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(app, "Contents", "MacOS", "daylog"), data, 0700)
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>io.daylog.agent</string><key>CFBundleExecutable</key><string>daylog</string><key>CFBundleShortVersionString</key><string>9.0.0</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`), 0600)
	if out, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", app).CombinedOutput(); err != nil {
		t.Fatalf("fixture signing: %s", out)
	}
	if err := verifyPublisher(context.Background(), app, false); err == nil {
		t.Fatal("untrusted signer accepted")
	}
	if err := verifyPublisher(context.Background(), app, true); err != nil {
		t.Fatal("explicit development signature rejected")
	}
	archive, err := os.Create(filepath.Join(dir, "app.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(archive)
	tw := tar.NewWriter(gz)
	err = filepath.Walk(app, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name, _ = filepath.Rel(dir, path)
		if err = tw.WriteHeader(header); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	archive.Close()
	blob, _ := os.ReadFile(filepath.Join(dir, "app.tar.gz"))
	sum := sha256.Sum256(blob)
	feed := Feed{Version: "9.0.0", Artifacts: map[string]Artifact{runtime.GOOS + "-" + runtime.GOARCH: {URL: "app.tar.gz", SHA256: hex.EncodeToString(sum[:])}}}
	raw, _ := json.Marshal(feed)
	manifest := filepath.Join(dir, "release.json")
	os.WriteFile(manifest, raw, 0600)
	stopped := false
	_, err = Install(context.Background(), manifest, app, "8.0.0", false, func() error { stopped = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "publisher") || stopped {
		t.Fatalf("wrong signer reached replacement: %v stopped=%v", err, stopped)
	}
}
func TestArchiveRejectsTraversalAndLinks(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind byte
	}{{"../../escape", tar.TypeReg}, {"Daylog.app/../../escape", tar.TypeReg}, {"Daylog.app/link", tar.TypeSymlink}} {
		path := archiveFixture(t, tc.name, tc.kind)
		if extract(path, t.TempDir()) == nil {
			t.Fatal("unsafe update archive accepted")
		}
	}
}
func TestManifestSelectsPlatformAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	feed := Feed{Version: "0.2.0", Artifacts: map[string]Artifact{runtime.GOOS + "-" + runtime.GOARCH: {URL: "update.tar.gz", SHA256: hex.EncodeToString(make([]byte, 32))}}}
	data, _ := json.Marshal(feed)
	os.WriteFile(path, data, 0600)
	_, check, err := Inspect(context.Background(), path, "0.1.0")
	if err != nil || !check.Upgrade {
		t.Fatal("platform update not selected")
	}
	_, check, err = Inspect(context.Background(), path, "0.3.0")
	if err != nil || check.Upgrade {
		t.Fatal("downgrade advertised as upgrade")
	}
}
func TestChecksumFailureNeverStopsRunningApp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native replacement is macOS-only")
	}
	dir := t.TempDir()
	archive := archiveFixture(t, "Daylog.app/Contents/file", tar.TypeReg)
	data, _ := os.ReadFile(archive)
	hash := sha256.Sum256(data)
	wrong := hash
	wrong[0] ^= 1
	os.WriteFile(filepath.Join(dir, "update.tar.gz"), data, 0600)
	feed := Feed{Version: "0.2.0", Artifacts: map[string]Artifact{runtime.GOOS + "-" + runtime.GOARCH: {URL: "update.tar.gz", SHA256: hex.EncodeToString(wrong[:])}}}
	raw, _ := json.Marshal(feed)
	manifest := filepath.Join(dir, "release.json")
	os.WriteFile(manifest, raw, 0600)
	app := filepath.Join(dir, "Daylog.app")
	os.Mkdir(app, 0700)
	called := false
	_, err := Install(context.Background(), manifest, app, "0.1.0", false, func() error { called = true; return nil })
	if err == nil || called {
		t.Fatal("invalid update reached recording stop/replacement")
	}
	if _, err = os.Stat(app); err != nil {
		t.Fatal("installed app changed on checksum failure")
	}
}
