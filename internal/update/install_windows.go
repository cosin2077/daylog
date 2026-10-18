//go:build windows

package update

import (
	"context"
	"crypto/sha256"
	"daylog/internal/platform"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func installWindows(ctx context.Context, source, currentExe, currentVersion string, force bool, beforeSwap, afterSwap func() error) (Check, error) {
	feed, check, e := Inspect(ctx, source, currentVersion)
	if e != nil || !force && !check.Upgrade {
		return check, e
	}
	if !filepath.IsAbs(currentExe) || !strings.EqualFold(filepath.Ext(currentExe), ".exe") {
		return check, errors.New("Windows update requires an installed executable")
	}
	lock, e := os.OpenFile(filepath.Join(filepath.Dir(currentExe), "update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return check, e
	}
	defer lock.Close()
	if e = platform.TryLock(lock); e != nil {
		return check, errors.New("another update is running")
	}
	defer platform.Unlock(lock)
	stage, e := os.MkdirTemp(filepath.Dir(currentExe), ".daylog-update-")
	if e != nil {
		return check, e
	}
	// Windows may keep the old image mapped until this updater exits. In that
	// case retain its backup, which the next invocation can remove safely.
	keep := false
	defer func() {
		if !keep {
			if e := os.RemoveAll(stage); e != nil {
				// Only a successful replacement gets a cleanup receipt. A failed
				// rollback retains recovery data without marking it disposable.
				if _, e = os.Stat(currentExe); e == nil {
					if entries, e := os.ReadDir(stage); e == nil && len(entries) == 1 && entries[0].Name() == "previous.exe" {
						raw, e := os.ReadFile(currentExe)
						if e == nil {
							digest := sha256.Sum256(raw)
							receipt, _ := json.Marshal(cleanupReceipt{Target: filepath.Base(currentExe), SHA256: hex.EncodeToString(digest[:])})
							_ = os.WriteFile(filepath.Join(stage, "cleanup.json"), receipt, 0600)
						}
					}
				}
			}
		}
	}()
	a := feed.Artifacts[check.Platform]
	url, e := artifactSource(source, a.URL)
	if e != nil {
		return check, e
	}
	in, e := openSource(ctx, url)
	if e != nil {
		return check, e
	}
	defer in.Close()
	archive := filepath.Join(stage, "update.zip")
	out, e := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return check, e
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, (32<<20)+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || n > 32<<20 {
		return check, errors.New("update ZIP exceeds limit or is unreadable")
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.ToLower(a.SHA256) {
		return check, errors.New("update checksum mismatch")
	}
	if e = extractWindows(archive, stage, feed.Version, runtime.GOARCH, developmentBuild == "true" && developmentSource(source)); e != nil {
		return check, e
	}
	newExe := filepath.Join(stage, "daylog.exe")
	// Execution is safe only after publisher verification; also check the build
	// version, not just the label in the signed archive metadata.
	versionData, e := exec.CommandContext(ctx, newExe, "version", "--json").Output()
	if e != nil {
		return check, errors.New("verified executable cannot run")
	}
	var v struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(versionData, &v) != nil || v.Version != feed.Version {
		return check, errors.New("Windows build version mismatch")
	}
	for _, name := range []string{"LICENSE", "THIRD_PARTY_NOTICES.txt"} {
		data, err := os.ReadFile(filepath.Join(stage, name))
		if err != nil {
			return check, err
		}
		if err = os.WriteFile(filepath.Join(filepath.Dir(currentExe), name), data, 0600); err != nil {
			return check, err
		}
	}
	if beforeSwap != nil {
		if e = beforeSwap(); e != nil {
			return check, e
		}
	}
	old := filepath.Join(stage, "previous.exe")
	if e = os.Rename(currentExe, old); e != nil {
		return check, errors.New("cannot stage installed executable; close other Daylog commands and retry")
	}
	if e = os.Rename(newExe, currentExe); e != nil {
		if rollback := os.Rename(old, currentExe); rollback != nil {
			keep = true
			return check, errors.New("replacement and rollback failed; previous.exe retained in update directory")
		}
		return check, errors.New("replacement failed; previous executable restored")
	}
	if afterSwap != nil {
		return check, afterSwap()
	}
	return check, nil
}

type cleanupReceipt struct {
	Target string `json:"target"`
	SHA256 string `json:"sha256"`
}

// Do not scan user data or schedule privileged reboot-time deletions. Remove
// only our exact post-update leftovers once the old image is no longer mapped.
func CleanupStaging(executable string) {
	parent := filepath.Dir(executable)
	entries, e := os.ReadDir(parent)
	if e != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), ".daylog-update-") {
			continue
		}
		dir := filepath.Join(parent, entry.Name())
		files, e := os.ReadDir(dir)
		if e != nil || len(files) != 2 {
			continue
		}
		valid := true
		for _, f := range files {
			if f.Type()&os.ModeSymlink != 0 || f.IsDir() || (f.Name() != "previous.exe" && f.Name() != "cleanup.json") {
				valid = false
			}
		}
		if !valid {
			continue
		}
		var receipt cleanupReceipt
		raw, e := os.ReadFile(filepath.Join(dir, "cleanup.json"))
		if e != nil || len(raw) > 4096 || json.Unmarshal(raw, &receipt) != nil || receipt.Target != filepath.Base(executable) {
			continue
		}
		binary, e := os.ReadFile(executable)
		if e != nil {
			continue
		}
		digest := sha256.Sum256(binary)
		if receipt.SHA256 != hex.EncodeToString(digest[:]) {
			continue
		}
		if os.Remove(filepath.Join(dir, "previous.exe")) == nil {
			os.Remove(filepath.Join(dir, "cleanup.json"))
			os.Remove(dir)
		}
	}
}
