package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"daylog/internal/journal"
)

type modelStatus struct {
	Name          string     `json:"name"`
	Installed     bool       `json:"installed"`
	LastAt        *time.Time `json:"last_at,omitempty"`
	LastErrorKind string     `json:"last_error_kind,omitempty"`
	State         string     `json:"state"`
}

// Read bounded diagnostic metadata only. No provider call, credentials or model
// response is used to infer present connectivity from a historical result.
func doctorModels(root string, c journal.Config) []modelStatus {
	installed := map[string]bool{}
	for _, a := range journal.Agents() {
		installed[a.Name] = a.Installed
	}
	latest := map[string]journal.AnalysisLog{}
	files, _ := filepath.Glob(filepath.Join(root, "logs", "llm", "*.jsonl"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	remaining := int64(4 << 20)
	for _, file := range files {
		if remaining <= 0 {
			break
		}
		info, e := os.Stat(file)
		if e != nil {
			continue
		}
		limit := min(remaining, info.Size())
		remaining -= limit
		f, e := os.Open(file)
		if e != nil {
			continue
		}
		offset := info.Size() - limit
		if _, e := f.Seek(offset, io.SeekStart); e != nil {
			f.Close()
			continue
		}
		scanner := bufio.NewScanner(io.LimitReader(f, limit))
		scanner.Buffer(make([]byte, 4096), 1<<20)
		if offset > 0 {
			scanner.Scan() // Discard the partial first line of a truncated tail.
		}
		for scanner.Scan() {
			var event struct {
				At        time.Time `json:"at"`
				Event     string    `json:"event"`
				Source    string    `json:"source"`
				ErrorKind string    `json:"error_kind"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Event != "response" {
				continue
			}
			name := strings.TrimPrefix(event.Source, "agent:")
			if strings.HasPrefix(name, "api") {
				name = "API"
			}
			if event.At.After(latest[name].At) {
				latest[name] = journal.AnalysisLog{At: event.At, ErrorKind: event.ErrorKind}
			}
		}
		f.Close()
	}
	var result []modelStatus
	for _, name := range c.Agent.Order {
		m := modelStatus{Name: name, Installed: installed[name], State: "未安装"}
		if m.Installed {
			m.State = "已安装，尚无整理结果"
		}
		if event, ok := latest[name]; ok {
			at := event.At
			m.LastAt = &at
			m.LastErrorKind = event.ErrorKind
			m.State = "最近整理成功"
			if event.ErrorKind != "" {
				m.State = journal.FailureReason(event.ErrorKind)
			}
		}
		result = append(result, m)
	}
	if c.AI.Enabled {
		m := modelStatus{Name: "API", State: "已配置，尚无整理结果"}
		if event, ok := latest["API"]; ok {
			at := event.At
			m.LastAt = &at
			m.LastErrorKind = event.ErrorKind
			m.State = "最近整理成功"
			if event.ErrorKind != "" {
				m.State = journal.FailureReason(event.ErrorKind)
			}
		}
		result = append(result, m)
	}
	return result
}

func doctorRow(w io.Writer, label, value string) {
	width := 0
	for _, r := range label {
		if unicode.Is(unicode.Han, r) {
			width += 2
		} else {
			width++
		}
	}
	fmt.Fprintln(w, "  "+label+strings.Repeat(" ", max(2, 14-width))+value)
}
