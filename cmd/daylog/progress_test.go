package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProgressDoesNotSwallowFailureOrEmitANSINonTTY(t *testing.T) {
	var out bytes.Buffer
	want := errors.New("fixture failure")
	got := progressOn(&out, false, "检查更新", func() error { return want })
	if !errors.Is(got, want) || strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "检查更新") {
		t.Fatal("non-TTY progress broke command result")
	}
}
func TestTTYProgressStopsBeforeFinalOutput(t *testing.T) {
	var out bytes.Buffer
	if err := progressOn(&out, true, "检查更新", func() error { time.Sleep(180 * time.Millisecond); return nil }); err != nil {
		t.Fatal(err)
	}
	out.WriteString("done")
	before := out.String()
	time.Sleep(120 * time.Millisecond)
	if out.String() != before || !strings.HasSuffix(before, "\r\x1b[Kdone") {
		t.Fatal("spinner continued after completion")
	}
}
