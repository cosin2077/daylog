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

  daylog config --profile balanced|battery|detailed
      选择均衡、省电或精细档；不改变模型和隐私设置
  daylog config
      查看当前配置
  daylog config --endpoint https://api.example.com/v1/chat/completions --model MODEL --api-key KEY
      使用支持图片的 OpenAI-compatible Chat Completions API
  daylog config --api-key KEY
      将明文 API Key 保存到 config.json 的 ai.api_key
  daylog config --api-key-stdin
      从标准输入接收 API Key，保存到 macOS 钥匙串，不回显
  daylog config --key-env MY_API_KEY
      改用指定环境变量中的密钥
  daylog config --agent auto
      切回本机 Agent；也可指定 claude、codex 或 hermes

也可直接编辑 ~/.daylog/config.json，或用 --file FILE 导入完整配置。
JSON 支持 ai.api_key 明文密钥；环境变量和钥匙串也可选。
密钥读取顺序：ai.api_key → key_env 指定的环境变量 → 钥匙串。
--file - 与 --api-key-stdin 不能同时使用。
正在记录时，修改配置后用 daylog stop、daylog start 重新启动以生效。
`

func configure(s *journal.Store, args []string, stdin io.Reader, stdout io.Writer, setSecret func(string) error) error {
	f := flags("config")
	profile := f.String("profile", "", "balanced, battery or detailed")
	file := f.String("file", "", "complete JSON config file or stdin")
	endpoint := f.String("endpoint", "", "full Chat Completions endpoint")
	model := f.String("model", "", "vision model name")
	plainKey := f.String("api-key", "", "plain API key saved in config.json")
	keyEnv := f.String("key-env", "", "API key environment variable name")
	keyStdin := f.Bool("api-key-stdin", false, "read API key from stdin into Keychain")
	agent := f.String("agent", "", "auto, claude, codex or hermes")
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
	if changed["agent"] && (customAPI || *keyStdin || changed["key-env"] || changed["api-key"]) {
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
		switch *agent {
		case "auto":
			c.Agent.Order = journal.DefaultConfig().Agent.Order
		case "claude", "codex", "hermes":
			c.Agent.Order = []string{*agent}
		default:
			return errors.New("agent 只能是 auto、claude、codex 或 hermes")
		}
		c.Agent.Enabled = true
		c.AI.Enabled = false
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
	edited := changed["profile"] || *file != "" || customAPI || changed["key-env"] || changed["agent"] || changed["api-key"] || *keyStdin
	if edited {
		if err := journal.WriteJSON(filepath.Join(s.Root, "config.json"), c); err != nil {
			return err
		}
	}
	if (!edited && !*keyStdin) || *asJSON {
		return json.NewEncoder(stdout).Encode(c)
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
