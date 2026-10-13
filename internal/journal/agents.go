package journal

import (
	"bufio"
	"bytes"
	"context"
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
	"syscall"
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
			path := filepath.Join(dir, name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
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
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}

const reportSchema = `{"type":"object","additionalProperties":false,"required":["batch_id","cards"],"properties":{"batch_id":{"type":"string"},"cards":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"object","additionalProperties":false,"required":["start","end","title","summary","category","evidence"],"properties":{"start":{"type":"string"},"end":{"type":"string"},"title":{"type":"string"},"summary":{"type":"string"},"category":{"type":"string"},"evidence":{"type":"array","minItems":1,"items":{"type":"string"}}}}}}}`

type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("CLI output limit exceeded")
	}
	return b.Buffer.Write(p)
}

// Images and prompts are private temporary inputs. Agent arguments never go
// through a shell; process groups bound timeout/stop to all descendants.
func runAgent(ctx context.Context, name, path, dir string, b Batch, images []string, timeout int, inheritLoginEnv bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	meta := inputMetadata(b)
	prompt := instructionsForBatch(b) + "\n不要调用任何工具，仅根据附图返回 JSON。\n" + string(meta)
	var args []string
	var input []byte
	switch name {
	case "codex":
		schema := filepath.Join(dir, "schema.json")
		if err := os.WriteFile(schema, []byte(schemaForBatch(b)), 0600); err != nil {
			return nil, err
		}
		args = []string{"exec", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "--disable", "shell_tool", "--disable", "unified_exec", "--disable", "apps", "--disable", "browser_use", "--disable", "computer_use", "--disable", "multi_agent", "--disable", "code_mode_host", "--config", "mcp_servers={}", "--output-schema", schema, "--output-last-message", filepath.Join(dir, "result.json"), "--color", "never"}
		for _, image := range images {
			args = append(args, "--image", image)
		}
		args = append(args, "-")
		input = []byte(prompt)
	case "claude":
		args = []string{"--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--disable-slash-commands", "--no-session-persistence", "--settings", `{"disableAllHooks":true}`}
		parts := []map[string]any{{"type": "text", "text": prompt}}
		for _, path := range images {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			parts = append(parts, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(data)}})
		}
		event := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": parts}}
		input, _ = json.Marshal(event)
		input = append(input, '\n')
	case "hermes":
		collage := filepath.Join(dir, "collage.jpg")
		if err := contactSheet(images, collage); err != nil {
			return nil, err
		}
		prompt += "\n附图按行排列，每行最多两张：第 i 张对应 frames[i]。拼图只用于本批次视觉输入。"
		args = []string{"chat", "--query-file", "-", "--oneshot", "--quiet", "--format", "stream-json", "--image", collage, "--toolsets", "none", "--max-turns", "1", "--run-budget", fmt.Sprint(timeout), "--ignore-rules", "--source", "tool"}
		input = []byte(prompt)
	default:
		return nil, errors.New("agent has no verified image adapter")
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	home, _ := os.UserHomeDir()
	env := os.Environ()
	if inheritLoginEnv {
		env = loginAgentEnvironment(ctx, name, env)
	}
	cmd.Env = append(env, "PATH="+os.Getenv("PATH")+":"+filepath.Join(home, ".local", "bin")+":"+filepath.Join(home, ".bun", "bin")+":/opt/homebrew/bin:/usr/local/bin")
	cmd.Stdin = bytes.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	stdout := &cappedBuffer{max: 2 << 20}
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, errors.New("agent invocation failed or timed out")
	}
	if name == "codex" {
		data, err := os.ReadFile(filepath.Join(dir, "result.json"))
		if err != nil || len(data) > 1<<20 {
			return nil, errors.New("agent result unavailable")
		}
		return data, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event struct {
			Type     string `json:"type"`
			Result   string `json:"result"`
			Text     string `json:"text"`
			IsError  bool   `json:"is_error"`
			ExitCode int    `json:"exit_code"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Type == "result" {
			if event.IsError || event.ExitCode != 0 {
				return nil, errors.New("agent reported failure")
			}
			if name == "claude" {
				return []byte(event.Result), nil
			}
			return []byte(event.Text), nil
		}
	}
	return nil, errors.New("agent returned no complete result")
}

func (s *Store) AnalyzeAgent(ctx context.Context, id string, c Config, selected string) error {
	if !c.Agent.Enabled && selected == "" {
		return errors.New("automatic agent analysis disabled")
	}
	lock, err := os.OpenFile(filepath.Join(s.Root, "analysis.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another analysis is running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	b, err := s.PrepareAnalysis(id)
	if err != nil {
		return err
	}
	if b.Attempts >= 3 || b.LastAttempt != nil && time.Since(*b.LastAttempt) < 15*time.Minute {
		return errors.New("batch attempts exhausted or cooling down")
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
	for _, name := range order {
		info := infos[name]
		if !info.Installed || !info.Supported {
			continue
		}
		if _, err = s.reserveAttempt(id, c, time.Now(), attempted); err != nil {
			return err
		}
		attempted = true
		data, callErr := runAgent(ctx, name, info.Path, dir, b, images, c.Agent.TimeoutSeconds, c.Agent.InheritLoginEnv)
		if callErr == nil {
			if err = s.CommitAnalysis(id, data, "agent:"+name, ""); err == nil {
				return nil
			}
		}
		_ = s.Failed(id, "agent_failed:"+name)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if !attempted {
		_ = s.Failed(id, "no_compatible_local_agent")
		return errors.New("no compatible local image agent found")
	}
	return errors.New("local agents failed; batch remains pending")
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
