package journal

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"time"
)

// A small RGB grid suppresses cursor/clock noise without keeping decoded frames.
type Fingerprint [32 * 18 * 3]uint8

func FingerprintJPEG(data []byte) (Fingerprint, error) {
	var fp Fingerprint
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
		return fp, errors.New("invalid or oversized image")
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fp, err
	}
	b := im.Bounds()
	for y := 0; y < 18; y++ {
		for x := 0; x < 32; x++ {
			r, g, blue, _ := im.At(b.Min.X+(2*x+1)*b.Dx()/64, b.Min.Y+(2*y+1)*b.Dy()/36).RGBA()
			i := (y*32 + x) * 3
			fp[i] = uint8(r >> 8)
			fp[i+1] = uint8(g >> 8)
			fp[i+2] = uint8(blue >> 8)
		}
	}
	return fp, nil
}

func Difference(a, b Fingerprint) float64 {
	var total float64
	for i := range a {
		total += math.Abs(float64(a[i]) - float64(b[i]))
	}
	return total / (float64(len(a)) * 255)
}

func ShouldSave(c Config, now time.Time, last time.Time, app, lastApp string, current, previous Fingerprint) bool {
	if last.IsZero() {
		return true
	}
	elapsed := now.Sub(last)
	if elapsed < time.Duration(c.MinSaveSeconds)*time.Second {
		return false
	}
	return app != lastApp || elapsed >= time.Duration(c.HeartbeatSeconds)*time.Second || Difference(current, previous) >= c.ChangeThreshold
}

func SelectFrames(frames []Frame, n int) []Frame {
	if len(frames) <= n {
		return append([]Frame(nil), frames...)
	}
	if n == 1 {
		return []Frame{frames[len(frames)-1]}
	}
	out := make([]Frame, n)
	for i := range out {
		index := int(math.Round(float64(i) * float64(len(frames)-1) / float64(n-1)))
		out[i] = frames[index]
	}
	return out
}
