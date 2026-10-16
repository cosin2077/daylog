package journal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
)

// Intermediate UI/text/retry events are diagnostic evidence, never a report.
// Only a single nonempty terminal result can be committed.
func agentEvents(name string, data []byte) (result, kind string, retries int) {
	kind = "missing_result"
	terminal := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event struct {
			Structured json.RawMessage `json:"structured_output"`
			Type       string          `json:"type"`
			Subtype    string          `json:"subtype"`
			Result     string          `json:"result"`
			Text       string          `json:"text"`
			IsError    bool            `json:"is_error"`
			ExitCode   int             `json:"exit_code"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Type == "system" && event.Subtype == "api_retry" {
			retries++
		}
		if event.Type != "result" {
			continue
		}
		if terminal {
			return "", "invalid_json", retries
		}
		terminal = true
		if event.IsError || event.ExitCode != 0 {
			kind = "agent_error"
			continue
		}
		if name == "claude" {
			if len(event.Structured) > 0 && string(event.Structured) != "null" {
				result = string(event.Structured)
			} else {
				result = event.Result
			}
		} else {
			result = event.Text
		}
		if strings.TrimSpace(result) != "" {
			kind = ""
		}
	}
	if scanner.Err() != nil {
		return "", "output_limit", retries
	}
	return result, kind, retries
}
