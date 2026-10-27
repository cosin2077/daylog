package journal

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ReportFrame is one screenshot embedded in the offline HTML report. Images are
// re-encoded locally as small JPEG data URIs; nothing is fetched remotely.
type ReportFrame struct {
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
	App   string    `json:"app,omitempty"`
	Thumb string    `json:"thumb"`
	Full  string    `json:"full,omitempty"`
}

// ReportImages records which screenshots a snapshot carries. Omitted counts
// frames that existed but were left out to respect the size budget.
type ReportImages struct {
	Start    time.Time     `json:"start"`
	End      time.Time     `json:"end"`
	Frames   []ReportFrame `json:"frames"`
	Omitted  int           `json:"omitted"`
	Bytes    int64         `json:"bytes"`
	Disabled bool          `json:"disabled,omitempty"`
}

// ImageOptions bounds the cost of embedding screenshots. Budget counts encoded
// JPEG bytes before base64 (the HTML grows by about 4/3 of that).
type ImageOptions struct {
	Disabled     bool
	Budget       int64
	ThumbBudget  int64
	ThumbWidth   int
	FullWidth    int
	ThumbQuality int
	FullQuality  int
}

func DefaultImageOptions() ImageOptions {
	return ImageOptions{Budget: 8 << 20, ThumbBudget: 3 << 20, ThumbWidth: 320, FullWidth: 960, ThumbQuality: 50, FullQuality: 55}
}

// FrameSource is a candidate screenshot. Read returns the original JPEG.
type FrameSource struct {
	ID       string
	At       time.Time
	App      string
	Evidence bool
	Read     func() ([]byte, error)
}

const maxSourceFrameBytes = 2 << 20

// AttachReportImages embeds screenshots captured in [start, end) into the
// overview. Missing or unreadable frames (e.g. removed by retention) are
// skipped silently; the report still renders without them.
func (s *Store) AttachReportImages(o *Overview, start, end time.Time, opt ImageOptions) error {
	if opt.Disabled {
		o.Images = &ReportImages{Start: start, End: end, Disabled: true, Frames: []ReportFrame{}}
		return nil
	}
	evidence := map[string]bool{}
	for _, list := range [][]PeriodReport{o.Views, o.Calendar, o.Periods} {
		for _, v := range list {
			for _, a := range v.Activities {
				for _, id := range a.Evidence {
					evidence[id] = true
				}
			}
		}
	}
	apps := map[string]string{}
	if observations, err := s.ReadObservations(start, end); err == nil {
		for _, ob := range observations {
			if ob.FrameID != "" && ob.App != "" {
				apps[ob.FrameID] = ob.App
			}
		}
	}
	seen := map[string]bool{}
	var sources []FrameSource
	add := func(dir, id string) {
		if seen[id] || !validID.MatchString(id) {
			return
		}
		at, err := time.Parse("20060102T150405.000000000Z", id)
		if err != nil || at.Before(start) || !at.Before(end) {
			return
		}
		path := filepath.Join(dir, id+".jpg")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxSourceFrameBytes {
			return
		}
		seen[id] = true
		sources = append(sources, FrameSource{ID: id, At: at.In(start.Location()), App: apps[id], Evidence: evidence[id], Read: func() ([]byte, error) { return readLimited(path) }})
	}
	root := filepath.Join(s.Root, "frames")
	if info, err := os.Lstat(root); err != nil || !info.IsDir() {
		o.Images = BuildReportImages(start, end, nil, opt)
		return nil
	}
	for day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location()).AddDate(0, 0, -1); day.Before(end.AddDate(0, 0, 1)); day = day.AddDate(0, 0, 1) {
		dir := filepath.Join(root, day.Format("2006-01-02"))
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if name := e.Name(); strings.HasSuffix(name, ".jpg") {
				add(dir, strings.TrimSuffix(name, ".jpg"))
			}
		}
	}
	// Legacy flat layout: only evidence frames are looked up individually.
	ids := make([]string, 0, len(evidence))
	for id := range evidence {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		add(root, id)
	}
	o.Images = BuildReportImages(start, end, sources, opt)
	return nil
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSourceFrameBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSourceFrameBytes {
		return nil, errors.New("frame too large")
	}
	return data, nil
}

// prioritize puts evidence frames first, then spreads the remaining frames
// evenly over time (coarse buckets first) so a tight budget still covers the
// whole day. The order is deterministic.
func prioritize(start time.Time, sources []FrameSource) []FrameSource {
	sorted := append([]FrameSource(nil), sources...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].At.Equal(sorted[j].At) {
			return sorted[i].At.Before(sorted[j].At)
		}
		return sorted[i].ID < sorted[j].ID
	})
	var out []FrameSource
	taken := make([]bool, len(sorted))
	for i, f := range sorted {
		if f.Evidence {
			out = append(out, f)
			taken[i] = true
		}
	}
	for _, step := range []time.Duration{20 * time.Minute, 10 * time.Minute, 5 * time.Minute, 2 * time.Minute, time.Minute, 30 * time.Second, 0} {
		buckets := map[int64]bool{}
		for i, f := range sorted {
			if taken[i] && step > 0 {
				buckets[int64(f.At.Sub(start)/step)] = true
			}
		}
		for i, f := range sorted {
			if taken[i] {
				continue
			}
			if step > 0 {
				b := int64(f.At.Sub(start) / step)
				if buckets[b] {
					continue
				}
				buckets[b] = true
			}
			out = append(out, f)
			taken[i] = true
		}
	}
	return out
}

type encodedFrame struct {
	thumb, full []byte
	ok          bool
}

// BuildReportImages encodes thumbnails (and, while the budget lasts, larger
// versions for click-to-enlarge) for the given sources.
func BuildReportImages(start, end time.Time, sources []FrameSource, opt ImageOptions) *ReportImages {
	def := DefaultImageOptions()
	if opt.Budget <= 0 {
		opt.Budget = def.Budget
	}
	if opt.ThumbBudget <= 0 || opt.ThumbBudget > opt.Budget {
		opt.ThumbBudget = min(def.ThumbBudget, opt.Budget)
	}
	if opt.ThumbWidth <= 0 {
		opt.ThumbWidth = def.ThumbWidth
	}
	if opt.FullWidth <= 0 {
		opt.FullWidth = def.FullWidth
	}
	if opt.ThumbQuality <= 0 {
		opt.ThumbQuality = def.ThumbQuality
	}
	if opt.FullQuality <= 0 {
		opt.FullQuality = def.FullQuality
	}
	result := &ReportImages{Start: start, End: end, Frames: []ReportFrame{}}
	if opt.Disabled {
		result.Disabled = true
		return result
	}
	ordered := prioritize(start, sources)
	workers := max(1, min(runtime.NumCPU(), 8))
	var thumbBytes, total int64
	stop := false
	for chunk := 0; chunk < len(ordered) && !stop; chunk += workers * 2 {
		batch := ordered[chunk:min(len(ordered), chunk+workers*2)]
		encoded := make([]encodedFrame, len(batch))
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for i := range batch {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int) {
				defer wg.Done()
				defer func() { <-sem }()
				encoded[i] = encodeFrame(batch[i], opt)
			}(i)
		}
		wg.Wait()
		for i, e := range encoded {
			if !e.ok {
				continue
			}
			if thumbBytes+int64(len(e.thumb)) > opt.ThumbBudget || total+int64(len(e.thumb)) > opt.Budget {
				stop = true
				break
			}
			f := batch[i]
			frame := ReportFrame{ID: f.ID, At: f.At.In(start.Location()), Thumb: dataURI(e.thumb)}
			if f.App != "" {
				frame.App = displayApp(f.App)
			}
			thumbBytes += int64(len(e.thumb))
			total += int64(len(e.thumb))
			// Reserve room so later thumbnails are not starved by full images.
			if len(e.full) > 0 && total+int64(len(e.full)) <= opt.Budget-(opt.ThumbBudget-thumbBytes) {
				frame.Full = dataURI(e.full)
				total += int64(len(e.full))
			}
			result.Frames = append(result.Frames, frame)
		}
	}
	result.Omitted = len(sources) - len(result.Frames)
	result.Bytes = total
	sort.SliceStable(result.Frames, func(i, j int) bool { return result.Frames[i].At.Before(result.Frames[j].At) })
	return result
}

func dataURI(b []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b)
}

func encodeFrame(f FrameSource, opt ImageOptions) (out encodedFrame) {
	defer func() {
		if recover() != nil {
			out = encodedFrame{}
		}
	}()
	if f.Read == nil {
		return
	}
	data, err := f.Read()
	if err != nil || len(data) == 0 || len(data) > maxSourceFrameBytes {
		return
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > 40_000_000 {
		return
	}
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return
	}
	rgba := toRGBA(src)
	thumb := resizeBox(rgba, opt.ThumbWidth)
	var buf bytes.Buffer
	if jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: opt.ThumbQuality}) != nil {
		return
	}
	out.thumb = append([]byte(nil), buf.Bytes()...)
	if opt.FullWidth > opt.ThumbWidth && rgba.Bounds().Dx() > opt.ThumbWidth {
		buf.Reset()
		if jpeg.Encode(&buf, resizeBox(rgba, opt.FullWidth), &jpeg.Options{Quality: opt.FullQuality}) == nil {
			out.full = append([]byte(nil), buf.Bytes()...)
		}
	}
	out.ok = true
	return
}

func toRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// resizeBox downsamples with an area-average (box) filter; it never upscales.
func resizeBox(src *image.RGBA, width int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if width <= 0 || sw <= width {
		return src
	}
	height := max(1, sh*width/sw)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		y0, y1 := y*sh/height, max(y*sh/height+1, (y+1)*sh/height)
		for x := 0; x < width; x++ {
			x0, x1 := x*sw/width, max(x*sw/width+1, (x+1)*sw/width)
			var r, g, b, a, n uint32
			for yy := y0; yy < y1; yy++ {
				row := src.Pix[yy*src.Stride:]
				for xx := x0; xx < x1; xx++ {
					p := row[xx*4 : xx*4+4]
					r += uint32(p[0])
					g += uint32(p[1])
					b += uint32(p[2])
					a += uint32(p[3])
					n++
				}
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0], o[1], o[2], o[3] = uint8(r/n), uint8(g/n), uint8(b/n), uint8(a/n)
		}
	}
	return dst
}
