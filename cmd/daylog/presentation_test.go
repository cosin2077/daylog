package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"daylog/internal/journal"
	"daylog/internal/native"
)

func commandOutput(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = previous; r.Close(); w.Close() }()
	// Commands under test have bounded output smaller than the pipe capacity.
	err = execute(args)
	w.Close()
	os.Stdout = previous
	data, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return data, err
}

func TestQueryCommandsRequireExplicitJSON(t *testing.T) {
	s := configStore(t)
	t.Setenv("DAYLOG_AGENT_SEARCH_PATH", t.TempDir())
	for _, command := range []string{"version", "--version", "init", "config", "agents", "pending", "today"} {
		t.Run(command, func(t *testing.T) {
			data, err := commandOutput(t, "--data", s.Root, command)
			if err != nil || len(bytes.TrimSpace(data)) == 0 || json.Valid(data) {
				t.Fatalf("default output is not readable: %s (%v)", data, err)
			}
			data, err = commandOutput(t, "--data", s.Root, command, "--json")
			if err != nil || !json.Valid(data) {
				t.Fatalf("JSON contract broken: %s (%v)", data, err)
			}
		})
	}
}

func TestConfigDisplayHidesKeyButExplicitExportPreservesIt(t *testing.T) {
	s := configStore(t)
	var out bytes.Buffer
	key := "fixture-only-key"
	if err := configure(s, []string{"--api-key", key}, strings.NewReader(""), &out, func(string) error { t.Fatal("unexpected keychain access"); return nil }); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := configure(s, nil, strings.NewReader(""), &out, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), key) || !strings.Contains(out.String(), "内容隐藏") {
		t.Fatal("default display leaked a key or lost its configured state")
	}
	out.Reset()
	if err := configure(s, []string{"--json"}, strings.NewReader(""), &out, nil); err != nil {
		t.Fatal(err)
	}
	var config journal.Config
	if err := json.Unmarshal(out.Bytes(), &config); err != nil || config.AI.APIKey != key {
		t.Fatal("explicit JSON is no longer a complete config")
	}
}

func TestDoctorDisplayDistinguishesPolicyAndConfiguration(t *testing.T) {
	var out bytes.Buffer
	writeDoctor(&out, "/fixture", native.Environment{OnBattery: true, BatteryPercent: 70}, journal.DefaultConfig(), true)
	permissionText := "未授权"
	if runtime.GOOS == "windows" {
		permissionText = "当前用户桌面"
	}
	for _, want := range []string{"电池供电", "接通电源后再处理", permissionText} {
		if !strings.Contains(out.String(), want) {
			t.Fatal("diagnostic state not explained")
		}
	}
	out.Reset()
	writeDoctor(&out, "/fixture", native.Environment{}, journal.DefaultConfig(), false)
	if !strings.Contains(out.String(), "暂不能判断分析策略") || strings.Contains(out.String(), "电源策略允许") {
		t.Fatal("invalid config produced reassuring policy")
	}
}

func TestWindowDisplayRemovesTerminalControlCharacters(t *testing.T) {
	var out bytes.Buffer
	writeWindows(&out, []native.Window{{ID: 42, App: "fixture", Title: "title\x1b[2J\nnext"}})
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "42") {
		t.Fatal("window title can control terminal output")
	}
}
