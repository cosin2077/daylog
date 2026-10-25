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

	"daylog/internal/credentials"
	"daylog/internal/exports"
	"daylog/internal/journal"
	"daylog/internal/notion"
	"daylog/internal/platform"
	"golang.org/x/term"
)

const notionHelp = `daylog notion setup [--page Notion页面链接] [--database 已有Daylog库链接] [--token-stdin] [--timezone Asia/Shanghai] [--since YYYY-MM-DD]
daylog notion status [--json]
daylog notion check [--json]
daylog notion schedule [--times 12:00,16:00,18:00,20:00,22:00] [--json]
daylog notion sync [--date YYYY-MM-DD | --yes] [--dry-run] [--retry-uncertain] [--json]
daylog notion disable [--json]

setup 隐藏输入 Token，在目标页下建立一个 Daylog 数据库，每天一页；只有名称、日期、状态三个字段。--database 可指定已有的兼容库。
Token 加密保存在 ~/.daylog/integrations/credentials.enc，密钥为同目录 credentials.key；两者权限均限制为当前用户，整个 ~/.daylog 可随备份/迁移移动。旧系统凭据在首次读取时成功迁入加密文件后才删除。脚本请用 --token-stdin 并指定 --page。
手动 sync 默认今天，显示日期后需要确认；--date 显式指定日期可直接执行，--yes 明确接受默认今天。脚本须使用 --date 或 --yes。
后台每天本地时间 00:00 同步前一天，开机/联网后补传；需保持 Daylog 后台运行，可用 daylog start --login。
schedule 设置当天覆盖同步的时间点，按已保存时区执行；--times "" 恢复仅午夜同步。错过多个时间点，恢复后只补一次最新记录。
时区在 setup 时保存，跨午夜、休眠或补传都使用记录所属日期作为标题。
已整理活动、估时与来源、未知区间、待整理数量都会同步；不上传截图、不额外调用模型。
--dry-run 只输出本地结构化内容，不联网。--retry-uncertain 仅在确认云端没有该条记录时使用。
活动按时间写在正文。同一天的手动、自动和补齐会更新同一页的 Daylog 活动记录，保留「我的备注」；本地历史缺失时保留较完整的云端记录。
普通联网失败会自动退避，权限/额度问题需修正后手动 sync 或重新 setup。
`

func notionCommand(ctx context.Context, store *journal.Store, args []string) error {
	if len(args) == 0 {
		printHelp(notionHelp)
		return nil
	}
	op := args[0]
	if op != "setup" && op != "status" && op != "check" && op != "sync" && op != "disable" && op != "schedule" {
		return errors.New("Notion 子命令须为 setup、status、check、sync、schedule 或 disable")
	}
	f := flags("notion " + op)
	asJSON := f.Bool("json", false, "JSON output")
	page, database, zone, since, date := "", "", "", "", ""
	stdin, dryRun, retryUncertain, yes := false, false, false, false
	times := ""
	if op == "schedule" {
		f.StringVar(&times, "times", "", "comma-separated local checkpoint times; empty disables daytime sync")
	}
	if op == "setup" {
		f.StringVar(&page, "page", "", "Notion parent page URL")
		f.StringVar(&database, "database", "", "existing daily-page database URL")
		f.StringVar(&zone, "timezone", notion.SystemTimezone(), "IANA timezone")
		f.StringVar(&since, "since", "", "first date, defaults to yesterday")
		f.BoolVar(&stdin, "token-stdin", false, "read token from stdin without echo")
		f.BoolVar(&retryUncertain, "retry-uncertain", false, "explicitly retry an uncertain create after checking Notion")
	}
	if op == "sync" {
		f.StringVar(&date, "date", "", "source date; defaults to today with confirmation")
		f.BoolVar(&yes, "yes", false, "accept the default date without a prompt")
		f.BoolVar(&dryRun, "dry-run", false, "preview without network or credentials")
		f.BoolVar(&retryUncertain, "retry-uncertain", false, "explicitly retry an uncertain create after checking Notion")
	}
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	timesExplicit := false
	f.Visit(func(value *flag.Flag) { timesExplicit = timesExplicit || value.Name == "times" })
	if op == "setup" || op == "disable" || (op == "schedule" && timesExplicit) {
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
	sinceExplicit, dateExplicit := false, false
	f.Visit(func(value *flag.Flag) {
		if value.Name == "since" {
			sinceExplicit = true
		}
		if value.Name == "date" {
			dateExplicit = true
		}
	})
	settings, readErr := notion.ReadSettings(store.Root)
	if op == "schedule" {
		if readErr != nil {
			return errors.New("Notion 尚未配置或配置无效，请运行 daylog notion setup")
		}
		if settings.Layout != notion.DailyLayout {
			return errors.New("Notion 仍为旧版双库配置，请先运行 daylog notion setup")
		}
		if timesExplicit {
			var err error
			settings.SyncTimes, err = notion.ParseSyncTimes(times)
			if err != nil {
				return err
			}
			if err = notion.SaveSettings(store.Root, settings); err != nil {
				return err
			}
		}
		message := "每天 00:00 同步前一天"
		if len(settings.SyncTimes) > 0 {
			message += "；当天覆盖同步：" + strings.Join(settings.SyncTimes, "、")
		}
		return result(*asJSON, map[string]any{"timezone": settings.Timezone, "sync_times": append([]string{}, settings.SyncTimes...), "enabled": settings.Enabled}, "Notion 同步计划（"+settings.Timezone+"）："+message+"。后台恢复时补传最新记录；自动同步启用："+enabled(settings.Enabled))
	}
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
		message := fmt.Sprintf("Notion 自动同步：%t · 时区 %s · 每天 00:00 同步前一天\n目标页面：https://www.notion.so/%s\n已记录 %d 个日期的同步状态；用 --json 查看每个日期的成功、失败与下次重试时间。", settings.Enabled && settings.Layout == notion.DailyLayout, settings.Timezone, settings.ParentPage, len(state.Days))
		if len(settings.SyncTimes) > 0 {
			message += "\n当天覆盖同步：" + strings.Join(settings.SyncTimes, "、")
		}
		if settings.Layout != notion.DailyLayout {
			message += "\n当前为旧版双库配置，需重新运行 daylog notion setup；已有单页库可用 --database 指定。"
		}
		stored, credentialErr := (credentials.Store{Root: store.Root}).Exists(settings.SecretName())
		credentialError := ""
		if credentialErr != nil {
			credentialError = "无法检查本地加密凭据"
		}
		if credentialErr == nil {
			if stored {
				message += "\n凭据：~/.daylog/integrations/credentials.enc（本地加密）"
			} else {
				message += "\n凭据：尚未迁移到本地加密文件；首次读取时尝试从系统凭据存储迁移"
			}
		}
		credentialFile := filepath.Join(store.Root, "integrations", "credentials.enc")
		return result(*asJSON, map[string]any{"configured": true, "needs_setup": settings.Layout != notion.DailyLayout, "settings": settings, "sync": state, "credential_storage": "encrypted_file", "credential_file": credentialFile, "credential_stored": stored, "credential_error": credentialError}, message)
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
			proposed.SyncTimes = settings.SyncTimes
			if settings.Layout == notion.DailyLayout {
				proposed.DailyDatabase = settings.DailyDatabase
				proposed.DailySource = settings.DailySource
			}
			if !sinceExplicit {
				proposed.Since = settings.Since
			}
		}
		if database != "" {
			proposed.DailyDatabase, err = notion.ParsePage(database)
			if err != nil {
				return err
			}
			proposed.DailySource = ""
		}
		client := notion.NewClient(token)
		preflightCtx, preflightCancel := context.WithTimeout(ctx, 30*time.Second)
		preflightErr := client.CheckParent(preflightCtx, proposed.ParentPage)
		preflightCancel()
		if preflightErr != nil {
			return preflightErr
		}
		if err = newIntegrationSecrets(store.Root).Set(proposed.SecretName(), token); err != nil {
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
		return result(*asJSON, map[string]any{"configured": true, "enabled": true, "settings": service.Settings}, "Notion 单页日志配置完成。每天 00:00 同步前一天，失败后自动补同步。请保持 Daylog 后台运行；登录自动启动：daylog start --login。手动 daylog notion sync 默认今天并确认日期，同一天后续更新原页面。")
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
		if dateExplicit {
			return errors.New("日期不能为空，请用 --date YYYY-MM-DD 指定")
		}
		date = time.Now().In(location).Format("2006-01-02")
	}
	var day time.Time
	if op == "sync" {
		day, err = exports.ValidateDay(date, location)
		if err != nil {
			return err
		}
		now := time.Now().In(location)
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
		if day.After(today) {
			return errors.New("不能同步未来日期，请选择今天或更早日期")
		}
	}
	if op == "sync" && dryRun {
		snapshot, err := exports.LoadDay(store, day)
		if err != nil {
			return err
		}
		return output(snapshot)
	}
	if settings.Layout != notion.DailyLayout {
		return errors.New("Notion 仍为旧版双库配置，请重新运行 daylog notion setup；已有单页库可用 --database 指定")
	}
	if op == "sync" && !dateExplicit && !yes {
		confirmed, err := confirmNotionDate(os.Stdin, os.Stderr, date, location.String(), term.IsTerminal(int(os.Stdin.Fd())))
		if err != nil {
			return err
		}
		if !confirmed {
			return result(*asJSON, map[string]any{"state": "cancelled", "date": date}, "已取消，未同步。")
		}
	}
	label := "正在同步到 Notion"
	if op == "check" {
		label = "正在检查 Notion 连接"
	}
	var job exports.Job
	err = withProgress(!*asJSON, label, func() error {
		token, tokenErr := newIntegrationSecrets(store.Root).Get(settings.SecretName())
		if errors.Is(tokenErr, credentials.ErrNotFound) {
			return errors.New("无法读取 Notion 凭证，请重新运行 daylog notion setup")
		}
		if tokenErr != nil {
			return tokenErr
		}
		if token == "" {
			return errors.New("无法读取 Notion 凭证，请检查系统凭据存储或重新运行 daylog notion setup")
		}
		service := notion.Service{Root: store.Root, Settings: settings, Client: notion.NewClient(token), AllowUncertain: retryUncertain}
		if op == "check" {
			return service.Check(ctx)
		}
		syncCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		var syncErr error
		job, syncErr = service.Engine().Sync(syncCtx, day, true)
		return syncErr
	})
	if err != nil {
		return err
	}
	if op == "check" {
		return result(*asJSON, map[string]any{"connected": true, "schema_valid": true}, "Notion 连接、数据库字段与读取权限检查通过；写权限会在同步时校验。")
	}
	return result(*asJSON, job, "Notion 同步完成："+date+"\n"+job.URL)
}

func confirmNotionDate(input io.Reader, output io.Writer, date, zone string, interactive bool) (bool, error) {
	if !interactive {
		return false, errors.New("手动同步默认今天，需要终端确认日期；脚本请显式使用 --date YYYY-MM-DD，或用 --yes 接受默认今天")
	}
	fmt.Fprintf(output, "将同步 %s（%s）的当前记录到 Notion；今天尚未结束，后续会更新同一页面。继续？[y/N] ", date, zone)
	line, err := bufio.NewReader(io.LimitReader(input, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, errors.New("无法读取同步确认，未执行同步")
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "是":
		return true, nil
	}
	return false, nil
}
