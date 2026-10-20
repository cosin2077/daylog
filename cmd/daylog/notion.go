package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"daylog/internal/exports"
	"daylog/internal/journal"
	"daylog/internal/native"
	"daylog/internal/notion"
	"daylog/internal/platform"
	"golang.org/x/term"
)

const notionHelp = `daylog notion setup [--page Notion页面链接] [--token-stdin] [--timezone Asia/Shanghai] [--since YYYY-MM-DD]
daylog notion status [--json]
daylog notion check [--json]
daylog notion sync [--date YYYY-MM-DD] [--dry-run] [--retry-uncertain] [--json]
daylog notion disable [--json]

setup 交互式隐藏输入 Token，并在目标页面下建立「Daylog 日志」「Daylog 活动」数据库。
Token 只存系统凭据存储，不写入配置、输出或命令历史；脚本请用 --token-stdin 并指定 --page。
默认同步昨天，此后后台每天本地时间 00:00 同步前一天，开机/联网后补传；需保持 Daylog 后台运行，可用 daylog start --login。
时区在 setup 时保存，跨午夜、休眠或补传都使用记录所属日期作为标题。
已整理活动、估时与来源、未知区间、待整理数量都会同步；不上传截图、不额外调用模型。
--dry-run 只输出本地结构化内容，不联网。--retry-uncertain 仅在确认云端没有该条记录时使用。
网页正文留给人工复盘，后续同步只更新 Daylog 管理的属性。普通联网失败会自动退避，权限/额度问题需修正后手动 sync 或重新 setup。
`

type nativeNotionSecrets struct{}

func (nativeNotionSecrets) Set(name, value string) error { return native.SetNamedSecret(name, value) }
func (nativeNotionSecrets) Get(name string) string       { return native.NamedSecret(name) }

func notionCommand(ctx context.Context, store *journal.Store, args []string) error {
	if len(args) == 0 {
		printHelp(notionHelp)
		return nil
	}
	op := args[0]
	if op != "setup" && op != "status" && op != "check" && op != "sync" && op != "disable" {
		return errors.New("Notion 子命令须为 setup、status、check、sync 或 disable")
	}
	f := flags("notion " + op)
	asJSON := f.Bool("json", false, "JSON output")
	page, zone, since, date := "", "", "", ""
	stdin, dryRun, retryUncertain := false, false, false
	if op == "setup" {
		f.StringVar(&page, "page", "", "Notion parent page URL")
		f.StringVar(&zone, "timezone", notion.SystemTimezone(), "IANA timezone")
		f.StringVar(&since, "since", "", "first date, defaults to yesterday")
		f.BoolVar(&stdin, "token-stdin", false, "read token from stdin without echo")
		f.BoolVar(&retryUncertain, "retry-uncertain", false, "explicitly retry an uncertain create after checking Notion")
	}
	if op == "sync" {
		f.StringVar(&date, "date", "", "source date; defaults to yesterday")
		f.BoolVar(&dryRun, "dry-run", false, "preview without network or credentials")
		f.BoolVar(&retryUncertain, "retry-uncertain", false, "explicitly retry an uncertain create after checking Notion")
	}
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if op == "setup" {
		dir := filepath.Join(store.Root, "integrations")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		lock, err := os.OpenFile(filepath.Join(dir, "notion-setup.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer lock.Close()
		if err = platform.TryLock(lock); err != nil {
			return errors.New("另一个 Notion 配置正在进行，请稍后再试")
		}
		defer platform.Unlock(lock)
	}
	sinceExplicit := false
	f.Visit(func(value *flag.Flag) {
		if value.Name == "since" {
			sinceExplicit = true
		}
	})
	settings, readErr := notion.ReadSettings(store.Root)
	if op == "status" {
		if errors.Is(readErr, os.ErrNotExist) {
			return result(*asJSON, map[string]any{"configured": false, "enabled": false}, "Notion 尚未配置。运行 daylog notion setup 开始配置。")
		}
		if readErr != nil {
			return readErr
		}
		state, err := exports.ReadState(settings.StateRoot(store.Root))
		if err != nil {
			return err
		}
		return result(*asJSON, map[string]any{"configured": true, "settings": settings, "sync": state}, fmt.Sprintf("Notion 自动同步：%t · 时区 %s · 每天 00:00 同步前一天\n目标页面：https://www.notion.so/%s\n已记录 %d 个日期的同步状态；用 --json 查看每个日期的成功、失败与下次重试时间。", settings.Enabled, settings.Timezone, settings.ParentPage, len(state.Days)))
	}
	if op == "setup" {
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if zone == "" {
			if stdin || !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("无法确定本机 IANA 时区，请用 --timezone 指定，例如 Asia/Shanghai")
			}
			fmt.Fprint(os.Stderr, "固定时区（例如 Asia/Shanghai）：")
			reader := bufio.NewReader(os.Stdin)
			value, err := reader.ReadString('\n')
			if err != nil {
				return errors.New("无法读取时区")
			}
			zone = strings.TrimSpace(value)
		}
		token := ""
		if stdin {
			if page == "" {
				return errors.New("--token-stdin 需要同时指定 --page")
			}
			raw, err := io.ReadAll(io.LimitReader(os.Stdin, 2562))
			if err != nil {
				return errors.New("无法读取 Notion Token")
			}
			if len(raw) > 2561 {
				return errors.New("Notion Token 长度无效")
			}
			token = strings.TrimSpace(string(raw))
		} else {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("请在终端运行 setup 隐藏输入 Token，或使用 --page 与 --token-stdin")
			}
			if page == "" {
				fmt.Fprint(os.Stderr, "Notion 目标页面链接：")
				reader := bufio.NewReader(os.Stdin)
				line, err := reader.ReadString('\n')
				if err != nil {
					return errors.New("无法读取目标页面链接")
				}
				page = strings.TrimSpace(line)
			}
			fmt.Fprint(os.Stderr, "Notion API Token（输入不可见）：")
			raw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return errors.New("无法隐藏读取 Notion Token")
			}
			token = strings.TrimSpace(string(raw))
		}
		if token == "" || len(token) > 2560 || strings.ContainsAny(token, "\x00\r\n") {
			return errors.New("Notion Token 须为不超过 2560 字节的单行文本")
		}
		location, err := time.LoadLocation(zone)
		if err != nil {
			return errors.New("时区无效，请使用例如 Asia/Shanghai 的 IANA 名称")
		}
		now := time.Now().In(location)
		if since == "" {
			since = now.AddDate(0, 0, -1).Format("2006-01-02")
		}
		start, err := exports.ValidateDay(since, location)
		if err != nil {
			return err
		}
		if start.After(now) {
			return errors.New("开始日期不能晚于今天")
		}
		proposed, err := notion.NewSettings(page, zone, since)
		if err != nil {
			return err
		}
		if readErr == nil && settings.ParentPage == proposed.ParentPage {
			proposed.SourceID = settings.SourceID
			proposed.DailyDatabase = settings.DailyDatabase
			proposed.DailySource = settings.DailySource
			proposed.ActivityDatabase = settings.ActivityDatabase
			proposed.ActivitySource = settings.ActivitySource
			if !sinceExplicit {
				proposed.Since = settings.Since
			}
		}
		client := notion.NewClient(token)
		preflightCtx, preflightCancel := context.WithTimeout(ctx, 30*time.Second)
		preflightErr := client.CheckParent(preflightCtx, proposed.ParentPage)
		preflightCancel()
		if preflightErr != nil {
			return preflightErr
		}
		if err = (nativeNotionSecrets{}).Set(proposed.SecretName(), token); err != nil {
			return err
		}
		if err = notion.SaveSettings(store.Root, proposed); err != nil {
			return err
		}
		service := notion.Service{Root: store.Root, Settings: proposed, Client: client, AllowUncertain: retryUncertain}
		setupCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		if err = service.Setup(setupCtx); err != nil {
			return err
		}
		return result(*asJSON, map[string]any{"configured": true, "enabled": true, "settings": service.Settings}, "Notion 配置完成。每天 00:00 同步前一天，失败后自动补同步。请保持 Daylog 后台运行；登录自动启动：daylog start --login。首次可用 daylog notion sync 同步昨天。")
	}
	if op == "disable" {
		if readErr != nil {
			return readErr
		}
		settings.Enabled = false
		if err := notion.SaveSettings(store.Root, settings); err != nil {
			return err
		}
		return result(*asJSON, map[string]any{"enabled": false}, "已停用 Notion 自动同步，云端记录保留。")
	}
	if readErr != nil && !(op == "sync" && dryRun && errors.Is(readErr, os.ErrNotExist)) {
		return errors.New("Notion 尚未配置或配置无效，请运行 daylog notion setup")
	}
	if op == "sync" && dryRun && readErr != nil {
		settings.Timezone = notion.SystemTimezone()
	}
	location, err := time.Local, error(nil)
	if settings.Timezone != "" {
		location, err = time.LoadLocation(settings.Timezone)
	}
	if err != nil {
		return errors.New("Notion 时区无效")
	}
	if op == "sync" && date == "" {
		date = time.Now().In(location).AddDate(0, 0, -1).Format("2006-01-02")
	}
	if op == "sync" && dryRun {
		day, err := exports.ValidateDay(date, location)
		if err != nil {
			return err
		}
		snapshot, err := exports.LoadDay(store, day)
		if err != nil {
			return err
		}
		return output(snapshot)
	}
	token := (nativeNotionSecrets{}).Get(settings.SecretName())
	if token == "" {
		return errors.New("无法读取 Notion 凭证，请检查系统凭据存储或重新运行 daylog notion setup")
	}
	service := notion.Service{Root: store.Root, Settings: settings, Client: notion.NewClient(token), AllowUncertain: retryUncertain}
	if op == "check" {
		if err = service.Check(ctx); err != nil {
			return err
		}
		return result(*asJSON, map[string]any{"connected": true, "schema_valid": true}, "Notion 连接、数据库字段与读取权限检查通过；写权限会在同步时校验。")
	}
	day, err := exports.ValidateDay(date, location)
	if err != nil {
		return err
	}
	if !day.Before(time.Date(time.Now().In(location).Year(), time.Now().In(location).Month(), time.Now().In(location).Day(), 0, 0, 0, 0, location)) {
		return errors.New("只同步已结束的日期，请选择昨天或更早日期")
	}
	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	job, err := service.Engine().Sync(syncCtx, day, true)
	if err != nil {
		return err
	}
	return result(*asJSON, job, "Notion 同步完成："+date+"\n"+job.URL)
}
