# Daylog

<img src="assets/icon/daylog.png" width="96" height="96" alt="Daylog：日光与日志" />

自动记录今天做了什么：后台低频截图，调用本机已有 Agent（Claude → Codex → Hermes）的视觉模型整理成工作日记，并生成日／周／月复盘报告。支持 macOS 14+；Windows 10 1809+ / Windows 11 x64 从 0.7.0 起提供（范围与边界见 [Windows 说明](skills/daylog/references/windows.md)）。

## 日常使用

```sh
daylog start                 # 开始记录（自动创建配置、准备后台并检查权限）
daylog today                 # 查看今天；today yesterday 查看昨天
daylog report                # 在浏览器打开日／周／月报告
daylog pause --minutes 30    # 暂停 1–1440 分钟，到时自动恢复
daylog resume                # 提前恢复
daylog stop                  # 停止记录
daylog status                # 六行概览：状态、今天估时、最近截图、待整理、报告、下一步
```

首批日记通常约 15 分钟后生成；它反映屏幕活动，不等于精确工时。`start` 重复执行不会重复启动；退出终端不会停止记录；默认不登录自启，需要时用 `start --login`，`stop` 会一并关闭。所有结果命令默认输出中文，脚本或 Agent 解析时加 `--json`。`daylog -h` 或 `daylog help 命令` 查看帮助，不会初始化数据。

默认使用本机 Agent 已有的模型配置，无需 API Key；模型需要支持图片，分析可能使用该 Agent 的云端服务。Grok / Antigravity 目前仅检测，不用于分析。没有可用模型时截图保留在本地等待处理。

## 安装与更新

**前提**：macOS 首次 `daylog start` 时，需要按系统提示在“系统设置 › 隐私与安全性”中给 Daylog 录屏权限，命令会等待并自动继续。Windows 无需管理员权限，在当前用户桌面运行，锁屏或安全桌面时暂停。

把这一句话发给 Claude Code、Codex 或 Hermes：

```text
请按 https://github.com/cosin2077/daylog 的 README，为我安装 Daylog 和当前 Agent 的 skill，并验证安装成功。
```

<details>
<summary>Agent 安装步骤 / 手动安装</summary>

1. 确认平台：macOS 14+ 或 Windows 10/11 x64。获取本仓库的 `skills/daylog`，或下载 [最新 Release 的轻量 skill 包](https://github.com/cosin2077/daylog/releases/latest/download/daylog-skill.tar.gz)；获取不到时检查 [Releases](https://github.com/cosin2077/daylog/releases)。不要要求用户安装编译工具。
2. 将 skill 放到当前 Agent 的目录：Codex 为 `~/.agents/skills/daylog`，Claude Code 为 `~/.claude/skills/daylog`，Hermes 为 `~/.hermes/skills/daylog`。源码仓库可用 `python3 scripts/install-skills.py --agent codex|claude|hermes|all`，也可直接复制目录。目标已存在时比较并保留用户修改。
3. 从 skill 目录运行安装器：macOS 执行 `sh scripts/install-runtime.sh`；Windows 执行 `powershell.exe -NoProfile -File scripts/install-runtime.ps1`。安装器只下载匹配平台的二进制，校验包摘要和固定发布身份；已有安装会升级并保留配置和日记。
4. 验证 `daylog version` 和 `daylog -h`。PATH 尚未生效时，macOS 使用 `~/.local/bin/daylog`，Windows 使用 `%LOCALAPPDATA%\Daylog\bin\daylog.exe`。只有用户要求开始记录时才执行 `daylog start`；macOS 授权由本人完成。

macOS 应用安装到 `~/Applications/Daylog.app`，命令链接到 `~/.local/bin/daylog`。预编译运行时不需要 Go、Node、Python 或包管理器。

</details>

更新：`daylog update`（`--check` 只检查）。默认更新源是 GitHub 最新正式 Release；校验 SHA-256、固定发布证书和签名后才替换，配置和记录保留，没有后台自动更新。签名与更新安全细节见 [开发与发布](docs/development.md)。

## 卸载

```sh
daylog service uninstall     # 停止后台并移除后台注册，记录保留
daylog auth delete           # 可选：删除系统凭据中保存的 API 密钥
rm -rf ~/Applications/Daylog.app ~/.local/bin/daylog
rm -rf ~/.daylog             # 可选：删除全部记录与配置，不可恢复
```

Windows 运行 `daylog service uninstall` 后删除 `%LOCALAPPDATA%\Daylog`，需要时再删除 `%USERPROFILE%\.daylog`。Agent 的 skill 目录（如 `~/.claude/skills/daylog`）可直接删除。

## 报告

```sh
daylog report                          # 今天
daylog report week                     # 本周；report month 为本月
daylog report --date 2026-10-13        # 指定日期所在的日／周／月
daylog report --no-images              # 不内嵌截图，便于分享
daylog report --period week --json     # 供 Agent 读取的结构化汇总
daylog report --from 2026-10-13T14:00:00+08:00 --to 2026-10-13T16:00:00+08:00 --json
```

报告是一个离线 HTML 快照（`~/.daylog/reports/report.html`，每次生成替换上一份）：先给出按做事类型汇总的一句话和三个关键数字（电脑工作估时、最长连续一段、任务切换，与前一天／上周／上月比较），再列出“做了哪些事”、当天时间轴、一条“下次试一件事”建议和“时间漏在哪”（短时切换来源）。周报显示每天的时长与切换，月报是日历热力图。支持日期导航、搜索、类型／任务筛选、截图预览、打印／PDF 和分享图片。

报告只汇总已保存的分析结果，生成时不调用模型、不联网、不读取密钥。含截图的 HTML 是私有文件，分享前用 `--no-images`。顶栏“分享”在浏览器本地生成 PNG 卡片，默认只含汇总数字，不含事项标题、应用名和截图。

![Daylog 每日复盘报告（演示数据）](docs/assets/report-preview.jpg)

估时与统计口径（空闲判断、推断区间、短时切换规则等）见 [工作原理与统计口径](docs/how-it-works.md)。

## 数据与隐私

所有数据在 `~/.daylog`（Windows 为 `%USERPROFILE%\.daylog`）：

- `config.json`：配置；可能含明文 API 密钥。
- `frames/YYYY-MM-DD/`：截图，权限受限的本地明文文件；`observations/`：采样与状态变化。
- `batches/`、`archive/`：待分析与已处理批次；`reports/`：日记与最近的 `report.html`。
- `usage/`：每日调用次数与已知 token 用量；`logs/`：本地诊断日志（默认 7 天、32 MiB 上限）。
- `integrations/`：Notion Token 的加密文件与本机密钥，迁移时需一起保留。

已分析原图保留 3 天，日记保留 90 天；待分析截图不会被自动删除，截图总量达到 256 MiB 或待整理批次达到 96 时暂停新截图。截图只在分析时发送给你配置的 Agent 或 API 模型。旧版 `~/Library/Application Support/Daylog` 会自动迁移。文件结构与 JSON 字段见 [数据目录说明](skills/daylog/references/data-layout.md)。

## 配置

```sh
daylog config                                  # 查看配置概览（隐藏密钥）
daylog config --profile balanced|battery|detailed
daylog config --idle-minutes 20                # 无键鼠输入多久算离开；0 关闭
daylog config --agent claude,hermes            # 指定 Agent 顺序；--agent auto 恢复默认
daylog config --endpoint https://api.example.com/v1/chat/completions --model vision-model --api-key-stdin
```

| 档位 | 采样 | 分批 | 每批目标／最多图片 | 每日尝试／图片上限 |
| --- | --- | --- | --- | --- |
| balanced（默认） | 90 秒 | 15 分钟 | 4 / 8 | 192 / 1536 |
| battery | 120 秒 | 30 分钟 | 3 / 6 | 96 / 768 |
| detailed | 30 秒 | 10 分钟 | 6 / 12 | 288 / 2304 |

自定义 API 需为支持图片的 OpenAI-compatible Chat Completions 接口（填写完整请求 URL），启用后优先使用；加 `--api-fallback` 则仅在本机 Agent 全部失败后使用。密钥读取顺序：`ai.api_key` 明文 → `key_env` 指定的环境变量 → 系统凭据存储。修改后若正在记录，执行 `daylog restart` 生效。完整选项见 `daylog config -h` 和 [命令参考](skills/daylog/references/commands.md)。

电池供电默认不分析（截图照常保留，插电后整理）；低电量、锁屏、屏幕休眠或严重热压力时暂停采样，不阻止系统睡眠。

## 失败与排障

分析失败的批次保留在队列，按 15 / 30 / 60 分钟递增退避（最长 6 小时）自动重试，受每日预算约束；同一批次累计第 7 轮失败时发送一次系统通知（`config --notifications=false` 可关闭）。`daylog status --verbose` 查看重试与磁盘详情，`daylog doctor` 检查权限与模型。需要立即重试：

```sh
daylog pending --json                  # 获取批次 ID
daylog analyze --batch 批次ID --retry
```

每次请求与响应记录在 `~/.daylog/logs/llm/`（不含图片 Base64 和认证头，已过滤常见密钥），可能含工作内容，应按私有数据对待。

### 常见问题

- **更新后提示没有录屏权限（开关明明开着）？** 先用 `daylog doctor` 确认；然后在“系统设置 › 隐私与安全性”的录屏权限列表中删除旧的 Daylog 条目，重新添加 `~/Applications/Daylog.app` 并授权，再运行 `daylog restart`。
- **为什么一段时间没有新截图？** 可能处于暂停、空闲（默认 20 分钟无输入）、锁屏、低电量，或已达存储／队列上限；用 `daylog status --verbose` 查看原因。
- **为什么用电池时没有整理？** 默认 `power.analyze_on_battery` 为 `false`；插电后会自动处理积压，也可在 `config.json` 中改为 `true` 后 `daylog restart`。
- **报告或 Notion 同步会上传截图吗？** 不会。生成报告与 Notion 同步都不上传截图、不调用模型；截图只在分析时发送给已配置的模型。

## 让 Agent 操作

安装 skill 后，直接对 Claude Code、Codex 或 Hermes 说“用 Daylog 开始记录”“看看今天做了什么”“总结本周并生成报告”。Agent 读取 `daylog report --json` / `daylog today --json` 的已有结果，不会重复分析截图；读取约定见 [数据目录说明](skills/daylog/references/data-layout.md)。

## Notion 每日同步

运行 `daylog notion setup`（终端隐藏输入 Token 和目标页面链接），Daylog 建一个数据库，每天一页，详细活动写在正文。后台每天 00:00 同步前一天，`daylog notion schedule --times 12:00,18:00` 可加白天备份；手动用 `daylog notion sync --date YYYY-MM-DD`，`--dry-run` 先预览。不上传截图。详见 [Notion 同步说明](docs/notion-sync.md)。

## 开发

源码构建、测试、发布流程与签名见 [开发与发布](docs/development.md)，实测范围见 [验证记录](VALIDATION.md)。
