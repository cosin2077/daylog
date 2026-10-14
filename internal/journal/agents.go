package journal

import (
	"bufio"
	"bytes"
	"context"
	"daylog/internal/platform"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type AgentInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Installed bool   `json:"installed"`
	Supported bool   `json:"supported"`
	Reason    string `json:"reason"`
}

func Agents() []AgentInfo {
	out := []AgentInfo{}
	for _, name := range []string{"claude", "codex", "hermes", "grok", "antigravity"} {
		info := AgentInfo{Name: name, Reason: "not_installed"}
		path, err := agentPath(name)
		if err == nil {
			info.Path = path
			info.Installed = true
			info.Supported = name == "claude" || name == "codex" || name == "hermes"
			info.Reason = "CLI_detected_auth_and_vision_not_verified"
			if !info.Supported {
				info.Reason = "no_verified_headless_image_adapter"
			}
		}
		out = append(out, info)
	}
	return out
}

func agentPath(name string) (string, error) {
	if override, ok := os.LookupEnv("DAYLOG_AGENT_SEARCH_PATH"); ok {
		for _, dir := range filepath.SplitList(override) {
			if path, err := findAgentInDir(dir, name); err == nil {
				return path, nil
			}
		}
		return "", exec.ErrNotFound
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{filepath.Join(home, ".local", "bin"), filepath.Join(home, ".bun", "bin"), "/opt/homebrew/bin", "/usr/local/bin"} {
		if path, err := findAgentInDir(dir, name); err == nil {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}

const reportSchema = `{"type":"object","additionalProperties":false,"required":["batch_id","cards"],"properties":{"batch_id":{"type":"string"},"cards":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"object","additionalProperties":false,"required":["start","end","title","summary","category","evidence"],"properties":{"start":{"type":"string"},"end":{"type":"string"},"title":{"type":"string"},"summary":{"type":"string"},"category":{"type":"string"},"evidence":{"type":"array","minItems":1,"items":{"type":"string"}}}}}}}`

type cappedBuffer struct {
	bytes.Buffer
	max      int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		b.overflow = true
		return 0, errors.New("CLI output limit exceeded")
	}
	return b.Buffer.Write(p)
}

type AgentTrace struct {
	Response, Stderr, Kind string
	ExitCode               *int
	secrets                []string
}
type tailBuffer struct {
	data []byte
	max  int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		b.data = append([]byte(nil), b.data[len(b.data)-b.max:]...)
	}
	return n, nil
}
func (b *tailBuffer) String() string { return string(b.data) }

func parseAnalysis(b Batch, data []byte) (any, error) {
	if b.SchemaVersion == 2 {
		return decodeInterpretation(b, data)
	}
	return parseAgentReport(data)
}

// Images and prompts are private temporary inputs. Agent arguments never go
// through a shell; process groups bound timeout/stop to all descendants.
func runAgent(ctx context.Context, name, path, dir string, b Batch, images []string, timeout int, inheritLoginEnv bool, effort string) (trace AgentTrace, err error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	trace.Kind = "setup_error"
	meta := inputMetadata(b)
	prompt := instructionsForBatch(b) + "\n" + string(meta)
	var args []string
	var input []byte
	switch name {
	case "codex":
		schema := filepath.Join(dir, "schema.json")
		if err := os.WriteFile(schema, []byte(codexSchemaForBatch(b)), 0600); err != nil {
			return trace, err
		}
		args = []string{"exec", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "--disable", "shell_tool", "--disable", "unified_exec", "--disable", "apps", "--disable", "browser_use", "--disable", "computer_use", "--disable", "multi_agent", "--disable", "code_mode_host", "--disable", "hooks", "--disable", "plugins", "--disable", "memories", "--disable", "skill_search", "--disable", "code_mode", "--config", "mcp_servers={}", "--output-schema", schema, "--output-last-message", filepath.Join(dir, "result.json"), "--color", "never"}
		if effort != "" && effort != "inherit" {
			args = append(args, "--config", `model_reasoning_effort="`+effort+`"`)
		}
		for _, image := range images {
			args = append(args, "--image", image)
		}
		args = append(args, "-")
		input = []byte(prompt)
	case "claude":
		args = []string{"--print", "--safe-mode", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--disable-slash-commands", "--no-session-persistence", "--settings", `{"disableAllHooks":true}`, "--max-turns", "1", "--system-prompt", "你是屏幕活动证据整理器。只根据附带截图和元数据返回指定结构的 JSON，不调用工具。"}
		if effort != "" && effort != "inherit" {
			args = append(args, "--effort", effort)
		}
		parts := []map[string]any{{"type": "text", "text": prompt}}
		for _, path := range images {
			data, err := os.ReadFile(path)
			if err != nil {
				return trace, err
			}
			parts = append(parts, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(data)}})
		}
		event := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": parts}}
		input, _ = json.Marshal(event)
		input = append(input, '\n')
	case "hermes":
		collage := filepath.Join(dir, "collage.jpg")
		if err := contactSheet(images, collage); err != nil {
			return trace, err
		}
		prompt += "\n附图按行排列，每行最多两张：第 i 张对应 frames[i]。拼图只用于本批次视觉输入。"
		args = []string{"chat", "--query-file", "-", "--oneshot", "--quiet", "--format", "stream-json", "--image", collage, "--toolsets", "none", "--max-turns", "1", "--run-budget", fmt.Sprint(timeout), "--ignore-rules", "--source", "tool"}
		if effort != "" && effort != "inherit" {
			args = append(args, "--reasoning", effort)
		}
		input = []byte(prompt)
	default:
		return trace, errors.New("agent has no verified image adapter")
	}
	cmd, commandErr := imageAgentCommand(ctx, name, path, args)
	if commandErr != nil {
		return trace, commandErr
	}
	cmd.Dir = dir
	home, _ := os.UserHomeDir()
	env := os.Environ()
	if inheritLoginEnv {
		env = loginAgentEnvironment(ctx, name, env)
	}
	trace.secrets = environmentSecrets(env)
	if name == "codex" {
		credentialNames := codexCredentialNames(env)
		for _, pair := range env {
			k, v, ok := strings.Cut(pair, "=")
			if ok && credentialNames[k] {
				trace.secrets = append(trace.secrets, v)
			}
		}
	}
	extraPath := []string{filepath.Join(home, ".local", "bin"), filepath.Join(home, ".bun", "bin")}
	if platform.IsWindows {
		extraPath = append(extraPath, filepath.Join(os.Getenv("APPDATA"), "npm"), filepath.Join(home, "scoop", "shims"))
	} else {
		extraPath = append(extraPath, "/opt/homebrew/bin", "/usr/local/bin")
	}
	cmd.Env = append(env, "PATH="+os.Getenv("PATH")+string(os.PathListSeparator)+strings.Join(extraPath, string(os.PathListSeparator)))
	cmd.Stdin = bytes.NewReader(input)
	cmd.WaitDelay = 2 * time.Second
	stdout := &cappedBuffer{max: 2 << 20}
	cmd.Stdout = stdout
	stderr := &tailBuffer{max: 16 << 10}
	cmd.Stderr = stderr
	callErr := platform.RunProcess(cmd)
	trace.Stderr = stderr.String()
	trace.Response = string(stdout.Bytes())
	if callErr != nil {
		trace.Kind = "cli_exit"
		var exit *exec.ExitError
		if errors.As(callErr, &exit) {
			code := exit.ExitCode()
			trace.ExitCode = &code
		}
		if ctx.Err() == context.DeadlineExceeded {
			trace.Kind = "timeout"
		} else if ctx.Err() != nil {
			trace.Kind = "canceled"
		} else if stdout.overflow {
			trace.Kind = "output_limit"
		}
		return trace, fmt.Errorf("%s: %s", name, trace.Kind)
	}
	code := 0
	trace.ExitCode = &code
	trace.Kind = "missing_result"

	if name == "codex" {
		data, err := os.ReadFile(filepath.Join(dir, "result.json"))
		if err != nil || len(data) > 1<<20 {
			return trace, errors.New("agent result unavailable")
		}
		trace.Response = string(data)
		trace.Kind = ""
		return trace, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event struct {
			Structured json.RawMessage `json:"structured_output"`
			Type       string          `json:"type"`
			Result     string          `json:"result"`
			Text       string          `json:"text"`
			IsError    bool            `json:"is_error"`
			ExitCode   int             `json:"exit_code"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Type == "result" {
			if event.IsError || event.ExitCode != 0 {
				trace.Kind = "agent_error"
				return trace, errors.New("agent reported failure")
			}
			if name == "claude" {
				if len(event.Structured) > 0 && string(event.Structured) != "null" {
					trace.Response = string(event.Structured)
					trace.Kind = ""
					return trace, nil
				}
				trace.Response = event.Result
				trace.Kind = ""
				return trace, nil
			}
			trace.Response = event.Text
			trace.Kind = ""
			return trace, nil
		}
	}
	return trace, errors.New("agent returned no complete result")
}

func (s *Store) analyzeAgent(ctx context.Context, id string, c Config, selected string) error {
	if !c.Agent.Enabled && selected == "" {
		return errors.New("automatic agent analysis disabled")
	}
	b, err := s.PrepareAnalysis(id)
	if err != nil {
		return err
	}
	if !RetryReady(b, time.Now()) {
		return ErrRetryCooling
	}
	parent := filepath.Join(s.Root, "jobs")
	if err = os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(parent, "analysis-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	images := []string{}
	var total int
	for _, f := range b.Frames {
		path, err := s.FramePath(f)
		if err != nil {
			return errors.New("batch evidence unavailable")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = checkFrameDigest(f, data); err != nil {
			return err
		}
		total += len(data)
		if total > 12<<20 {
			return errors.New("batch images exceed 12 MiB")
		}
		copyPath := filepath.Join(dir, f.ID+".jpg")
		if err = os.WriteFile(copyPath, data, 0600); err != nil {
			return err
		}
		images = append(images, copyPath)
	}
	order := c.Agent.Order
	if selected != "" {
		order = []string{selected}
	}
	infos := map[string]AgentInfo{}
	for _, info := range Agents() {
		infos[info.Name] = info
	}
	attempted := false
	lastReason := "no_compatible_local_agent"
	for _, name := range order {
		info := infos[name]
		if !info.Installed || !info.Supported {
			continue
		}
		reserved, reserveErr := s.reserveAttempt(id, c, time.Now(), attempted)
		if reserveErr != nil {
			return reserveErr
		}
		attempted = true
		started := time.Now()
		requestID := ID(started)
		event := AnalysisLog{Event: "request", RequestID: requestID, BatchID: id, Source: "agent:" + name, Attempt: reserved.Attempts, Payload: map[string]any{"input": logPayload(b), "effort": c.Agent.Effort, "timeout_seconds": c.Agent.TimeoutSeconds}}
		secrets := environmentSecrets(os.Environ())
		s.LogAnalysis(c, event, secrets)
		trace, callErr := runAgent(ctx, name, info.Path, dir, b, images, c.Agent.TimeoutSeconds, c.Agent.InheritLoginEnv, c.Agent.Effort)
		event.Event = "response"
		event.Payload = nil
		event.ElapsedMS = time.Since(started).Milliseconds()
		event.Response = trace.Response
		event.Stderr = trace.Stderr
		event.ExitCode = trace.ExitCode
		event.ErrorKind = trace.Kind
		if callErr == nil {
			if _, parseErr := parseAnalysis(b, []byte(trace.Response)); parseErr != nil {
				callErr = parseErr
				event.ErrorKind = "invalid_json"
			} else if err = s.CommitAnalysis(id, []byte(trace.Response), "agent:"+name, ""); err != nil {
				callErr = err
				event.ErrorKind = "validation_failed"
			}
		}
		if callErr != nil {
			event.Error = callErr.Error()
		}
		s.LogAnalysis(c, event, append(secrets, trace.secrets...))
		if callErr == nil {
			return nil
		}
		lastReason = name + ":" + event.ErrorKind
		_ = s.Failed(id, lastReason)
		if ctx.Err() != nil {
			return ctx.Err()
		}

	}
	if !attempted {
		_ = s.Failed(id, lastReason)
		return errors.New("no compatible local image agent found")
	}
	return fmt.Errorf("%s; batch queued for retry", lastReason)

}

func parseAgentReport(data []byte) (Report, error) {
	var report Report
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, "```json\n") && strings.HasSuffix(text, "```") {
		text = strings.TrimSuffix(strings.TrimPrefix(text, "```json\n"), "```")
	}
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if err := d.Decode(&report); err != nil {
		return report, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return report, errors.New("trailing agent output")
	}
	return report, nil
}

func contactSheet(paths []string, target string) error {
	if len(paths) == 0 {
		return errors.New("no images")
	}
	const width = 960
	images := []image.Image{}
	maxHeight := 0
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		im, _, err := image.Decode(file)
		file.Close()
		if err != nil {
			return err
		}
		height := im.Bounds().Dy() * width / im.Bounds().Dx()
		if height > maxHeight {
			maxHeight = height
		}
		images = append(images, im)
	}
	if maxHeight > 960 {
		maxHeight = 960
	}
	cols := 2
	if len(images) == 1 {
		cols = 1
	}
	rows := (len(images) + cols - 1) / cols
	out := image.NewRGBA(image.Rect(0, 0, width*cols, maxHeight*rows))
	for y := 0; y < out.Bounds().Dy(); y++ {
		for x := 0; x < out.Bounds().Dx(); x++ {
			out.SetRGBA(x, y, color.RGBA{24, 24, 24, 255})
		}
	}
	for i, im := range images {
		bounds := im.Bounds()
		h := bounds.Dy() * width / bounds.Dx()
		w := width
		if h > maxHeight {
			h = maxHeight
			w = bounds.Dx() * h / bounds.Dy()
		}
		x0 := (i % cols) * width
		y0 := (i / cols) * maxHeight
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out.Set(x0+x, y0+y, im.At(bounds.Min.X+x*bounds.Dx()/w, bounds.Min.Y+y*bounds.Dy()/h))
			}
		}
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, out, &jpeg.Options{Quality: 70})
}
