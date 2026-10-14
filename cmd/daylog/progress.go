package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Progress is confined to stderr and disabled entirely for --json callers.
func withProgress(show bool, label string, work func() error) error {
	if !show {
		return work()
	}
	info, err := os.Stderr.Stat()
	tty := err == nil && info.Mode()&os.ModeCharDevice != 0 && os.Getenv("TERM") != "" && os.Getenv("TERM") != "dumb"
	return progressOn(os.Stderr, tty, label, work)
}
func progressOn(w io.Writer, tty bool, label string, work func() error) error {
	if !tty {
		fmt.Fprintln(w, label+"…")
		return work()
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
		}
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			fmt.Fprintf(w, "\r\033[K%s %s…", frames[i%len(frames)], label)
			select {
			case <-done:
				fmt.Fprint(w, "\r\033[K")
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { close(done); wg.Wait() }()
	return work()
}
