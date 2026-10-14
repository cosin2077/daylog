package journal

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// LaunchAgents do not read shell profiles. Query the existing login profile
// only when analyzing, then retain relevant provider variables in memory.
// Neither screenshot text nor CLI arguments are interpolated into this shell.
func loginAgentEnvironment(ctx context.Context, agent string, base []string) []string {
	shell := "/bin/zsh"
	for _, entry := range base {
		if value, ok := strings.CutPrefix(entry, "SHELL="); ok && filepath.IsAbs(value) {
			name := filepath.Base(value)
			if name == "zsh" || name == "bash" {
				shell = value
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-ilc", "printf '\\000DAYLOG_ENV_BEGIN\\000'; /usr/bin/env -0")
	cmd.Env = base
	cmd.Dir, _ = os.UserHomeDir()
	cmd.Stdin = nil
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	out := &cappedBuffer{max: 256 << 10}
	cmd.Stdout = out
	if cmd.Run() != nil {
		return base // File-based auth can still work without a shell profile.
	}
	marker := []byte("\x00DAYLOG_ENV_BEGIN\x00")
	index := bytes.Index(out.Bytes(), marker)
	if index < 0 {
		return base
	}
	env := append([]string(nil), base...)
	indices := make(map[string]int, len(env))
	for i, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		indices[name] = i
	}
	for _, entry := range strings.Split(string(out.Bytes()[index+len(marker):]), "\x00") {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || value == "" || !agentEnvironmentVariable(agent, name) {
			continue
		}
		if i, exists := indices[name]; exists {
			_, current, _ := strings.Cut(env[i], "=")
			if current == "" {
				env[i] = entry
			}
			continue // Explicit caller configuration takes precedence.
		}
		indices[name] = len(env)
		env = append(env, entry)
	}
	if agent == "codex" {
		names := codexCredentialNames(env)
		for _, entry := range strings.Split(string(out.Bytes()[index+len(marker):]), "\x00") {
			name, value, ok := strings.Cut(entry, "=")
			if !ok || value == "" || !names[name] {
				continue
			}
			if i, exists := indices[name]; exists {
				_, current, _ := strings.Cut(env[i], "=")
				if current == "" {
					env[i] = entry
				}
			} else {
				indices[name] = len(env)
				env = append(env, entry)
			}
		}
	}
	return env
}

var providerEnvKey = regexp.MustCompile(`^\s*env_key\s*=\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*(?:#.*)?$`)

// Read only declared credential variable names; values remain in process memory.
func codexCredentialNames(env []string) map[string]bool {
	names := map[string]bool{}
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".codex")
	for _, pair := range env {
		if value, ok := strings.CutPrefix(pair, "CODEX_HOME="); ok && value != "" {
			root = value
		}
	}
	f, err := os.Open(filepath.Join(root, "config.toml"))
	if err != nil {
		return names
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return names
	}
	provider := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			provider = strings.HasPrefix(line, "[model_providers.")
		}
		if !provider {
			continue
		}
		if match := providerEnvKey.FindStringSubmatch(line); match != nil {
			names[match[1]] = true
		}
	}
	return names
}

func agentEnvironmentVariable(agent, name string) bool {
	switch name {
	case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS":
		return true
	}
	switch agent {
	case "claude":
		return strings.HasPrefix(name, "ANTHROPIC_") || name == "CLAUDE_CONFIG_DIR" || name == "CLAUDE_CODE_OAUTH_TOKEN" || name == "CLAUDE_CODE_MAX_CONTEXT_TOKENS" || name == "CLAUDE_CODE_EFFORT_LEVEL"
	case "codex":
		return strings.HasPrefix(name, "OPENAI_") || name == "CODEX_HOME"
	case "hermes":
		return strings.HasPrefix(name, "HERMES_") || strings.HasPrefix(name, "OPENAI_") || strings.HasPrefix(name, "ANTHROPIC_") || name == "OPENROUTER_API_KEY" || name == "GROQ_API_KEY" || name == "GEMINI_API_KEY"
	}
	return false
}
