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

	"daylog/internal/journal"
	"daylog/internal/native"
	"daylog/internal/notion"
	"daylog/internal/platform"
	"daylog/internal/service"
	"daylog/internal/update"
)

var version = "0.10.0"

const help = `Daylog — 自动记录今天做了什么

  daylog start    开始记录（首次需授权录屏）
  daylog today    查看今天的日记
  daylog report   打开日／周／月可视化报告
  daylog status   查看运行状态和待整理记录
  daylog stop     停止记录
  daylog config   查看或配置模型（可选，使用 config -h 查看方法）
  daylog notion   配置每日 Notion 同步（可选）
  daylog update   更新版本
  daylog -h       显示帮助（直接运行 daylog 也可以）

配置、截图和日记都在 ~/.daylog。
自动使用本机 Agent 的模型配置，整理时可能调用云端服务。
默认输出便于阅读；Agent / 脚本查询请加 --json。
更多设置与排障：daylog help --advanced
`

const advancedHelp = `Daylog — 高级操作

daylog [--data 私有目录] <命令>

日常
  start [--login]             开始记录；--login 启用登录启动
  stop                       停止记录
  pause [--minutes N]         暂停 1–1440 分钟，到时自动恢复
  resume                     恢复记录
  today [yesterday]           查看今天或昨天；--full 查看全部
  report [day|week|month]     打开日／周／月报告；--no-open 只生成
  status [--verbose]          查看状态；--verbose 显示详情
  pending [--limit N]         查看待整理记录，默认最多 10 段
  analyze [--batch ID]        整理一段记录；--retry 立即重试
  config [选项]              查看或修改配置
  update [--check]            安装更新；--check 只检查
  notion setup|status|sync    配置、查看或手动执行 Notion 同步

排障
  doctor                     检查系统与最近整理结果，不截图或联网
  agents                     查看本机 Agent 安装及适配状态
  permission                 请求系统录屏权限
  windows                    列出可选窗口（标题可能敏感）

Agent 集成
  today | report | pending | status --json
                             结构化查询；pending 含记录编号和图片路径
  commit --file 文件或-       导入 Agent 整理好的记录
  prompt                     查看整理提示词
  auth set --stdin | delete   存进系统凭据存储（不会回显），或移除密钥

服务与验证
  init                       创建配置，不启动记录
  run [--fixture JPEG]       前台运行；--fixture 只使用合成测试图片
  service install [--login] [--dry-run]
                             配置用户后台；--dry-run 只预览配置
  service start | stop | uninstall
                             启动、停止或移除后台服务
  version                    查看版本

每个命令可用 -h 或 daylog help <命令> 查看完整选项。
结果命令默认显示中文，加 --json 输出结构化结果。
service install --dry-run 默认导出平台后台配置；run 为前台进程，不输出结果文档。

默认不配置额外 API；启动后尝试已有 Agent（可能使用云端服务）。安装不启动录制。
`

func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "daylog:", err)
		os.Exit(1)
	}
}
func output(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }
func printHelp(text string) {
	fmt.Print(strings.Replace(text, "Daylog —", "Daylog "+version+" —", 1))
}

type commandFlags struct{ *flag.FlagSet }

func (f *commandFlags) Parse(args []string) error {
	prepared, err := f.prepare(args)
	if err != nil {
		return err
	}
	if err := f.FlagSet.Parse(prepared); err != nil {
		return explainError(err)
	}
	if f.NArg() != 0 {
		return fmt.Errorf("不支持的位置参数；用 daylog help --advanced 查看 %s 的用法", f.Name())
	}
	return nil
}

func flags(name string) *commandFlags {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return &commandFlags{f}
}

func execute(args []string) error {
	err := executeCommand(args)
	if errors.Is(err, flag.ErrHelp) {
		printHelp(subcommandHelp(args[0]))
		return nil
	}
	return explainError(err)
}

func executeCommand(args []string) error {
	if exe, err := os.Executable(); err == nil {
		update.CleanupStaging(exe)
	}
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
	if len(args) > 1 && args[0] == "help" && args[1] != "--advanced" {
		if _, ok := commandUsage[args[1]]; !ok {
			return unknownCommand(args[1])
		}
		printHelp(subcommandHelp(strings.Join(args[1:], " ")))
		return nil
	}
	if len(args) > 1 && args[0] != "help" {
		for _, a := range args[1:] {
			if a == "-h" || a == "--help" {
				if _, ok := commandUsage[args[0]]; ok {
					printHelp(subcommandHelp(args[0]))
					return nil
				}
			}
		}
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if len(args) > 1 && args[1] == "--advanced" {
			printHelp(advancedHelp)
		} else {
			printHelp(help)
		}
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
	known := false
	for _, command := range commands {
		known = known || args[0] == command
	}
	if !known {
		return unknownCommand(args[0])
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
	case "notion":
		return notionCommand(ctx, s, args[1:])
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
		var draft journal.Batch
		if readErr := journal.ReadJSON(filepath.Join(s.Root, "draft.json"), &draft); readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return errors.New("记录已停止，但无法检查草稿状态；请运行 daylog doctor")
		}
		if len(draft.Frames) > 0 {
			return errors.New("记录已停止，但仍有未封批草稿，数据已保留；请运行 daylog doctor 检查后重新启动")
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
		env, err := (native.Desktop{}).Environment(ctx)
		if err != nil {
			return err
		}
		c, configErr := journal.ReadConfig(s.Root)
		if asJSON {
			return output(map[string]any{"version": version, "environment": env, "config_valid": configErr == nil, "policy": journal.PowerPolicy(c, env), "data_dir": s.Root, "model_status": doctorModels(s.Root, c), "note": "模型显示安装及最近结果，本次未截图或联网测试"})
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
			*source = journal.DefaultUpdateSource
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
				fmt.Printf("已是最新版本（%s）。\n", version)
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
		if platform.IsWindows {
			app = exe
		}
		wasRunning := false
		restarted := false
		wasService := false
		err = withProgress(!*asJSON, "正在下载、校验并安装更新", func() error {
			var installErr error
			check, installErr = update.Install(ctx, *source, app, version, *force, func() error {
				wasService = service.IsLoaded(s.Root)
				previous, probe := journal.SendControl(s.Root, journal.Control{Op: "status"})
				if probe != nil {
					stopped, err := recorderStopped(s.Root)
					if err != nil {
						return err
					}
					if !stopped || wasService {
						return errors.New("记录进程仍在运行但控制接口未就绪，请稍后重试更新")
					}
					return nil
				}
				if err := s.SavePause(previous.Paused, previous.PauseUntil); err != nil {
					return err
				}
				wasRunning = true
				// Unload the LaunchAgent and wait for the daemon lock. A socket
				// disappearing does not prove launchd has reaped the exiting process;
				// kickstart during that gap can succeed without launching a replacement.
				return stopRuntime(ctx, s.Root)
			}, func() error {
				if wasRunning && wasService {
					home, _ := os.UserHomeDir()
					if err = service.Start(home, s.Root); err != nil {
						return errors.New("应用已更新，但后台重启失败；原暂停状态已保留")
					}
					if _, err = waitRuntimeReady(ctx, func() (journal.Status, error) { return journal.SendControl(s.Root, journal.Control{Op: "status"}) }, true); err != nil {
						return errors.New("应用已更新，但后台未就绪；原暂停状态已保留，请运行 daylog doctor 检查")
					}
					restarted = true
				}
				return nil
			})
			return installErr
		})
		if err != nil {
			return err
		}

		if *asJSON {
			return output(map[string]any{"state": "updated", "version": check.Available, "restarted": restarted, "manual_restart_needed": wasRunning && !wasService})
		}
		fmt.Printf("已更新至 %s。\n", check.Available)
		if wasRunning {
			fmt.Println("更新已保留原暂停状态；运行 daylog status 查看，恢复记录请用 daylog resume。")
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
		var source native.Source = native.Desktop{}
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
		platform.BackgroundPriority()
		r := journal.Runner{Store: s, Config: c, Source: source, Key: func() string { return apiKey(c) }}
		if *fixture == "" {
			r.Background = func(ctx context.Context) { notion.Run(ctx, s.Root, nativeNotionSecrets{}) }
		}
		return r.Run(ctx)
	case "status":
		return statusCommand(s, args[1:])
	case "resume", "pause":
		f := flags(args[0])
		asJSON := f.Bool("json", false, "JSON output")
		pauseMinutes := 30
		if args[0] == "pause" {
			f.IntVar(&pauseMinutes, "minutes", 30, "pause duration, 1..1440 minutes")
		}
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		if pauseMinutes < 1 || pauseMinutes > 1440 {
			return errors.New("暂停时长须为 1–1440 分钟；恢复记录请用 daylog resume")
		}
		status, err := journal.SendControl(s.Root, journal.Control{Op: args[0], Minutes: pauseMinutes})
		if err != nil {
			return err
		}
		message := "已恢复记录。"
		if args[0] == "pause" {
			if !status.Paused || status.PauseUntil == nil {
				return errors.New("后台尚未确认暂停，请用 daylog status 检查")
			}
			message = fmt.Sprintf("已暂停 %d 分钟，%s 自动恢复。提前恢复：daylog resume", pauseMinutes, status.PauseUntil.Local().Format("15:04"))
		} else if status.Paused {
			return errors.New("后台尚未确认恢复，请用 daylog status 检查")
		}
		return result(*asJSON, status, message)
	case "pending":
		f := flags("pending")
		asJSON := f.Bool("json", false, "JSON output")
		limit := f.Int("limit", 10, "record limit; JSON defaults to the full queue")
		prepare := f.Bool("prepare", false, "freeze context for manual analysis; requires --json")
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		explicitLimit := false
		f.Visit(func(value *flag.Flag) {
			if value.Name == "limit" {
				explicitLimit = true
			}
		})
		if *prepare && !*asJSON {
			return errors.New("--prepare 需要与 --json 一起使用；普通查询不冻结分析上下文")
		}
		if *limit < 1 || *limit > 256 {
			return errors.New("limit must be 1..256")
		}
		batches, err := s.Pending(0)
		if err != nil {
			return err
		}
		total := len(batches)
		if (!*asJSON || explicitLimit) && len(batches) > *limit {
			batches = batches[:*limit]
		}
		if !*asJSON {
			writePending(os.Stdout, batches, total)
			return nil
		}
		out := []map[string]any{}
		for _, b := range batches {
			if *prepare {
				b, err = s.PrepareAnalysis(b.ID)
			} else {
				b, err = s.PreviewAnalysis(b.ID)
			}
			if err != nil {
				return err
			}
			paths := []string{}
			available := true
			for _, frame := range b.Frames {
				path, err := s.FramePath(frame)
				if err != nil {
					available = false
					continue // Keep the broken batch visible without offering misaligned paths.
				}
				paths = append(paths, path)
			}
			if !available {
				paths = []string{}
			}
			out = append(out, map[string]any{"batch": b, "image_paths": paths, "evidence_available": available, "context_preview": b.SchemaVersion == 2 && !b.ContextReady, "queue_total": total, "queue_has_more": total > len(batches)})
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
		if *id != "" {
			if _, e := s.Batch(*id); e != nil {
				if errors.Is(e, os.ErrNotExist) || e.Error() == "invalid batch ID" {
					return fmt.Errorf("找不到这段记录（%s）。用 daylog pending 查看可整理的记录", readable(*id))
				}
				return fmt.Errorf("无法读取这段记录，请用 daylog doctor 检查数据目录：%w", e)
			}
		}
		c, err := journal.ReadConfig(s.Root)
		if err != nil {
			return err
		}
		env, err := (native.Desktop{}).Environment(ctx)
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
				return result(*asJSON, map[string]string{"state": "no_pending_batches"}, "没有待整理的记录。")
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
		return todayCommand(s, args[1:])
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
			dry := f.Bool("dry-run", false, "print platform configuration without installing")
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
			configKey := "plist"
			if platform.IsWindows {
				configKey = "config"
			}
			if *dry {
				if *asJSON {
					return output(map[string]string{configKey: string(service.Preview(exe, s.Root, *login)), "state": "preview"})
				}
				_, err = os.Stdout.Write(service.Preview(exe, s.Root, *login))
				return err
			}
			path, err := service.Install(home, s.Root, exe, *login)
			if err != nil {
				return err
			}
			return result(*asJSON, map[string]any{configKey: path, "state": "installed_not_started", "start_at_login": *login}, "后台服务已配置，尚未启动。\n登录启动："+enabled(*login)+"\n配置文件："+path)
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
			err = service.Uninstall(home, s.Root)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return result(asJSON, map[string]string{"state": "uninstalled"}, "后台服务注册已移除，历史记录保留。")
		}
		return errors.New("未知服务操作；可用 install、start、stop、uninstall")
	}
	return unknownCommand(args[0])
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
