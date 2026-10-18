package native

import (
	"image"
	"strings"
)

func blockedApplication(app string, blocked []string) bool {
	for _, candidate := range blocked {
		if strings.EqualFold(strings.TrimSpace(candidate), app) {
			return true
		}
	}
	return false
}

type privacyWindow struct {
	App         string
	Bounds      image.Rectangle
	BoundsKnown bool
}

// GDI reads screen pixels, including other applications covering the target.
// If exclusions cannot be guaranteed, discard the entire capture. Occluded
// windows may cause conservative skips; no excluded pixels are intentionally kept.
func checkCapturePrivacy(region image.Rectangle, windows []privacyWindow, blocked []string) error {
	if len(blocked) == 0 {
		return nil
	}
	for _, window := range windows {
		if !window.BoundsKnown {
			return ErrBlocked
		}
		if !window.Bounds.Overlaps(region) {
			continue
		}
		if window.App == "" || blockedApplication(window.App, blocked) {
			return ErrBlocked
		}
	}
	return nil
}
