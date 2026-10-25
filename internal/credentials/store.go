// Package credentials stores small integration secrets in a private, portable
// encrypted file under the Daylog data directory.
package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"daylog/internal/platform"
)

var ErrNotFound = errors.New("本地凭据不存在")
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

const (
	formatVersion = 1
	maxVaultBytes = 1 << 20
)

type Store struct{ Root string }

type vault struct {
	Version int               `json:"version"`
	Entries map[string]string `json:"entries"`
}

func (s Store) directory() string { return filepath.Join(s.Root, "integrations") }
func (s Store) vaultPath() string { return filepath.Join(s.directory(), "credentials.enc") }
func (s Store) keyPath() string   { return filepath.Join(s.directory(), "credentials.key") }
func (s Store) lockPath() string  { return filepath.Join(s.directory(), "credentials.lock") }

func (s Store) Exists(name string) (bool, error) {
	var found bool
	err := s.withLock(func() error {
		v, err := s.readVault()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		found = v.Entries[name] != ""
		if found {
			_, err = s.readKey()
			return err
		}
		return nil
	})
	return found, err
}

func (s Store) Get(name string) (string, error) {
	if !validName.MatchString(name) {
		return "", errors.New("凭据名称无效")
	}
	var value string
	err := s.withLock(func() error {
		v, err := s.readVault()
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		key, err := s.readKey()
		if err != nil {
			return err
		}
		encoded := v.Entries[name]
		if encoded == "" {
			return ErrNotFound
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return errors.New("本地凭据格式无效")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return err
		}
		if len(raw) <= gcm.NonceSize() {
			return errors.New("本地凭据内容无效")
		}
		plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte("daylog-credentials-v1:"+name))
		if err != nil {
			return errors.New("本地凭据无法解密；请重新运行 daylog notion setup")
		}
		value = string(plain)
		return nil
	})
	return value, err
}

func (s Store) Set(name, value string) error {
	if !validName.MatchString(name) {
		return errors.New("凭据名称无效")
	}
	if value == "" || len(value) > 16<<10 {
		return errors.New("凭据长度无效")
	}
	return s.withLock(func() error {
		v, err := s.readVault()
		if errors.Is(err, os.ErrNotExist) {
			_, keyErr := s.readOrCreateKey()
			if keyErr != nil {
				return keyErr
			}
			v = vault{Version: formatVersion, Entries: map[string]string{}}
		} else if err != nil {
			return err
		}
		key, err := s.readKey()
		if err != nil {
			return err
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return err
		}
		nonce := make([]byte, gcm.NonceSize())
		if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
			return err
		}
		sealed := gcm.Seal(nonce, nonce, []byte(value), []byte("daylog-credentials-v1:"+name))
		v.Entries[name] = base64.StdEncoding.EncodeToString(sealed)
		return s.writeVault(v)
	})
}

func (s Store) Delete(name string) error {
	if !validName.MatchString(name) {
		return errors.New("凭据名称无效")
	}
	return s.withLock(func() error {
		v, err := s.readVault()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		delete(v.Entries, name)
		return s.writeVault(v)
	})
}

func (s Store) readKey() ([]byte, error) {
	data, err := os.ReadFile(s.keyPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("本地凭据密钥缺失，请重新运行 daylog notion setup")
	}
	if err != nil {
		return nil, errors.New("无法读取本地凭据密钥")
	}
	if len(data) != 32 {
		return nil, errors.New("本地凭据密钥长度无效")
	}
	if err = secureFile(s.keyPath()); err != nil {
		return nil, err
	}
	return data, nil
}

func (s Store) withLock(fn func() error) error {
	if err := s.ensureDirectory(); err != nil {
		return err
	}
	f, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return errors.New("无法打开本地凭据锁")
	}
	defer f.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = platform.TryLock(f)
		if err == nil {
			break
		}
		if !errors.Is(err, platform.ErrBusy) || time.Now().After(deadline) {
			return errors.New("本地凭据存储正被占用，请稍后重试")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer platform.Unlock(f)
	return fn()
}

func (s Store) ensureDirectory() error {
	dir := s.directory()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("无法创建本地凭据目录")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return errors.New("无法保护本地凭据目录")
	}
	return platform.PrivateDir(dir)
}

func (s Store) readOrCreateKey() ([]byte, error) {
	data, err := os.ReadFile(s.keyPath())
	if err == nil {
		if len(data) != 32 {
			return nil, errors.New("本地凭据密钥长度无效")
		}
		if err = secureFile(s.keyPath()); err != nil {
			return nil, err
		}
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("无法读取本地凭据密钥")
	}
	key := make([]byte, 32)
	if _, err = io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.keyPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return s.readOrCreateKey()
	}
	if err != nil {
		return nil, errors.New("无法创建本地凭据密钥")
	}
	_, writeErr := f.Write(key)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(s.keyPath())
		return nil, errors.New("无法保存本地凭据密钥")
	}
	return key, nil
}

func (s Store) readVault() (vault, error) {
	v := vault{Version: formatVersion, Entries: map[string]string{}}
	if err := secureFile(s.vaultPath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return v, os.ErrNotExist
		}
		return v, err
	}
	f, err := os.Open(s.vaultPath())
	if err != nil {
		return v, errors.New("无法读取本地凭据")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxVaultBytes+1))
	if err != nil || len(data) > maxVaultBytes {
		return v, errors.New("本地凭据文件过大或无法读取")
	}
	if err = json.Unmarshal(data, &v); err != nil || v.Version != formatVersion || v.Entries == nil {
		return v, errors.New("本地凭据文件格式无效")
	}
	return v, nil
}

func (s Store) writeVault(v vault) error {
	if v.Version != formatVersion || v.Entries == nil {
		return errors.New("本地凭据状态无效")
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxVaultBytes {
		return errors.New("本地凭据文件过大")
	}
	tmp, err := os.CreateTemp(s.directory(), ".credentials-*")
	if err != nil {
		return errors.New("无法创建本地凭据临时文件")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil || closeErr != nil {
		return errors.New("无法保存本地凭据")
	}
	if err = os.Rename(name, s.vaultPath()); err != nil {
		return errors.New("无法提交本地凭据")
	}
	return nil
}

func secureFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("本地凭据文件不安全：%s", filepath.Base(path))
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("本地凭据文件权限不安全：%s", filepath.Base(path))
	}
	return nil
}
