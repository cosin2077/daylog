package journal

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"sort"
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

// Local changes can be significant even when the entire screen is nearly static.
func VisualChanged(c Config, current, previous Fingerprint) bool {
	if Difference(current, previous) >= c.ChangeThreshold {
		return true
	}
	for by := 0; by < 18; by += 3 {
		for bx := 0; bx < 32; bx += 4 {
			var sum float64
			for y := by; y < by+3; y++ {
				for x := bx; x < bx+4; x++ {
					for k := 0; k < 3; k++ {
						i := (y*32+x)*3 + k
						sum += math.Abs(float64(current[i]) - float64(previous[i]))
					}
				}
			}
			if sum/(36*255) >= math.Max(0.08, c.ChangeThreshold*8) {
				return true
			}
		}
	}
	return false
}

// Preserve app boundaries before filling temporal gaps. Rank is deterministic;
// video-like high novelty cannot consume every slot ahead of a brief app switch.
func SelectKeyFrames(frames []Frame, target, limit int) []Frame {
	if target > limit {
		target = limit
	}
	if target < 1 {
		target = 1
	}
	if len(frames) <= target {
		return append([]Frame(nil), frames...)
	}
	boundary := 0
	for i := 1; i < len(frames); i++ {
		if frames[i].App != frames[i-1].App {
			boundary++
		}
	}
	n := target
	if boundary+2 > n {
		n = boundary + 2
	}
	if n > limit {
		n = limit
	}
	if n > len(frames) {
		n = len(frames)
	}
	chosen := map[int]bool{0: true}
	if n > 1 {
		chosen[len(frames)-1] = true
	}
	for len(chosen) < n {
		best := -1
		score := -1.0
		for i, f := range frames {
			if chosen[i] {
				continue
			}
			distance := float64(len(frames))
			for j := range chosen {
				d := math.Abs(float64(i - j))
				if d < distance {
					distance = d
				}
			}
			priority := distance/float64(len(frames)) + math.Min(f.Change, 1)*0.25
			if i > 0 && frames[i-1].App != f.App {
				priority += 2
			}
			if priority > score {
				best = i
				score = priority
			}
		}
		chosen[best] = true
	}
	indexes := make([]int, 0, n)
	for i := range chosen {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	out := make([]Frame, 0, n)
	for _, i := range indexes {
		out = append(out, frames[i])
	}
	return out
}
