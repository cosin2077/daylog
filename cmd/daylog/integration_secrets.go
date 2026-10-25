package main

import (
	"errors"
	"fmt"

	"daylog/internal/credentials"
	"daylog/internal/native"
)

type legacySecretStore interface {
	Get(string) string
	Delete(string) error
}

type nativeLegacySecrets struct{}

func (nativeLegacySecrets) Get(name string) string   { return native.NamedSecret(name) }
func (nativeLegacySecrets) Delete(name string) error { return native.DeleteNamedSecret(name) }

// integrationSecrets migrates legacy system credentials once, then uses the
// portable encrypted file. Deletion happens only after an exact read-back.
type integrationSecrets struct {
	root   string
	legacy legacySecretStore
}

func newIntegrationSecrets(root string) integrationSecrets {
	return integrationSecrets{root: root, legacy: nativeLegacySecrets{}}
}

func (s integrationSecrets) Set(name, value string) error {
	store := credentials.Store{Root: s.root}
	if err := store.Set(name, value); err != nil {
		return fmt.Errorf("无法保存本地加密凭据：%w", err)
	}
	check, err := store.Get(name)
	if err != nil || check != value {
		return errors.New("无法确认本地加密凭据，旧凭据未删除")
	}
	if s.legacy != nil {
		_ = s.legacy.Delete(name)
	}
	return nil
}

func (s integrationSecrets) Get(name string) (string, error) {
	store := credentials.Store{Root: s.root}
	value, err := store.Get(name)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, credentials.ErrNotFound) || s.legacy == nil {
		return "", err
	}
	legacy := s.legacy.Get(name)
	if legacy == "" {
		return "", credentials.ErrNotFound
	}
	if err = store.Set(name, legacy); err != nil {
		return "", fmt.Errorf("旧钥匙串凭据尚未迁移，原条目已保留：%w", err)
	}
	check, err := store.Get(name)
	if err != nil || check != legacy {
		return "", errors.New("旧钥匙串凭据迁移后无法确认，原条目已保留")
	}
	_ = s.legacy.Delete(name)
	return legacy, nil
}
