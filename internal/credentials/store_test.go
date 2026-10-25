package credentials

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestEncryptedRoundTripAndNoPlaintext(t *testing.T) {
	store := Store{Root: filepath.Join(t.TempDir(), "data")}
	secret := "ntn_fixture_only_1234567890"
	if err := store.Set("notion.fixture", secret); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("notion.fixture")
	if err != nil || got != secret {
		t.Fatal("round trip failed", err)
	}
	exists, err := store.Exists("notion.fixture")
	if err != nil || !exists {
		t.Fatal("missing entry", err)
	}
	vaultBytes, err := os.ReadFile(store.vaultPath())
	if err != nil || bytes.Contains(vaultBytes, []byte(secret)) {
		t.Fatal("credential was stored in plaintext")
	}
	key, err := os.ReadFile(store.keyPath())
	if err != nil || len(key) != 32 {
		t.Fatal("key is missing or invalid", err, len(key))
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(store.vaultPath()); err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("vault permissions")
		}
		if info, err := os.Stat(store.keyPath()); err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("key permissions")
		}
		if info, err := os.Stat(store.directory()); err != nil || info.Mode().Perm() != 0700 {
			t.Fatal("directory permissions")
		}
	}
	if err = store.Delete("notion.fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get("notion.fixture"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted secret remains", err)
	}
}

func TestCorruptionAndInsecurePermissionsAreRejected(t *testing.T) {
	store := Store{Root: filepath.Join(t.TempDir(), "data")}
	if err := store.Set("notion.fixture", "fixture-only"); err != nil {
		t.Fatal(err)
	}
	var v vault
	data, _ := os.ReadFile(store.vaultPath())
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	v.Entries["notion.fixture"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 40))
	data, _ = json.Marshal(v)
	if err := os.WriteFile(store.vaultPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("notion.fixture"); err == nil || err == ErrNotFound {
		t.Fatal("corruption was accepted")
	}
	if err := os.Chmod(store.vaultPath(), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("notion.fixture"); err == nil {
		t.Fatal("insecure permissions were accepted")
	}
}

func TestConcurrentWritesPreserveAllEntries(t *testing.T) {
	store := Store{Root: filepath.Join(t.TempDir(), "data")}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("fixture.%02d", i)
			if err := store.Set(name, "value-"+name); err != nil {
				t.Errorf("set %s: %v", name, err)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("fixture.%02d", i)
		got, err := store.Get(name)
		if err != nil || got != "value-"+name {
			t.Fatalf("lost %s: %q %v", name, got, err)
		}
	}
}

func TestSymlinkKeyRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires additional Windows privileges")
	}
	store := Store{Root: filepath.Join(t.TempDir(), "data")}
	if err := store.Set("notion.fixture", "fixture-only"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(store.directory(), "key-target")
	key, _ := os.ReadFile(store.keyPath())
	if err := os.WriteFile(target, key, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.keyPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, store.keyPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("notion.fixture"); err == nil {
		t.Fatal("symlinked key accepted")
	}
}

func TestMissingKeyDoesNotSilentlyReencryptExistingVault(t *testing.T) {
	store := Store{Root: filepath.Join(t.TempDir(), "data")}
	if err := store.Set("notion.fixture", "original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.keyPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("notion.fixture"); err == nil {
		t.Fatal("missing key was silently regenerated for existing vault")
	}
	if err := store.Set("notion.fixture", "replacement"); err == nil {
		t.Fatal("set overwrote entries without the original key")
	}
}
