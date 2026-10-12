package service

import (
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
)

func Label(root string) string {
	hash := sha256.Sum256([]byte(root))
	return fmt.Sprintf("io.daylog.agent.%x", hash[:4])
}
func Path(home, root string) string {
	return filepath.Join(home, "Library", "LaunchAgents", Label(root)+".plist")
}
func escape(s string) string {
	var out stringWriter
	_ = xml.EscapeText(&out, []byte(s))
	return string(out)
}

type stringWriter []byte

func (w *stringWriter) Write(p []byte) (int, error) { *w = append(*w, p...); return len(p), nil }

func Plist(executable, root string, login bool) []byte {
	flag := "false"
	if login {
		flag = "true"
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>--data</string><string>%s</string><string>run</string></array>
<key>RunAtLoad</key><%s/>
<key>ProcessType</key><string>Background</string>
<key>Nice</key><integer>10</integer>
<key>LowPriorityIO</key><true/>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>%s</string></dict>
<key>ThrottleInterval</key><integer>30</integer>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, Label(root), escape(executable), escape(root), flag, escape(os.Getenv("PATH")), escape(filepath.Join(root, "daemon.log")), escape(filepath.Join(root, "daemon.log"))))
}

func Install(home, root, executable string, login bool) (string, error) {
	path := Path(home, root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, Plist(executable, root, login), 0600); err != nil {
		return "", err
	}
	return path, nil
}
