package native

import (
	"errors"
	"image"
	"testing"
)

func TestCapturePrivacyIncludesVisibleBackgroundWindows(t *testing.T) {
	region := image.Rect(0, 0, 1920, 1080)
	for _, tc := range []struct {
		name    string
		windows []privacyWindow
		blocked []string
		deny    bool
	}{
		{"background secret", []privacyWindow{{App: "editor.exe", Bounds: region, BoundsKnown: true}, {App: "keepass.exe", Bounds: image.Rect(20, 20, 700, 700), BoundsKnown: true}}, []string{"Keepass.EXE"}, true},
		{"excluded outside capture", []privacyWindow{{App: "keepass.exe", Bounds: image.Rect(1920, 0, 2500, 700), BoundsKnown: true}}, []string{"keepass.exe"}, false},
		{"unknown visible owner", []privacyWindow{{Bounds: region, BoundsKnown: true}}, []string{"keepass.exe"}, true},
		{"unavailable bounds", []privacyWindow{{App: "editor.exe"}}, []string{"keepass.exe"}, true},
		{"no exclusions", []privacyWindow{{}}, nil, false},
		{"allowed windows", []privacyWindow{{App: "editor.exe", Bounds: region, BoundsKnown: true}}, []string{"keepass.exe"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCapturePrivacy(region, tc.windows, tc.blocked)
			if errors.Is(err, ErrBlocked) != tc.deny {
				t.Fatal("incorrect privacy decision", err)
			}
		})
	}
}
