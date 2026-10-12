package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"daylog/internal/journal"
	"daylog/internal/native"
	"daylog/internal/service"
	"daylog/internal/update"
)

var version = "0.1.0"

const help = `Daylog — macOS 低占用工作日记

daylog [--data 私有目录] <命令>
  init                       创建配置，不启动记录
  config [--file 文件或-]     查看/替换配置；运行中修改后须重启
  doctor                     检查电源、温度与屏幕权限，不截图
  permission                 请求系统屏幕录制权限
  windows                    列出可捕获窗口 ID（标题可能敏感）
  run [--fixture JPEG]       前台运行；fixture 用于隔离验证
  status                     查询正在运行的后台状态
  stop | pause [--minutes N] | resume
  pending [--limit N]        待分析批次与本地图片路径
  commit --file 文件或-      导入 Agent 生成的证据化记录
  analyze [--batch ID] [--agent 名称] [--allow-battery]  分析一批
  agents                     检测本机 Agent 及适配状态
  update [--source 地址或文件] [--check] [--remember]  自更新
  today [--date YYYY-MM-DD] [--json]      查看日记
  auth set --stdin | auth delete         系统钥匙串凭据，只写不读
  service install [--login] [--dry-run]   安装 LaunchAgent，不立即启动
  service start | stop | uninstall
  version

默认不配置额外 API；启动后尝试已有 Agent（可能使用云端服务）。安装不启动录制。
`

func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "daylog:", err)
		os.Exit(1)
	}
}
func output(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }
func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}

func execute(args []string) error {
	root, err := journal.DefaultRoot()
	if err != nil {
		return err
	}
	filtered := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--data" {
			i++
			if i == len(args) {
				return errors.New("--data needs a directory")
			}
			root = args[i]
		} else {
			filtered = append(filtered, args[i])
		}
	}
	args = filtered
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Print(help)
		return nil
	}
	if args[0] == "version" || args[0] == "--version" {
		return output(map[string]string{"version": version})
	}
	s, err := journal.Open(root)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch args[0] {
	case "init":
		if err = s.Init(); err != nil {
			return err
		}
		return output(map[string]string{"config": filepath.Join(s.Root, "config.json"), "state": "initialized_not_running"})
	case "config":
		f := flags("config")
		file := f.String("file", "", "JSON file or stdin")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		if *file != "" {
			input, closeFn, err := readInput(*file)
			if err != nil {
				return err
			}
			defer closeFn()
			c := journal.DefaultConfig()
			decoder := json.NewDecoder(io.LimitReader(input, 65536))
			decoder.DisallowUnknownFields()
			if err = decoder.Decode(&c); err != nil {
				return errors.New("invalid configuration JSON")
			}
			var trailing any
			if decoder.Decode(&trailing) != io.EOF {
				return errors.New("configuration contains trailing content")
			}
			if err = c.Validate(); err != nil {
				return err
			}
			if err = journal.WriteJSON(filepath.Join(s.Root, "config.json"), c); err != nil {
				return err
			}
		}
		c, err := journal.ReadConfig(s.Root)
		if err != nil {
			return err
		}
		return output(c)
	case "doctor":
		env, err := (native.Mac{}).Environment(ctx)
		if err != nil {
			return err
		}
		c, configErr := journal.ReadConfig(s.Root)
		return output(map[string]any{"version": version, "environment": env, "config_valid": configErr == nil, "policy": journal.PowerPolicy(c, env), "data_dir": s.Root, "note": "权限检查不代表真实截图、续航或安装升级已验证"})
	case "permission":
		return output(map[string]bool{"granted": native.RequestPermission()})
	case "windows":
		windows, err := native.Windows()
		if err != nil {
			return err
		}
		return output(windows)
	case "agents":
		return output(journal.Agents())
	case "update":
		f := flags("update")
		source := f.String("source", "", "release manifest URL or local file")
		checkOnly := f.Bool("check", false, "only inspect")
		remember := f.Bool("remember", false, "remember this update source")
		force := f.Bool("force", false, "explicitly reinstall this version")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		c, err := journal.ReadConfig(s.Root)
		if err != nil {
			return err
		}
		if *source == "" {
			*source = c.Update.Source
		}
		if *source == "" {
			return errors.New("update source is not configured; use --source <release.json URL or file>")
		}
		_, check, err := update.Inspect(ctx, *source, version)
		if err != nil {
			return err
		}
		if *remember {
			c.Update.Source = *source
			if err = journal.WriteJSON(filepath.Join(s.Root, "config.json"), c); err != nil {
				return err
			}
		}
		if *checkOnly || !check.Upgrade && !*force {
			return output(check)
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return err
		}
		app := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
		wasRunning := false
		wasService := service.IsLoaded(s.Root)
		check, err = update.Install(ctx, *source, app, version, *force, func() error {
			if _, probe := journal.SendControl(s.Root, journal.Control{Op: "status"}); probe != nil {
				return nil
			}
			wasRunning = true
			if _, stopErr := journal.SendControl(s.Root, journal.Control{Op: "stop"}); stopErr != nil {
				return stopErr
			}
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if _, probe := journal.SendControl(s.Root, journal.Control{Op: "status"}); probe != nil {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			}
			return errors.New("recording did not stop in time; update refused")
		})
		if err != nil {
			return err
		}
		restarted := false
		if wasRunning && wasService {
			home, _ := os.UserHomeDir()
			if err = service.Start(home, s.Root); err != nil {
				return errors.New("app updated; background restart failed")
			}
			restarted = true
		}
		return output(map[string]any{"state": "updated", "version": check.Available, "restarted": restarted, "manual_restart_needed": wasRunning && !wasService})
	case "run":
		f := flags("run")
		fixture := f.String("fixture", "", "synthetic JPEG fixture")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		c, err := journal.ReadConfig(s.Root)
		if err != nil {
			return err
		}
		var source native.Source = native.Mac{}
		if *fixture != "" {
			data, err := os.ReadFile(*fixture)
			if err != nil {
				return err
			}
			if _, err = journal.FingerprintJPEG(data); err != nil {
				return errors.New("fixture must be an image")
			}
			source = fixtureSource{data: data}
			if c.AI.Enabled {
				return errors.New("fixture runner refuses network-enabled configuration")
			}
			c.Agent.Enabled = false
		}
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)
		r := journal.Runner{Store: s, Config: c, Source: source, Key: func() string { return apiKey(c) }}
		return r.Run(ctx)
	case "status", "stop", "resume", "pause":
		f := flags(args[0])
		minutes := f.Int("minutes", 30, "pause duration, 0 means indefinite")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		if *minutes < 0 || *minutes > 1440 {
			return errors.New("pause minutes must be 0..1440")
		}
		status, err := journal.SendControl(s.Root, journal.Control{Op: args[0], Minutes: *minutes})
		if err != nil {
			return err
		}
		return output(status)
	case "pending":
		f := flags("pending")
		limit := f.Int("limit", 1, "batch limit")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		if *limit < 1 || *limit > 256 {
			return errors.New("limit must be 1..256")
		}
		batches, err := s.Pending(*limit)
		if err != nil {
			return err
		}
		out := []map[string]any{}
		for _, b := range batches {
			paths := []string{}
			for _, frame := range b.Frames {
				path, err := s.FramePath(frame)
				if err != nil {
					return errors.New("pending screenshot evidence unavailable")
				}
				paths = append(paths, path)
			}
			out = append(out, map[string]any{"batch": b, "image_paths": paths})
		}
		return output(out)
	case "commit":
		f := flags("commit")
		file := f.String("file", "-", "report JSON")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		input, closeFn, err := readInput(*file)
		if err != nil {
			return err
		}
		defer closeFn()
		var report journal.Report
		decoder := json.NewDecoder(io.LimitReader(input, 2<<20))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&report); err != nil {
			return errors.New("invalid report JSON")
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return errors.New("report contains trailing content")
		}
		if err = s.Commit(report, "agent"); err != nil {
			return err
		}
		return output(map[string]string{"batch_id": report.BatchID, "state": "committed"})
	case "analyze":
		f := flags("analyze")
		id := f.String("batch", "", "batch ID")
		agent := f.String("agent", "", "choose a local agent instead of configured API")
		allowBattery := f.Bool("allow-battery", false, "explicit battery override")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		c, err := journal.ReadConfig(s.Root)
		if err != nil {
			return err
		}
		env, err := (native.Mac{}).Environment(ctx)
		if err != nil {
			return err
		}
		if !journal.PowerPolicy(c, env).CanAnalyze && !*allowBattery {
			return errors.New("analysis paused by power policy; --allow-battery explicitly overrides it")
		}
		if *id == "" {
			pending, err := s.Pending(1)
			if err != nil {
				return err
			}
			if len(pending) == 0 {
				return output(map[string]string{"state": "no_pending_batches"})
			}
			*id = pending[0].ID
		}
		if c.AI.Enabled && *agent == "" {
			err = s.Analyze(ctx, *id, c, apiKey(c), nil)
		} else {
			err = s.AnalyzeAgent(ctx, *id, c, *agent)
		}
		if err != nil {
			return err
		}
		return output(map[string]string{"batch_id": *id, "state": "analyzed"})
	case "today":
		f := flags("today")
		date := f.String("date", time.Now().Format("2006-01-02"), "local calendar day")
		asJSON := f.Bool("json", false, "JSON output")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		start, err := time.ParseInLocation("2006-01-02", *date, time.Local)
		if err != nil {
			return errors.New("date must be YYYY-MM-DD")
		}
		end := start.AddDate(0, 0, 1)
		reports, err := s.Reports(start, end)
		if err != nil {
			return err
		}
		pending, err := s.Pending(0)
		if err != nil {
			return err
		}
		count := 0
		for _, b := range pending {
			if b.Start.Before(end) && b.End.After(start) {
				count++
			}
		}
		if *asJSON {
			return output(map[string]any{"date": *date, "reports": reports, "pending_batches": count})
		}
		fmt.Printf("# %s\n\n", *date)
		for _, r := range reports {
			for _, card := range r.Cards {
				fmt.Printf("- %s–%s **%s**：%s\n", card.Start.Local().Format("15:04"), card.End.Local().Format("15:04"), card.Title, card.Summary)
			}
		}
		if len(reports) == 0 {
			fmt.Println("暂无已分析记录。")
		}
		if count > 0 {
			fmt.Printf("\n还有 %d 批待分析；以上记录不完整。\n", count)
		}
		return nil
	case "auth":
		if len(args) < 2 {
			return errors.New("use auth set --stdin or auth delete")
		}
		if args[1] == "delete" {
			return native.DeleteSecret()
		}
		if args[1] != "set" || len(args) != 3 || args[2] != "--stdin" {
			return errors.New("API keys are accepted only on stdin")
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 16385))
		if err != nil {
			return err
		}
		value := strings.TrimSpace(string(data))
		if value == "" || len(value) > 16384 {
			return errors.New("invalid API key length")
		}
		if err = native.SetSecret(value); err != nil {
			return err
		}
		return output(map[string]string{"state": "stored_in_keychain"})
	case "service":
		if len(args) < 2 {
			return errors.New("service requires install/start/stop/uninstall")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		switch args[1] {
		case "install":
			f := flags("service install")
			login := f.Bool("login", false, "start at login")
			dry := f.Bool("dry-run", false, "print plist without installing")
			if err = f.Parse(args[2:]); err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if *dry {
				_, err = os.Stdout.Write(service.Plist(exe, s.Root, *login))
				return err
			}
			path, err := service.Install(home, s.Root, exe, *login)
			if err != nil {
				return err
			}
			return output(map[string]any{"plist": path, "state": "installed_not_started", "start_at_login": *login})
		case "start":
			return service.Start(home, s.Root)
		case "stop":
			return service.Stop(s.Root)
		case "uninstall":
			if err = service.Stop(s.Root); err != nil {
				return err
			}
			err = os.Remove(service.Path(home, s.Root))
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		return errors.New("unknown service operation")
	}
	return errors.New("unknown command; use daylog help")
}

func readInput(path string) (io.Reader, func(), error) {
	if path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { f.Close() }, nil
}
func apiKey(c journal.Config) string {
	if c.AI.KeyEnv != "" {
		if value := os.Getenv(c.AI.KeyEnv); value != "" {
			return value
		}
	}
	return native.Secret()
}

type fixtureSource struct{ data []byte }

func (f fixtureSource) Environment(context.Context) (native.Environment, error) {
	return native.Environment{Permission: true, BatteryPercent: 100, App: "fixture"}, nil
}
func (f fixtureSource) Capture(context.Context, native.CaptureOptions) ([]byte, error) {
	return f.data, nil
}
