package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
