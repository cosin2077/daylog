//go:build darwin && cgo

package native

import (
	"context"
	"testing"
)

// Querying power/session metadata must decode without Screen Recording access.
// This exercises the actual Objective-C JSON boundary, without capturing pixels.
func TestEnvironmentMetadataDecode(t *testing.T) {
	env, err := (Desktop{}).Environment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if env.IdleSeconds < 0 || env.BatteryPercent < -1 || env.BatteryPercent > 100 {
		t.Fatal("native metadata values outside their public range")
	}
}
