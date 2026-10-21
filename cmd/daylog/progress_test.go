package main

import (
	"bytes"
	"context"
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

func TestTTYProgressClearsBeforeCancellationError(t *testing.T) {
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	writer := progressWriteFunc(func(p []byte) (int, error) {
		n, err := out.Write(p)
		if strings.Contains(string(p), "正在同步到 Notion") {
			cancel()
		}
		return n, err
	})
	err := progressOn(writer, true, "正在同步到 Notion", func() error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || !strings.Contains(out.String(), "正在同步到 Notion") || !strings.HasSuffix(out.String(), "\r\x1b[K") {
		t.Fatal("progress hid cancellation or left a stale terminal line", err, out.String())
	}
}

type progressWriteFunc func([]byte) (int, error)

func (f progressWriteFunc) Write(p []byte) (int, error) { return f(p) }
