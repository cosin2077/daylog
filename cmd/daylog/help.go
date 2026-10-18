package main

import "strings"

var commandUsage = map[string]string{
	"today":      "daylog today [today|yesterday] [--date YYYY-MM-DD] [--full | --limit N] [--explain] [--json]\n\n查看当天活动；默认最多 20 项，--full 查看全部。--explain 查看估时说明。",
	"status":     "daylog status [--verbose] [--json]\n\n查看记录状态和下一步。--verbose 显示截图、进程和整理详情。",
	"report":     reportHelp,
	"config":     configHelp,
	"pending":    "daylog pending [--limit N] [--json] [--prepare]\n\n文本默认最多显示 10 段；JSON 默认返回完整数组。显式 --limit 的 JSON 各项含 queue_total/queue_has_more。空数组表示队列为空。普通查询只预览上下文；手工读图前用 --prepare --json 冻结所选批次的关联候选。",
	"analyze":    "daylog analyze [ID | --batch ID] [--retry] [--agent NAME] [--allow-battery] [--json]\n\n整理一段记录。先用 daylog pending 查看待整理内容；--retry 立即重试，不重置额度。",
	"pause":      "daylog pause [--minutes 30] [--json]\n\n暂停 1–1440 分钟，到时自动恢复。提前恢复：daylog resume。",
	"resume":     "daylog resume [--json]\n\n恢复记录，并确认后台已经恢复。",
	"start":      "daylog start [--login] [--json]\n\n开始记录，首次需授权录屏。--login 同时启用登录启动。",
	"stop":       "daylog stop [--json]\n\n停止记录，保留日记和截图，并关闭登录启动。",
	"doctor":     "daylog doctor [--json]\n\n检查屏幕、电源和配置，显示模型安装及最近整理结果；不截图、不联网调用模型。",
	"agents":     "daylog agents [--json]\n\n查看本机 Agent 安装及图片适配情况；安装状态不代表模型已连通。",
	"permission": "daylog permission [--json]\n\n检查或请求系统录屏权限。",
	"windows":    "daylog windows [--json]\n\n查看可选窗口；窗口标题可能包含个人内容。",
	"update":     "daylog update [--check] [--source URL] [--remember] [--json]\n\n检查或安装最新正式版本。--check 只检查；默认保留配置和记录。",
	"init":       "daylog init [--json]\n\n准备配置，不开始记录。日常使用直接运行 daylog start 即可。",
	"run":        "daylog run [--fixture JPEG]\n\n在当前终端持续记录。--fixture 仅供合成图片测试使用。",
	"commit":     "daylog commit --file FILE|- [--json]\n\n导入 Agent 整理好的记录，校验截图引用后保存。",
	"prompt":     "daylog prompt [--json]\n\n查看整理提示词，不调用模型。",
	"auth":       "daylog auth set --stdin | delete [--json]\n\n将密钥存进系统凭据存储（不会回显），或移除已存密钥。",
	"service":    "daylog service install [--login] [--dry-run] | start | stop | uninstall [--json]\n\n管理当前用户的后台记录服务。普通使用请运行 daylog start / stop。",
	"version":    "daylog version [--json]\n\n查看当前版本。",
}

func subcommandHelp(name string) string {
	if name == "config" || name == "report" {
		return commandUsage[name]
	}
	base := strings.Fields(name)
	key := name
	if len(base) > 0 {
		key = base[0]
	}
	usage := commandUsage[key]
	if usage == "" {
		return help
	}
	return "Daylog — " + key + "\n\n  " + usage + "\n"
}
