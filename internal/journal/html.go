package journal

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"path/filepath"
	"time"
)

//go:embed report.html
var reportTemplate string

func RenderHTML(overview Overview, initial string) ([]byte, error) {
	if initial != "day" && initial != "week" && initial != "month" && initial != "range" {
		return nil, errors.New("invalid report period")
	}
	// Serialize calendar-aware ticks; a fixed UTC offset is wrong across DST.
	ticks := func(views []PeriodReport) []PeriodReport {
		views = append([]PeriodReport(nil), views...)
		for i := range views {
			v := &views[i]
			if len(v.Activities) == 0 {
				continue
			}
			start, end := v.Activities[0].Start, v.Activities[0].End
			for _, a := range v.Activities {
				if a.Start.Before(start) {
					start = a.Start
				}
				if a.End.After(end) {
					end = a.End
				}
			}
			if !end.After(start) {
				start = start.Add(-5 * time.Minute)
				end = end.Add(5 * time.Minute)
			}
			v.AxisTicks = nil
			for n := 0; n < 5; n++ {
				v.AxisTicks = append(v.AxisTicks, start.Add(end.Sub(start)*time.Duration(n)/4).In(v.Start.Location()))
			}
		}
		return views
	}
	overview.Views = ticks(overview.Views)
	overview.Calendar = ticks(overview.Calendar)
	overview.Periods = ticks(overview.Periods)
	data, err := json.Marshal(overview)
	if err != nil {
		return nil, err
	}
	t, err := template.New("report").Parse(reportTemplate)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	// encoding/json escapes HTML delimiters and U+2028/U+2029. The browser
	// parses this data as JSON and inserts report text only via textContent.
	err = t.Execute(&out, struct {
		Data    template.JS
		Initial string
	}{template.JS(data), initial})
	return out.Bytes(), err
}

func (s *Store) WriteHTML(overview Overview, initial string) (string, error) {
	data, err := RenderHTML(overview, initial)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.Root, "reports", "report.html")
	return path, writeAtomic(path, data)
}
