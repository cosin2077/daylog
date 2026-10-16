package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"daylog/internal/journal"
)

const configHelp = `Daylog — 配置

默认自动使用本机 Agent，不需要额外配置。

  daylog config --notifications=false
      关闭系统失败通知；status、报告和日志仍显示失败信息
  daylog config --profile balanced|battery|detailed
      选择均衡、省电或精细档；不改变模型和隐私设置
  daylog config
      查看中文配置概览（隐藏密钥内容）
  daylog config --json
      导出完整 JSON 配置，可能含明文密钥
  daylog config --endpoint https://api.example.com/v1/chat/completions --model MODEL --api-key KEY
      使用支持图片的 OpenAI-compatible Chat Completions API
  daylog config --api-key KEY
      将明文 API Key 保存到 config.json 的 ai.api_key
  daylog config --api-key-stdin
      从标准输入接收 API Key，保存到 macOS 钥匙串，不回显
  daylog config --key-env MY_API_KEY
      改用指定环境变量中的密钥
  daylog config --agent auto
      切回本机 Agent；可用 --agent claude,hermes 指定优先顺序
  daylog config --agent-timeout 240
      设置单次 Agent 调用时限（10–600 秒，默认 180 秒）；不改变模型或顺序
  daylog config --api-fallback --fallback-models MODEL2
      本机 Agent 全部失败后才使用已配置 API，主模型失败再试备用模型

也可直接编辑 ~/.daylog/config.json，或用 --file FILE 导入完整配置。
JSON 支持 ai.api_key 明文密钥；环境变量和钥匙串也可选。
密钥读取顺序：ai.api_key → key_env 指定的环境变量 → 钥匙串。
--file - 与 --api-key-stdin 不能同时使用。
正在记录时，修改配置后用 daylog stop、daylog start 重新启动以生效。
`

func configure(s *journal.Store, args []string, stdin io.Reader, stdout io.Writer, setSecret func(string) error) error {
	f := flags("config")
	notifications := f.Bool("notifications", true, "enable system notifications for persistent analysis failures")
	profile := f.String("profile", "", "balanced, battery or detailed")
	file := f.String("file", "", "complete JSON config file or stdin")
	endpoint := f.String("endpoint", "", "full Chat Completions endpoint")
	model := f.String("model", "", "vision model name")
	plainKey := f.String("api-key", "", "plain API key saved in config.json")
	keyEnv := f.String("key-env", "", "API key environment variable name")
	keyStdin := f.Bool("api-key-stdin", false, "read API key from stdin into Keychain")
	agent := f.String("agent", "", "auto or comma-separated agent order")
	agentTimeout := f.Int("agent-timeout", 0, "per-agent timeout in seconds, 10..600")
	apiFallback := f.Bool("api-fallback", false, "try configured API after local agents fail")
	fallbackModels := f.String("fallback-models", "", "comma-separated fallback API models")
	asJSON := f.Bool("json", false, "JSON output after editing")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("用法：daylog config -h")
	}
	changed := map[string]bool{}
	f.Visit(func(v *flag.Flag) { changed[v.Name] = true })
	customAPI := changed["endpoint"] || changed["model"]
	if changed["agent"] && !*apiFallback && (customAPI || *keyStdin || changed["key-env"] || changed["api-key"]) {
		return errors.New("请分别配置自定义 API 或选择本机 Agent，不要同时指定两者")
	}
	if *file == "-" && *keyStdin {
		return errors.New("配置 JSON 和 API Key 不能共用标准输入")
	}
	if *keyStdin && (changed["api-key"] || changed["key-env"]) {
		return errors.New("--api-key-stdin 不能与 --api-key 或 --key-env 同时使用")
	}
	c := journal.DefaultConfig()
	if *file != "" {
		input := stdin
		if *file != "-" {
			opened, err := os.Open(*file)
			if err != nil {
				return err
			}
			defer opened.Close()
			input = opened
		}
		d := json.NewDecoder(io.LimitReader(input, 65536))
		d.DisallowUnknownFields()
		if err := d.Decode(&c); err != nil {
			return errors.New("配置 JSON 无效，请检查字段和语法")
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			return errors.New("配置 JSON 含多余内容")
		}
	} else {
		var err error
		c, err = journal.ReadConfig(s.Root)
		if err != nil {
			return err
		}
	}
	if changed["notifications"] {
		c.Diagnostics.FailureNotifications = *notifications
	}
	if changed["agent-timeout"] {
		if *agentTimeout < 10 || *agentTimeout > 600 {
			return errors.New("Agent 调用时限必须为 10–600 秒")
		}
		c.Agent.TimeoutSeconds = *agentTimeout
	}
	if changed["profile"] {
		if err := c.ApplyProfile(*profile); err != nil {
			return err
		}
	}
	if changed["endpoint"] {
		c.AI.Endpoint = strings.TrimSpace(*endpoint)
	}
	if changed["model"] {
		c.AI.Model = strings.TrimSpace(*model)
	}
	if changed["key-env"] {
		c.AI.KeyEnv = strings.TrimSpace(*keyEnv)
		c.AI.APIKey = ""
	}
	if changed["api-key"] {
		c.AI.APIKey = strings.TrimSpace(*plainKey)
	}
	if customAPI {
		c.AI.Enabled = true
	}
	if changed["agent"] {
		if *agent == "auto" {
			c.Agent.Order = journal.DefaultConfig().Agent.Order
		} else {
			c.Agent.Order = nil
			for _, name := range strings.Split(*agent, ",") {
				name = strings.TrimSpace(name)
				if name != "claude" && name != "codex" && name != "hermes" {
					return errors.New("agent 只能为 auto 或 claude,codex,hermes 的顺序列表")
				}
				c.Agent.Order = append(c.Agent.Order, name)
			}
		}
		c.Agent.Enabled = true
		c.AI.Enabled = false
	}
	if changed["api-fallback"] {
		c.AI.FallbackAfterAgents = *apiFallback
		if *apiFallback {
			c.AI.Enabled = true
			c.Agent.Enabled = true
		}
	}
	if changed["fallback-models"] {
		c.AI.FallbackModels = nil
		if strings.TrimSpace(*fallbackModels) != "" {
			for _, model := range strings.Split(*fallbackModels, ",") {
				c.AI.FallbackModels = append(c.AI.FallbackModels, strings.TrimSpace(model))
			}
		}
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if *keyStdin {
		data, err := io.ReadAll(io.LimitReader(stdin, 16385))
		if err != nil {
			return err
		}
		key := strings.TrimSpace(string(data))
		if key == "" || len(key) > 16384 || strings.ContainsAny(key, "\x00\r\n") {
			return errors.New("API Key 必须是非空单行文本，且不得超过 16 KiB")
		}
		if err = setSecret(key); err != nil {
			return err
		}
		c.AI.APIKey = ""
		c.AI.KeyEnv = ""
	}
	edited := changed["notifications"] || changed["agent-timeout"] || changed["api-fallback"] || changed["fallback-models"] || changed["profile"] || *file != "" || customAPI || changed["key-env"] || changed["agent"] || changed["api-key"] || *keyStdin
	if edited {
		if err := journal.WriteJSON(filepath.Join(s.Root, "config.json"), c); err != nil {
			return err
		}
	}
	if *asJSON {
		return json.NewEncoder(stdout).Encode(c)
	}
	if !edited {
		writeConfig(stdout, s.Root, c)
		return nil
	}
	if edited {
		fmt.Fprintln(stdout, "已保存配置："+filepath.Join(s.Root, "config.json"))
	}
	if *keyStdin {
		fmt.Fprintln(stdout, "API Key 已保存到 macOS 钥匙串。")
	}
	if _, err := journal.SendControl(s.Root, journal.Control{Op: "status"}); err == nil {
		fmt.Fprintln(stdout, "执行 daylog stop 后再 daylog start 应用配置。")
	}
	return nil
}
