package journal

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// syntheticJPEG returns a noisy test image so JPEG sizes are not trivially small.
func syntheticJPEG(t *testing.T, w, h, seed int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8((x*7 + y*13 + seed*31 + (x*y)%97) % 255)
			img.Set(x, y, color.RGBA{v, uint8(255 - v), uint8(x + seed), 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decodeDataURI(t *testing.T, uri string) image.Config {
	t.Helper()
	const prefix = "data:image/jpeg;base64,"
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("not a JPEG data URI: %.40q", uri)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func sourcesEvery(t *testing.T, start time.Time, n int, step time.Duration, w, h int) []FrameSource {
	var out []FrameSource
	for i := 0; i < n; i++ {
		data := syntheticJPEG(t, w, h, i)
		at := start.Add(time.Duration(i) * step)
		out = append(out, FrameSource{ID: ID(at), At: at, App: "com.google.Chrome", Read: func() ([]byte, error) { return data, nil }})
	}
	return out
}

func TestPrioritizeEvidenceFirstThenSpreadsOverTime(t *testing.T) {
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	var sources []FrameSource
	for i := 60; i >= 0; i-- { // reversed input must not matter
		at := start.Add(time.Duration(i) * time.Minute)
		sources = append(sources, FrameSource{ID: ID(at), At: at, Evidence: i == 37})
	}
	got := prioritize(start, sources)
	if len(got) != len(sources) {
		t.Fatalf("prioritize dropped frames: %d of %d", len(got), len(sources))
	}
	minute := func(f FrameSource) int { return int(f.At.Sub(start) / time.Minute) }
	want := []int{37, 0, 40, 60}
	for i, m := range want {
		if minute(got[i]) != m {
			t.Fatalf("order[%d] = minute %d, want %d (evidence first, then one per 20-minute bucket)", i, minute(got[i]), m)
		}
	}
	again := prioritize(start, sources)
	for i := range got {
		if got[i].ID != again[i].ID {
			t.Fatal("prioritize is not deterministic")
		}
	}
	seen := map[string]bool{}
	for _, f := range got {
		if seen[f.ID] {
			t.Fatal("duplicate frame in priority order")
		}
		seen[f.ID] = true
	}
}

func TestBuildReportImagesRespectsThumbnailBudget(t *testing.T) {
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	sources := sourcesEvery(t, start, 24, time.Minute, 640, 400)
	one := BuildReportImages(start, start.Add(time.Hour), sources[:1], ImageOptions{Budget: 1 << 20, ThumbBudget: 1 << 20})
	if len(one.Frames) != 1 {
		t.Fatal("single frame was not embedded")
	}
	size, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(one.Frames[0].Thumb, "data:image/jpeg;base64,"))
	// Room for about five thumbnails and no larger copies.
	budget := int64(len(size))*5 + int64(len(size))/3
	got := BuildReportImages(start, start.Add(time.Hour), sources, ImageOptions{Budget: budget, ThumbBudget: budget})
	if len(got.Frames) < 3 || len(got.Frames) > 7 {
		t.Fatalf("embedded %d frames under a ~5 thumbnail budget", len(got.Frames))
	}
	if got.Bytes > budget {
		t.Fatalf("budget exceeded: %d > %d", got.Bytes, budget)
	}
	if got.Omitted != len(sources)-len(got.Frames) {
		t.Fatalf("omitted = %d, want %d", got.Omitted, len(sources)-len(got.Frames))
	}
	for i, f := range got.Frames {
		if f.Full != "" {
			t.Fatal("full-size copy embedded although the budget only covers thumbnails")
		}
		if cfg := decodeDataURI(t, f.Thumb); cfg.Width > DefaultImageOptions().ThumbWidth {
			t.Fatalf("thumbnail width %d exceeds %d", cfg.Width, DefaultImageOptions().ThumbWidth)
		}
		if i > 0 && f.At.Before(got.Frames[i-1].At) {
			t.Fatal("frames are not sorted by time")
		}
		if f.App != "Chrome" {
			t.Fatalf("app label = %q, want friendly name", f.App)
		}
	}
	// Spread: a tight budget should still cover more than the first few minutes.
	if span := got.Frames[len(got.Frames)-1].At.Sub(got.Frames[0].At); span < 15*time.Minute {
		t.Fatalf("budgeted frames cover only %v of the hour", span)
	}
}

func TestBuildReportImagesAddsFullCopiesWithinBudget(t *testing.T) {
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	sources := sourcesEvery(t, start, 4, time.Minute, 800, 500)
	got := BuildReportImages(start, start.Add(time.Hour), sources, DefaultImageOptions())
	if len(got.Frames) != 4 || got.Omitted != 0 {
		t.Fatalf("frames=%d omitted=%d", len(got.Frames), got.Omitted)
	}
	for _, f := range got.Frames {
		if f.Full == "" {
			t.Fatal("full copy missing with ample budget")
		}
		if cfg := decodeDataURI(t, f.Full); cfg.Width != 800 {
			t.Fatalf("full copy width %d; must not upscale an 800px source", cfg.Width)
		}
	}
}

func TestBuildReportImagesSkipsBrokenFramesAndHonoursDisabled(t *testing.T) {
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	good := syntheticJPEG(t, 320, 200, 1)
	sources := []FrameSource{
		{ID: ID(start), At: start, Read: func() ([]byte, error) { return nil, errors.New("removed by retention") }},
		{ID: ID(start.Add(time.Minute)), At: start.Add(time.Minute), Read: func() ([]byte, error) { return []byte("not a jpeg"), nil }},
		{ID: ID(start.Add(2 * time.Minute)), At: start.Add(2 * time.Minute)},
		{ID: ID(start.Add(3 * time.Minute)), At: start.Add(3 * time.Minute), Read: func() ([]byte, error) { return good, nil }},
	}
	got := BuildReportImages(start, start.Add(time.Hour), sources, DefaultImageOptions())
	if len(got.Frames) != 1 || got.Frames[0].ID != ID(start.Add(3*time.Minute)) || got.Omitted != 3 {
		t.Fatalf("frames=%d omitted=%d", len(got.Frames), got.Omitted)
	}
	off := BuildReportImages(start, start.Add(time.Hour), sources, ImageOptions{Disabled: true})
	if !off.Disabled || len(off.Frames) != 0 || off.Frames == nil {
		t.Fatal("disabled images must produce an empty, non-nil frame list")
	}
}

func TestAttachReportImagesReadsOnlyWindowedRegularFrames(t *testing.T) {
	root := t.TempDir()
	s := &Store{Root: root}
	loc := time.FixedZone("UTC+8", 8*3600)
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	write := func(at time.Time, name string, data []byte) string {
		dir := filepath.Join(root, "frames", at.In(loc).Format("2006-01-02"))
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	img := syntheticJPEG(t, 400, 250, 2)
	inside := start.Add(9 * time.Hour)
	write(inside, ID(inside)+".jpg", img)
	write(start.Add(-time.Hour), ID(start.Add(-time.Hour))+".jpg", img)        // previous day
	write(start.Add(25*time.Hour), ID(start.Add(25*time.Hour))+".jpg", img)    // next day
	write(inside, "../escape.jpg", img)                                        // invalid name lands outside; ignored
	write(inside, "not-an-id.jpg", img)                                        // invalid ID
	write(inside, ID(inside.Add(time.Minute))+".jpg", make([]byte, (2<<20)+1)) // too large
	target := write(inside, "target.bin", img)
	if err := os.Symlink(target, filepath.Join(root, "frames", inside.Format("2006-01-02"), ID(inside.Add(2*time.Minute))+".jpg")); err != nil {
		t.Fatal(err)
	}
	// Legacy flat layout is consulted for evidence frames.
	legacy := inside.Add(3 * time.Minute)
	if err := os.WriteFile(filepath.Join(root, "frames", ID(legacy)+".jpg"), img, 0600); err != nil {
		t.Fatal(err)
	}
	o := Overview{Views: []PeriodReport{{Activities: []Activity{{Evidence: []string{ID(legacy), "../../config"}}}}}}
	if err := s.AttachReportImages(&o, start, start.Add(24*time.Hour), DefaultImageOptions()); err != nil {
		t.Fatal(err)
	}
	if o.Images == nil || len(o.Images.Frames) != 2 {
		t.Fatalf("got %+v, want the in-window frame and the legacy evidence frame", o.Images)
	}
	if o.Images.Frames[0].ID != ID(inside) || o.Images.Frames[1].ID != ID(legacy) {
		t.Fatalf("unexpected frames %s, %s", o.Images.Frames[0].ID, o.Images.Frames[1].ID)
	}
	if _, off := o.Images.Frames[0].At.Zone(); off != 8*3600 {
		t.Fatal("frame time not reported in the report's zone")
	}
	o.Images = nil
	if err := s.AttachReportImages(&o, start, start.Add(24*time.Hour), ImageOptions{Disabled: true}); err != nil || !o.Images.Disabled || len(o.Images.Frames) != 0 {
		t.Fatal("--no-images must embed nothing")
	}
}

func TestRenderHTMLEmbedsImagesSafely(t *testing.T) {
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	o := BuildOverview(start, nil, nil, Batch{})
	o.Images = BuildReportImages(start, start.Add(24*time.Hour), sourcesEvery(t, start, 3, time.Minute, 300, 200), DefaultImageOptions())
	o.Images.Frames[0].App = `</script><script>alert(1)</script>`
	data, err := RenderHTML(o, "day")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	if strings.Count(html, "</script>") != 2 { // the JSON block and the inline script
		t.Fatalf("unexpected </script> count %d", strings.Count(html, "</script>"))
	}
	// The only remote URL allowed is the project link in the header: a plain navigation
	// (new tab, no referrer, no report data in the URL), never a loaded resource.
	const projectLink = `<a class="gh-link" id="gh-link" href="https://github.com/cosin2077/daylog" target="_blank" rel="noopener noreferrer"`
	if strings.Count(html, projectLink) != 1 {
		t.Fatal("project link missing or changed")
	}
	local := strings.Replace(html, projectLink, "", 1)
	if !strings.Contains(html, "img-src data:") || strings.Contains(html, "unsafe-eval") || regexp.MustCompile(`(?i)(src|href)="https?:`).MatchString(local) {
		t.Fatal("CSP or remote resource policy changed")
	}
	match := regexp.MustCompile(`(?s)<script type="application/json" id="report-data">(.*?)</script>`).FindSubmatch(data)
	if len(match) != 2 {
		t.Fatal("report payload missing")
	}
	var decoded Overview
	if err = json.Unmarshal(match[1], &decoded); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if decoded.Images == nil || len(decoded.Images.Frames) != 3 || decoded.Images.Frames[0].App != o.Images.Frames[0].App {
		t.Fatal("embedded frames lost in the payload")
	}
	for _, id := range []string{`id="peek"`, `id="lightbox"`, `id="rhythm-chart"`, `id="share"`, `id="share-canvas"`, `rel="icon"`} {
		if !strings.Contains(html, id) {
			t.Fatalf("report markup lacks %s", id)
		}
	}
}
