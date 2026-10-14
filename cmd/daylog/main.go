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

var version = "0.4.2"

const help = `Daylog — 自动记录今天做了什么

  daylog start    开始记录，自动准备环境；首次按系统提示授权
  daylog today    查看今天的日记
  daylog report   打开日／周／月可视化报告
  daylog status   查看运行状态、采集数量和待整理记录
  daylog stop     停止记录
  daylog config   查看或配置模型（可选，使用 config -h 查看方法）
  daylog update   更新版本
  daylog -h       显示帮助（直接运行 daylog 也可以）

配置、截图和日记都在 ~/.daylog。
自动使用本机 Agent 的模型配置，分析时可能调用云端服务。
默认输出便于阅读；Agent / 脚本查询请加 --json。
更多设置与排障：daylog help --advanced
`

const advancedHelp = `Daylog — 高级操作

daylog [--data 私有目录] <命令>
  init                       创建配置，不启动记录
  config [选项]              查看/修改配置；config -h 显示模型配置方法
  doctor                     检查电源、温度与屏幕权限，不截图
  permission                 请求系统屏幕录制权限
  windows                    列出可捕获窗口 ID（标题可能敏感）
  run [--fixture JPEG]       前台运行；fixture 用于隔离验证
  status [--json]             查看中文状态概览；--json 供 Agent / 脚本使用
  stop | pause [--minutes N] | resume
  pending [--limit N]        待分析批次与本地图片路径
  commit --file 文件或-      导入 Agent 生成的证据化记录
  analyze [--batch ID] [--agent 名称] [--retry] [--allow-battery]  分析一批
  agents                     检测本机 Agent 及适配状态
  update [--source 地址或文件] [--check] [--remember]  自更新
  today [--date YYYY-MM-DD] [--json]      查看日记
  report [--period day|week|month] [--date YYYY-MM-DD] [--no-open] [--json]
                             生成日／周／月报告；默认打开离线 HTML
  prompt                     查看截图分析使用的提示词
  auth set --stdin | auth delete         系统钥匙串凭据，只写不读
  service install [--login] [--dry-run]   安装 LaunchAgent，不立即启动
  service start | stop | uninstall
  version

结果命令默认显示中文，加 --json 输出结构化结果。
service install --dry-run 默认导出原始 plist；run 为前台进程，不输出结果文档。

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
	customRoot := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--data" {
			i++
			if i == len(args) {
				return errors.New("--data needs a directory")
			}
			root = args[i]
			customRoot = true
		} else {
			filtered = append(filtered, args[i])
		}
	}
	args = filtered
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if len(args) > 1 && args[1] == "--advanced" {
			fmt.Print(advancedHelp)
		} else {
			fmt.Print(help)
		}
		return nil
	}
	if args[0] == "config" && len(args) == 2 && (args[1] == "-h" || args[1] == "--help") {
		fmt.Print(configHelp)
		return nil
	}
	if args[0] == "report" && len(args) == 2 && (args[1] == "-h" || args[1] == "--help") {
		fmt.Print(reportHelp)
		return nil
	}
	if args[0] == "version" || args[0] == "--version" {
		asJSON, err := jsonOption("version", args[1:])
		if err != nil {
			return err
		}
		return result(asJSON, map[string]string{"version": version}, "Daylog "+version)
	}
	if args[0] == "prompt" {
		asJSON, err := jsonOption("prompt", args[1:])
		if err != nil {
			return err
		}
		if asJSON {
			return output(map[string]string{"prompt": journal.AnalysisPrompt()})
		}
		fmt.Print(journal.AnalysisPrompt())
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if !customRoot {
		if err = migrateDefaultData(ctx, root, args[0]); err != nil {
			return err
		}
	}
	s, err := journal.Open(root)
	if err != nil {
		return err
	}
	if err = s.Init(); err != nil {
		return err
	}
	if handled, nativeErr := nativeCommand(ctx, s.Root, args); handled {
		return nativeErr
	}
	switch args[0] {
	case "report":
		return reportCommand(ctx, s, args[1:])
	case "start":
		f := flags("start")
		login := f.Bool("login", false, "also start after login")
		asJSON := f.Bool("json", false, "JSON output")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("用法：daylog start")
		}
		return startRecording(ctx, s, *login, *asJSON)
	case "stop":
		f := flags("stop")
		asJSON := f.Bool("json", false, "JSON output")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("用法：daylog stop")
		}
		if err = stopRuntime(ctx, s.Root); err != nil {
			return err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		if err = removeServiceFile(home, s.Root); err != nil {
			return err
		}
		if *asJSON {
			return output(map[string]string{"state": "stopped"})
		}
		fmt.Println("已停止记录。再次开始：daylog start")
		return nil
	case "init":
		asJSON, err := jsonOption("init", args[1:])
		if err != nil {
			return err
		}
		if err = s.Init(); err != nil {
			return err
		}
		return result(asJSON, map[string]string{"config": filepath.Join(s.Root, "config.json"), "state": "initialized_not_running"}, "配置已就绪："+filepath.Join(s.Root, "config.json")+"\n运行 daylog start 开始记录。")
	case "config":
		return configure(s, args[1:], os.Stdin, os.Stdout, native.SetSecret)
	case "doctor":
		asJSON, err := jsonOption("doctor", args[1:])
		if err != nil {
			return err
		}
		env, err := (native.Mac{}).Environment(ctx)
		if err != nil {
			return err
		}
		c, configErr := journal.ReadConfig(s.Root)
		if asJSON {
			return output(map[string]any{"version": version, "environment": env, "config_valid": configErr == nil, "policy": journal.PowerPolicy(c, env), "data_dir": s.Root, "note": "权限检查不代表真实截图、续航或安装升级已验证"})
		}
		writeDoctor(os.Stdout, s.Root, env, c, configErr == nil)
		return nil
	case "permission":
		asJSON, err := jsonOption("permission", args[1:])
		if err != nil {
			return err
		}
		granted := native.RequestPermission()
		message := "尚未获得录屏权限，请在 macOS 系统设置中授权 Daylog。"
		if granted {
			message = "录屏权限已开启。运行 daylog start 开始记录。"
		}
		return result(asJSON, map[string]bool{"granted": granted}, message)
	case "windows":
		asJSON, err := jsonOption("windows", args[1:])
		if err != nil {
			return err
		}
		windows, err := native.Windows()
		if err != nil {
			return err
		}
		if asJSON {
			return output(windows)
		}
		writeWindows(os.Stdout, windows)
		return nil
	case "agents":
		asJSON, err := jsonOption("agents", args[1:])
		if err != nil {
			return err
		}
		agents := journal.Agents()
		if asJSON {
			return output(agents)
		}
		writeAgents(os.Stdout, agents)
		return nil
	case "update":
		f := flags("update")
		source := f.String("source", "", "release manifest URL or local file")
		checkOnly := f.Bool("check", false, "only inspect")
		remember := f.Bool("remember", false, "remember this update source")
		force := f.Bool("force", false, "explicitly reinstall this version")
		asJSON := f.Bool("json", false, "JSON output")
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
		var check update.Check
		err = withProgress(!*asJSON, "正在检查更新", func() error { var e error; _, check, e = update.Inspect(ctx, *source, version); return e })
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
			if *asJSON {
				return output(check)
			}
			if check.Upgrade {
				fmt.Printf("可更新至 %s，运行 daylog update 安装。\n", check.Available)
			} else {
				fmt.Printf("已是当前可用版本 %s。\n", version)
			}
			return nil
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
		err = withProgress(!*asJSON, "正在下载、校验并安装更新", func() error {
			var installErr error
			check, installErr = update.Install(ctx, *source, app, version, *force, func() error {
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
			return installErr
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
		if *asJSON {
			return output(map[string]any{"state": "updated", "version": check.Available, "restarted": restarted, "manual_restart_needed": wasRunning && !wasService})
		}
		fmt.Printf("已更新至 %s。\n", check.Available)
		if wasRunning {
			fmt.Println("更新后如需确认录屏授权，运行 daylog start。")
		}
		return nil
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
	case "status":
		return statusCommand(s, args[1:])
	case "resume", "pause":
		f := flags(args[0])
		asJSON := f.Bool("json", false, "JSON output")
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
		message := "已请求恢复记录。用 daylog status 查看当前状态。"
		if args[0] == "pause" {
			message = "已请求暂停记录。运行 daylog start 可继续。"
			if *minutes > 0 {
				message = fmt.Sprintf("已请求暂停记录 %d 分钟，之后自动恢复。", *minutes)
			}
		}
		return result(*asJSON, status, message)
	case "pending":
		f := flags("pending")
		asJSON := f.Bool("json", false, "JSON output")
		limit := f.Int("limit", 1, "batch limit")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		if *limit < 1 || *limit > 256 {
			return errors.New("limit must be 1..256")
		}
		batches, err := s.Pending(0)
		if err != nil {
			return err
		}
		total := len(batches)
		if len(batches) > *limit {
			batches = batches[:*limit]
		}
		if !*asJSON {
			writePending(os.Stdout, batches, total)
			return nil
		}
		out := []map[string]any{}
		for _, b := range batches {
			b, err = s.PrepareAnalysis(b.ID)
			if err != nil {
				return err
			}
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
		asJSON := f.Bool("json", false, "JSON output")
		file := f.String("file", "-", "report JSON")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		input, closeFn, err := readInput(*file)
		if err != nil {
			return err
		}
		defer closeFn()
		data, err := io.ReadAll(io.LimitReader(input, (1<<20)+1))
		if err != nil || len(data) > 1<<20 {
			return errors.New("invalid analysis size")
		}
		var identity struct {
			BatchID string `json:"batch_id"`
		}
		if json.Unmarshal(data, &identity) != nil {
			return errors.New("invalid analysis JSON")
		}
		if err = s.CommitAnalysis(identity.BatchID, data, "agent", ""); err != nil {
			return err
		}
		return result(*asJSON, map[string]string{"batch_id": identity.BatchID, "state": "committed"}, "活动记录已保存。用 daylog today 或 daylog report 查看。")

	case "analyze":
		f := flags("analyze")
		asJSON := f.Bool("json", false, "JSON output")
		id := f.String("batch", "", "batch ID")
		agent := f.String("agent", "", "choose a local agent instead of configured API")
		allowBattery := f.Bool("allow-battery", false, "explicit battery override")
		retry := f.Bool("retry", false, "retry a failed batch now without resetting daily budgets")
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
			pending, err := s.Pending(0)
			if err != nil {
				return err
			}
			if len(pending) == 0 {
				return result(*asJSON, map[string]string{"state": "no_pending_batches"}, "没有待整理的批次。")
			}
			*id = pending[0].ID
			if *retry {
				for _, b := range pending {
					if b.LastError != "" {
						*id = b.ID
						break
					}
				}
			}
		}
		if *retry {
			if err = s.RequestRetry(*id); err != nil {
				return err
			}
		}
		err = withProgress(!*asJSON, "正在整理活动", func() error {
			return s.AnalyzeConfigured(ctx, *id, c, apiKey(c), *agent, nil)
		})

		if err != nil {
			if *asJSON {
				return err
			}
			return fmt.Errorf("%s\n截图仍保留。重试：daylog analyze --batch %s --retry", analysisReason(err.Error()), *id)
		}
		return result(*asJSON, map[string]string{"batch_id": *id, "state": "analyzed"}, "已完成一批活动整理。用 daylog today 或 daylog report 查看。")
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
		fmt.Printf("Daylog · %s\n\n", *date)
		for _, r := range reports {
			for _, card := range r.Cards {
				fmt.Printf("  %s–%s  %s\n    %s", card.Start.Local().Format("15:04"), card.End.Local().Format("15:04"), readable(card.Title), readable(card.Summary))
				if card.DurationMethod != "" {
					fmt.Printf("（观察估计约 %d 分钟）", card.EstimatedSeconds/60)
				}
				fmt.Println()
			}
		}
		if len(reports) == 0 {
			if status, err := journal.SendControl(s.Root, journal.Control{Op: "status"}); err == nil {
				if status.Paused {
					fmt.Println("记录已暂停；运行 daylog start 继续。")
				} else if status.Error != "" {
					fmt.Println(recordingReason(status.Error))
				} else {
					fmt.Println("正在记录，整理完成后会显示今天的日记。")
				}
			} else {
				fmt.Println("还没有日记。运行 daylog start 开始记录。")
			}
		}
		if count > 0 {
			fmt.Printf("\n还有 %d 段活动尚未整理，日记暂不完整。\n", count)
		}
		return nil
	case "auth":
		if len(args) < 2 {
			return errors.New("用法：daylog auth set --stdin 或 daylog auth delete")
		}
		f := flags("auth " + args[1])
		asJSON := f.Bool("json", false, "JSON output")
		fromStdin := f.Bool("stdin", false, "read key from stdin")
		if err = f.Parse(args[2:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("auth 不接受位置参数")
		}
		if args[1] == "delete" {
			if *fromStdin {
				return errors.New("auth delete 不接受 --stdin")
			}
			if err = native.DeleteSecret(); err != nil {
				return err
			}
			return result(*asJSON, map[string]string{"state": "deleted_from_keychain"}, "已删除 Daylog 的系统钥匙串凭据。")
		}
		if args[1] != "set" || !*fromStdin {
			return errors.New("请使用 daylog auth set --stdin 从标准输入传入密钥")
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 16385))
		if err != nil {
			return err
		}
		value := strings.TrimSpace(string(data))
		if value == "" || len(value) > 16384 {
			return errors.New("API Key 长度无效")
		}
		if err = native.SetSecret(value); err != nil {
			return err
		}
		return result(*asJSON, map[string]string{"state": "stored_in_keychain"}, "API Key 已保存到 macOS 系统钥匙串。")

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
			asJSON := f.Bool("json", false, "JSON output")
			login := f.Bool("login", false, "start at login")
			dry := f.Bool("dry-run", false, "print plist without installing")
			if err = f.Parse(args[2:]); err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			exe, err = filepath.EvalSymlinks(exe)
			if err != nil {
				return err
			}
			if *dry {
				if *asJSON {
					return output(map[string]string{"plist": string(service.Plist(exe, s.Root, *login)), "state": "preview"})
				}
				_, err = os.Stdout.Write(service.Plist(exe, s.Root, *login))
				return err
			}
			path, err := service.Install(home, s.Root, exe, *login)
			if err != nil {
				return err
			}
			return result(*asJSON, map[string]any{"plist": path, "state": "installed_not_started", "start_at_login": *login}, "后台服务已配置，尚未启动。\n登录启动："+enabled(*login)+"\n配置文件："+path)
		case "start":
			asJSON, err := jsonOption("service start", args[2:])
			if err != nil {
				return err
			}
			if err = service.Start(home, s.Root); err != nil {
				return err
			}
			return result(asJSON, map[string]string{"state": "start_requested"}, "已请求启动后台服务。用 daylog status 查看运行情况。")
		case "stop":
			asJSON, err := jsonOption("service stop", args[2:])
			if err != nil {
				return err
			}
			if err = service.Stop(s.Root); err != nil {
				return err
			}
			return result(asJSON, map[string]string{"state": "stopped"}, "后台服务已停止。")
		case "uninstall":
			asJSON, err := jsonOption("service uninstall", args[2:])
			if err != nil {
				return err
			}
			if err = service.Stop(s.Root); err != nil {
				return err
			}
			err = os.Remove(service.Path(home, s.Root))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return result(asJSON, map[string]string{"state": "uninstalled"}, "后台服务注册已移除，历史记录保留。")
		}
		return errors.New("未知服务操作；可用 install、start、stop、uninstall")
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
	if c.AI.APIKey != "" {
		return c.AI.APIKey
	}
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
