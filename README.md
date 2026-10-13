# Daylog

<img src="assets/icon/daylog.png" width="96" height="96" alt="Daylog：日光与日志" />

自动记录今天做了什么。macOS 14+，后台采样截图，自动调用已有 Agent 的视觉模型整理成工作日记。

## 日常只需三个命令

```sh
daylog start    # 开始记录
daylog today    # 查看今天
daylog stop     # 停止记录
```

`start` 自动创建配置、准备后台服务、检查录屏权限并开始工作。首次按 macOS 提示给 Daylog 授权，命令会等待并自动继续；不需要手动执行初始化或安装服务。重复执行不会重复启动，暂停后执行则恢复。

默认使用本机 Claude → Codex → Hermes 的已有模型配置。无需再填 API Key；模型需要支持图片，分析可能使用该 Agent 的云端服务。没有兼容 Agent 或分析失败时，截图保留在本地等待处理。Grok / Antigravity 目前仅检测。

`daylog status` 查看中文状态概览：运行时长、最近截图、采集/跳过次数、待整理批次和截图磁盘占用。时间按本机时区显示；采集计数属于本次启动，待整理和占用包含历史。Agent 或脚本使用 `daylog status --json`。

`daylog`、`daylog -h`、`daylog --help` 都显示帮助，且不会初始化数据或启动记录。`daylog today` 查看日记。首批日记通常约 15 分钟后生成；它反映屏幕活动，不等于精确工时。

## 安装与更新

把这一句话发给 Claude Code、Codex 或 Hermes：

```text
请按 https://github.com/cosin2077/daylog 的 README，为我安装 Daylog 和当前 Agent 的 skill，并验证安装成功。
```

<details>
<summary>Agent 安装步骤 / 手动安装</summary>

1. 确认系统为 macOS 14+，获取本仓库。优先使用自带双架构二进制的 [skill 包](dist/daylog-skill-macos.tar.gz)；安装器会自动选择 Apple Silicon 或 Intel，不需要用户判断架构。
2. 如果源码检出中没有 `skills/daylog/assets` 的二进制包，先检查本机已有的 Go 1.24+ 和 Xcode Command Line Tools，再运行 `sh scripts/build.sh --all-arches`。缺少发行包或构建条件时明确说明，不能把尚未发布的下载地址当作可用资源。
3. 在仓库目录执行 `python3 scripts/install-skills.py --agent codex`，按当前 Agent 改为 `claude` 或 `hermes`。如果目标 skill 已存在，先比较版本，保留用户修改，避免盲目覆盖。
4. 从安装后的 skill 目录运行 `sh scripts/install-runtime.sh`，让更新源指向持久保存的 skill 资源。也可从源码目录运行 `sh skills/daylog/scripts/install-runtime.sh`。已有安装由安装器检查更新，保留 `~/.daylog` 中的配置和记录。
5. 验证 `~/.local/bin/daylog version` 和 `~/.local/bin/daylog -h`，告知用户安装结果。安装到此完成；只有用户要求开始记录时才执行 `daylog start`，系统授权由本人完成。

直接使用解压后的 skill 包时，安装命令为：

```sh
sh daylog/scripts/install-runtime.sh
```

应用安装到 `~/Applications/Daylog.app`，命令链接到 `~/.local/bin/daylog`；若终端找不到命令，使用完整路径 `~/.local/bin/daylog`。使用预编译包安装运行时不需要 Go、Node 或 Python。

</details>

安装后，更新只需：

```sh
daylog update
```

目前更新源是随安装包登记的本地 manifest，尚未发布互联网更新服务。更新已有 SHA-256、签名完整性、版本/架构校验与替换失败回退，但尚不具备正式发行级保障：没有签名清单/固定发布者校验，两个目录重命名之间存在崩溃窗口，更新后启动失败也不会自动回滚。它由命令触发，没有后台定时更新。

本地开发签名更新可能使录屏权限失效；`daylog start` 会检查并引导重新授权。正式签名、公证与远程发布尚待完成。

## 自定义 LLM（可选）

命令行直接修改现有配置，保留其他偏好：

```sh
daylog config --endpoint https://api.example.com/v1/chat/completions --model vision-model --api-key YOUR_API_KEY
```

也可以直接编辑 `~/.daylog/config.json` 的 `ai` 部分，保留文件中其他字段：

```json
{
  "ai": {
    "enabled": true,
    "endpoint": "https://api.example.com/v1/chat/completions",
    "model": "vision-model",
    "api_key": "YOUR_API_KEY"
  }
}
```

需要支持图片的 OpenAI-compatible Chat Completions 接口；URL 是完整的请求地址，不只是域名。启用后优先使用该 API。

`--api-key` 会把密钥明文写入 `~/.daylog/config.json` 的 `ai.api_key`，也可以直接编辑这个字段。读取优先级为：JSON 明文密钥 → 指定环境变量 → 系统钥匙串。环境变量和 `--api-key-stdin` 钥匙串方式均为可选。

修改后若正在记录，执行 `daylog stop`、`daylog start` 生效。`daylog config --agent auto` 切回本机 Agent；`daylog config -h` 查看完整方法。

## 所有使用数据都在 ~/.daylog

- `config.json`：配置，默认即可使用。
- `observations/YYYY-MM-DD.jsonl`：采样和状态变化；重复画面也保留时间线索。
- `frames/YYYY-MM-DD/`：按本地日期保存的新截图；旧版平铺截图兼容读取。
- `reports/`：已分析的日记；`reports/report.html` 是最近生成的可视化快照。
- 同一目录还保存待分析批次、日志和运行状态。

旧版 `~/Library/Application Support/Daylog` 会整体迁移，保留历史和配置；新旧目录同时存在时拒绝覆盖。App 与 macOS 要求的后台注册文件仍放在系统约定位置。

默认截取主屏；可通过配置选择窗口和排除敏感应用。截图是权限受限的本地明文文件。已分析原图保留 3 天，日记保留 90 天，待分析证据不会被自动清理。

## 在 macOS 上以什么形式运行

Daylog 是 **Go 命令行工具 + macOS App 包 + 用户级 LaunchAgent 后台服务**。`~/Applications/Daylog.app` 装有同一个 Go 可执行文件、权限身份和图标；`~/.local/bin/daylog` 是它的命令入口。App 目前没有独立窗口、菜单栏界面或 Dock 常驻界面；报告在浏览器打开。

`daylog start` 启动后台进程，退出终端或 Agent 对话不会停止记录；`daylog stop` 停止后台。默认不登录启动，按需使用 `start --login`。后台使用较低进程优先级和低优先级磁盘 I/O；截图由少量原生桥接代码调用 ScreenCaptureKit。

## 开始记录后会发生什么

`daylog start` 准备后台服务并检查权限，按电源和空闲策略采样。相似画面复用同批截图，仍保存观察时间；应用切换、锁屏、空闲和采样中断留下状态边界。默认约 15 分钟封批，目标选 4 张、最多 8 张，优先保留应用切换，再兼顾内容变化和时间分布。稳定画面可能只需一张。旧配置中明确设置的上限继续生效。

后台每分钟检查待分析队列：优先使用自定义 API，否则尝试 Claude → Codex → Hermes。每次提交新批截图及最近 30 分钟内最多 5 个任务的短摘要，模型可关联已知任务；程序分配稳定的任务／活动 ID。失败保留队列，受重试、每日 48 次尝试和 256 张提交图片预算约束，重试同样计费计数。Agent 内部请求次数/token 不一定可知；API 返回的 token 用量会记录到 `usage/`。

**模型辨认活动，程序估算时间。** 新版模型只返回任务、对象、动作、置信度和截图证据，不输出时间。仅同一片段内相邻、同应用、间隔足够短且没有中断的有效观察计入估算；未选中的图仅在前后关键帧识别为同一任务、间隔有界且中间连续采样/同应用时做受限插值，并标记 `interpolated_seconds`；长间隔、切换边界和低置信识别记为未知。它仍是屏幕活动估计，不证明人在持续专注，也不能保证短暂动作不遗漏。旧版报告继续显示，单独标记为旧版模型时间范围。

内置提示词与 [JSON Schema](internal/journal/analysis_schema.json) 要求只描述可见事实，不能把编辑当完成或把截图文字当指令，不抄录凭据和完整聊天。运行 `daylog prompt` 查看新版提示词；升级前待处理批次仍使用旧格式。新报告保存 `schema_version: 2`、任务/活动 ID、实际估算区间、未知区间、提示词版本及输入摘要哈希。已提交结果不可覆盖；人工修订和原生编码前去重等后续事项见 [设计与实施状态](docs/analysis-design.md)。

## 日／周／月可视化报告

```sh
daylog report
```

浏览器中打开一个离线 HTML，可直接切换日报、周报、月报，查看活动时间线、分类、日期分布与待分析记录。支持关键词搜索、筛选、分页和浏览器打印 / 保存 PDF；移动窗口使用单栏布局。

日报按本地日历日，周报按周一到周日，月报按自然月；跨日片段拆分展示，重叠时间做合并统计。“记录覆盖”合并新版有效估算区间与旧版模型区间；报告分别显示观察估计、旧版范围和已分析批次内的未知时间。未知量不代表全天所有缺口，未记录或未分析时段也不代表没有工作。

周／月报告复用已有模型分析结果进行本地汇总，不会重新上传截图或额外请求模型。当前概述是统计性描述，尚未增加第二层 LLM 长文总结。报告按需生成，避免后台反复扫描整月；输出到 `~/.daylog/reports/report.html`，下次生成替换上一次快照。HTML 不附带原始图片或模型配置，也不会读取配置中的密钥；不依赖远程脚本、字体或服务。它仍包含日记内容，是私有明文文件。

```sh
daylog report --period week              # 本周视图
daylog report --period month             # 本月视图
daylog report --date 2026-10-13           # 指定日期所在的日／周／月
daylog report --period week --json       # 供 Agent 读取的结构化汇总
daylog report --no-open                  # 只生成 HTML
daylog report --from 2026-10-13T14:00:00+08:00 --to 2026-10-13T16:00:00+08:00 --json
```

生成后是静态快照，新活动需要重新运行 `daylog report` 才会更新。[示例报告](dist/daylog-report-demo.html) 使用演示数据。

## 省资源的默认行为

截图间隔可配置：默认插电时至少间隔 90 秒，电池时约 120 秒。相似画面不重复存图，但保留观察时间；每个新批次会重新留一张当前画面。每批目标 4 张、最多 8 张。电池供电默认不分析；低电量、锁屏、屏幕休眠或严重热压力暂停，不阻止系统睡眠。

在 `~/.daylog/config.json` 中，`min_save_seconds` 控制最小截图间隔，`interval_seconds` 控制检查周期。例如要在插电时约每 30 秒采样，把两者都设为 `30`，保留其他配置；电池与低电量模式仍分别使用 `power.battery_interval_seconds`、`power.low_power_interval_seconds` 降频。检查周期不能大于最小截图间隔，实际采样还受空闲、锁屏等策略约束。修改后重新启动正在运行的记录即可生效，也可以直接让 Agent 帮你调整。

也可直接选择档位，修改后重新启动记录：

```sh
daylog config --profile balanced  # 默认：90 秒采样，15 分钟分批，目标 4 / 最多 8 张
daylog config --profile battery   # 120 秒采样，30 分钟分批，目标 3 / 最多 6 张
daylog config --profile detailed  # 30 秒采样，10 分钟分批，目标 6 / 最多 12 张
```

电池采样分别为 120 / 180 / 120 秒；档位不启用电池分析，也不修改模型、密钥、屏蔽应用和窗口设置。高级配置包括 `target_images_per_batch`、`max_images_per_batch`、`max_images_per_day`、`max_observation_gap_seconds`（连续估算最大间隔，默认 300 秒，相邻采样还取实际采样间隔的两倍作为更严格上限；插值两端关键帧也不能超过此配置上限）。`heartbeat_seconds` 为旧配置保留；v2 使用真实重复观察替代定时保存重复图。

Go 后台和少量原生代码调用 ScreenCaptureKit，无 WebView、Node 或第三方 Go 依赖。旧版已有实机后台内存短样本约 27–30 MiB（不代表新版测量）；实际电池续航尚未测量。小网格可能漏掉细微文字修改，长时间无输入的阅读也可能被当作空闲。

## 让 Agent 操作

配套 skill 可供 Claude Code、Codex、Hermes 使用。源码目录中运行：

```sh
python3 scripts/install-skills.py --agent all
```

脚本只安装 skill 的说明、资源和二进制包；复制 skill 不会直接安装或运行 App。向 Agent 请求安装/使用后，它运行 skill 内的安装器，按 Mac 硬件选择 arm64 或 amd64，无需现场编译。

之后直接说“用 Daylog 开始记录”“看看今天做了什么”“停止记录”。Agent 使用同一套简单入口；用户无需理解截图批次或服务管理。

**安装之后，也可以直接让 Agent 解读已有记录：**

> 用 Daylog 看看今天下午我主要在忙什么，大概花了多久，哪些时段还没有分析。
>
> 根据 Daylog 总结本周做了哪些事，并生成一份可视化报告。

给 Agent 的读取约定：先用 `daylog report --period day|week|month --date YYYY-MM-DD --json` 获取相关时间范围的活动、记录覆盖、待分析和未封批数量；需要原始卡片时用 `daylog today --date YYYY-MM-DD --json`。仅查询某几小时则使用 `report --from RFC3339 --to RFC3339 --json`；时间必须带时区，程序会裁剪并合并有效区间。普通总结复用已有 JSON，不启动截图或重复分析图片。用人话概括任务和可见进展，将时间标为估计覆盖，并说明缺口；不把空白当休息。

需要可视化时执行 `daylog report --no-open`，返回 HTML 路径。详细的 [数据目录与 JSON 读取说明](skills/daylog/references/data-layout.md) 随 skill 一起分发，包含 `frames`、`batches`、`archive`、`reports`、`draft.json` 的用途。`config.json` 可能含密钥，回答活动问题无需读取它。

高级设置、自定义 API、指定窗口和排障见 [命令参考](skills/daylog/references/commands.md) 或 `daylog help --advanced`。默认不启用登录启动；需要时使用 `daylog start --login`，`daylog stop` 会一并关闭它。

开发：`go test -race ./...`、`go vet ./...`、`sh scripts/build.sh --all-arches`。实测范围见 [验证记录](VALIDATION.md)。

图标源文件为 [daylog.svg](assets/icon/daylog.svg)，修改后在 macOS 执行 `sh scripts/build-icon.sh`（需要本机 `rsvg-convert`）重新生成 PNG / ICNS。普通构建直接使用仓库内的 ICNS，不增加安装或运行依赖。
