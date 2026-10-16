package journal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func findAgentInDir(dir, name string) (string, error) {
	extensions := []string{""}
	if runtime.GOOS == "windows" {
		extensions = []string{".exe", ".cmd"}
	}
	for _, ext := range extensions {
		p := filepath.Join(dir, name+ext)
		if i, e := os.Stat(p); e == nil && !i.IsDir() && (runtime.GOOS == "windows" || i.Mode()&0111 != 0) {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

func imageAgentCommand(ctx context.Context, name, path string, args []string) (*exec.Cmd, error) {
	if runtime.GOOS != "windows" || strings.EqualFold(filepath.Ext(path), ".exe") {
		return exec.CommandContext(ctx, path, args...), nil
	}
	// Resolve known npm launchers to node directly. Never interpolate prompts,
	// schema JSON or image paths into cmd.exe or PowerShell command text.
	var entry string
	switch name {
	case "claude":
		entry = filepath.Join(filepath.Dir(path), "node_modules", "@anthropic-ai", "claude-code", "cli.js")
	case "codex":
		entry = filepath.Join(filepath.Dir(path), "node_modules", "@openai", "codex", "bin", "codex.js")
	default:
		return nil, errors.New("Windows agent requires a native executable")
	}
	if i, e := os.Stat(entry); e != nil || i.IsDir() {
		return nil, errors.New("unsupported Windows agent launcher; use the official native or npm install")
	}
	node, e := exec.LookPath("node.exe")
	if e != nil {
		return nil, errors.New("npm agent requires installed Node.js")
	}
	return exec.CommandContext(ctx, node, append([]string{entry}, args...)...), nil
}
