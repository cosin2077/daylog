// Native metadata probe for manual foreground-switch verification; never captures images.
package main

import (
	"context"
	"daylog/internal/native"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		env, err := (native.Desktop{}).Environment(ctx)
		if err != nil {
			return
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"at": time.Now(), "app": env.App})
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
