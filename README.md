# Daylog

面向 Agent 的 macOS 工作日记。Go 负责后台、采样、存储和模型请求，小型 Objective-C 桥调用系统 ScreenCaptureKit、硬件空闲、电源/温度和 Keychain。无 WebView、Node、数据库驱动或第三方 Go 依赖。产品思路参考 [Daygo](https://github.com/Jwz-git/Daygo) 和 [Dayflow](https://github.com/JerryZLiu/Dayflow)，代码为独立实现。

首版 macOS 14+，提供 Apple Silicon 与 Intel 二进制。它记录可见活动，不把截图摘要当作精确工时或任务完成证明。原生截图与实际续航需要真机验证，见 [验证记录](VALIDATION.md)。

## 本地构建

开发需要 Go 1.24+ 与 Xcode Command Line Tools；使用打好的安装包不需要这些依赖。

```sh
go test -race ./...
go vet ./...
sh scripts/build.sh --all-arches
```

产物：`dist/Daylog.app` 和自带可执行文件的 `dist/daylog-skill-macos.tar.gz`。当前是本地 ad-hoc 签名，不是 Developer ID 签名/公证；不能承诺其他 Mac 上无 Gatekeeper 阻挡或升级后权限不变。未发布远程安装地址。

## 安装与启动

```sh
sh skills/daylog/scripts/install-runtime.sh
~/.local/bin/daylog init
~/.local/bin/daylog doctor
~/.local/bin/daylog permission
~/.local/bin/daylog service install
~/.local/bin/daylog service start
```

安装不启动采集。屏幕权限必须由用户在 macOS 中授予；修改后可能需重启。`service install --login` 才启用登录启动，默认不启用。该 `.app` 是命令行宿主和稳定的权限归属，不带图形窗口；通过 Agent 或 CLI 操作。

## Agent skills

```sh
python3 scripts/install-skills.py --agent all
# 也可分别 --agent codex / claude / hermes
```

一份标准 SKILL.md，分别安装到 `~/.agents/skills/daylog`、`~/.claude/skills/daylog`、`~/.hermes/skills/daylog`。脚本拒绝覆盖已有 skill。安装包自包含，可先解压 skill，再从任意位置运行其中的 runtime 安装脚本。

Agent 可执行：安装、启动、暂停、停止、查询已分析日记，以及读取待处理截图、生成证据化 JSON、经 CLI 校验入库。启动记录后，默认按 Claude → Codex → Hermes 尝试本机 CLI，使用各自现有模型/鉴权，不重新配置凭据。默认模型仍需支持视觉；未登录、接口不兼容或返回无效记录时会保留待处理批次。Grok 与 Antigravity 当前仅检测，没有可验证的图像调用适配。

也可明确配置视觉 Chat Completions 服务，该配置优先于本机 Agent；一轮请求生成一批卡片。额外 API 默认关闭，并不表示本机 Agent 完全离线。CLI 一次分析可能含其内部重试；Daylog 的调用预算不等于该 Agent 的实际 HTTP 请求数。Claude/Codex 尽量使用不保存会话的模式，Hermes 仍可能按自己的配置保存会话，Daylog 不修改其全局设置。

```sh
daylog today --json
daylog pending --limit 1
daylog commit --file report.json
daylog pause --minutes 30
daylog stop
daylog agents
daylog update --check
daylog update
```

详细配置、凭据、窗口选择和 Agent 操作见 [skill 命令参考](skills/daylog/references/commands.md)。

## 自更新

安装器按实际 Mac 硬件选择 arm64/amd64，并登记随 skill 附带的本地 `assets/release.json`。以后运行 `daylog update` 即可读取该源；本地源内容更新后才会出现新版本。发布者也可托管相同格式的 HTTPS manifest 与二进制，使用 `daylog update --source https://你的域名/release.json --remember --check` 切换。当前没有发布远端服务，不宣称已经能从互联网取得新版本。

更新前校验 SHA-256、应用代码签名、bundle ID、版本与 Mach-O 架构；全部通过后才停止录制并原子替换，保留旧包用于替换失败回退。已运行的 LaunchAgent 会尝试恢复；手动前台运行的进程会提示需手动重启。用户数据和凭据不参与包替换。信任 manifest 的来源仍是前提，SHA 校验不等于正式发行身份认证；ad-hoc 更新也不能保证 macOS 权限保持。

## 默认资源策略

| 约束 | 默认行为 |
|---|---|
| 电源检查 | 插电 30 秒，电池 120 秒，低电量模式 180 秒 |
| 截图门限 | 至少间隔 90 秒才截图；RGB 小网格差异决定保存，静止画面只留低频证据 |
| 图片 | JPEG 60，720 高度、最大 1920 宽；体积依画面变化，无固定几十 KB 保证 |
| 电池和系统 | 20% 及以下电池、锁屏、屏幕休眠、严重热压力暂停；不阻止睡眠 |
| 优先级 | nice +10、LaunchAgent Background/低优先级 I/O、截图 Utility QoS |
| 模型 | 每批最多 6 张，每日最多 48 次分析尝试；电池默认不启动分析，电源策略变化在下一轮检查生效 |
| 原始数据 | 256 MiB 硬上限、最多 96 个待分析批次；待处理证据不被自动清理 |
| 留存 | 已分析原图 3 天，文本报告 90 天；日期索引减少历史扫描 |

提高频率、关掉空闲跳过或延长留存，会增加资源消耗。默认硬件空闲 5 分钟会漏掉长时间无输入的阅读；这属于明确取舍，可配置关闭。只有主屏或指定窗口，未做跨显示器合并、音频、浏览器网站级脱敏或完整 GUI。

采集和存储位于本机。自动/手动调用 Agent 查看图片会走该 Agent 的视觉服务；启用额外 API 后图片送往所选服务。数据与报告是权限受限的明文文件，Keychain 仅保护凭据，不保护日记。屏蔽名单默认为空，启动前应配置自己的敏感应用。卸载保留历史，不自动删除数据。后台不常驻 Agent 客户端，分析时才启动一个；CLI 分析阶段仍会暂时增加 CPU/内存占用。

Skill 规范来源（2026-10-12 核对）：[Codex](https://developers.openai.com/codex/skills)、[Claude Code](https://code.claude.com/docs/en/skills)、[Hermes](https://hermes-agent.nousresearch.com/docs/user-guide/features/skills)。同一份 skill 可安装到三者目录，不意味着三个客户端的真实视觉/授权闭环均已跑过。
