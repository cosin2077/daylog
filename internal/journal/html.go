package journal

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"path/filepath"
)

//go:embed report.html
var reportTemplate string

func RenderHTML(overview Overview, initial string) ([]byte, error) {
	if initial != "day" && initial != "week" && initial != "month" && initial != "range" {
		return nil, errors.New("invalid report period")
	}
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
