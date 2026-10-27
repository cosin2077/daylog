# 开发、构建与发布

面向维护者。普通用户只需看 [README](../README.md)。

## 本地开发

```sh
go test -race ./...
go vet ./...
DAYLOG_DEVELOPMENT_BUILD=1 sh scripts/build.sh --all-arches
```

离线安装测试：先运行上面的构建，再从 skill 目录执行 `DAYLOG_DEVELOPMENT_INSTALL=1 sh scripts/install-runtime.sh --source "$PWD/assets/release.json"`。此模式使用本地包和更新源；普通用户保持默认在线方式。开发包需要显式的本地/回环开发开关，不能通过正式发布的打包检查。

报告界面可用合成数据预览（不读取用户数据）：`go run ./scripts/report-demo OUTPUT/report.html`，可选 `--failure`、`--unrecognized`、`--interpolated`。

图标源文件为 [daylog.svg](../assets/icon/daylog.svg)，修改后在 macOS 执行 `sh scripts/build-icon.sh`（需要本机 `rsvg-convert`）重新生成 PNG / ICNS。普通构建直接使用仓库内的 ICNS，不增加安装或运行依赖。

实测范围见 [验证记录](../VALIDATION.md)。

## GitHub 自动构建与发布

- PR 和 `main` 推送：在 macOS 15 的 arm64 / Intel runner 上运行测试、构建和隔离安装验证；Windows 2022 runner 运行 Go 测试、PowerShell 5.1/7 安装与后台/自更新验证。
- 推送 `v0.7.0` 这样的数字版本标签：构建 macOS 双架构包和 Windows x64 包，生成版本固定的更新清单和轻量 skill 包，上传 Actions artifact 并创建 **Release 草稿**；另在 Windows runner 验证正式签名包的安装与自更新。
- 也可手动运行 “Build release” workflow，传入已经存在的标签。重跑只替换同标签草稿附件；已发布版本拒绝覆盖。
- 维护者验证后将草稿发布为正式 Release，并设为 Latest，Agent 默认安装及 `daylog update` 才会使用它。CI 不会自动开始用户录屏。

版本标签应指向已提交的发布代码。配置步骤、产物与验证边界见 [发布说明](releasing.md)。

## 签名与更新安全

正式构建使用同一张长期自签名代码签名证书，安装器和更新器都固定（pin）该证书。已在本机验证：0.5.0→0.5.1 的二进制发生变化，录屏授权保留且成功截图，无需再次授权；从旧版临时签名迁移时可能需要授权一次。此签名不是 Apple Developer ID，尚无 Apple 公证，首次安装仍可能有系统安全提示。私有签名身份只在维护者构建机上。维护流程见 [固定签名说明](signing.md)。

更新清单中的二进制 URL 固定到版本标签；`--source` 可用于维护者的本地验证。更新验证 SHA-256、固定发布证书、签名完整性和版本/架构，校验通过后才停止旧进程。清单本身尚未独立签名；两个目录重命名之间仍存在崩溃窗口，启动失败也不会自动回滚。

Windows 包和更新使用固定发布者的 RSA 签名校验，绑定版本、平台和可执行文件摘要；它不等同于 Authenticode 或 SmartScreen 信誉。Windows 适配使用 `go-winio` 和 `x/sys`，许可随包分发。真实用户桌面、多屏/HDR/受保护内容和电池表现仍需要 Windows 实机验证；见 [Windows 说明](../skills/daylog/references/windows.md)。

## 运行形态与资源

macOS 上 Daylog 是 Go 命令行工具 + App 包 + 用户级 LaunchAgent。`~/Applications/Daylog.app` 装有同一个 Go 可执行文件、权限身份和图标；`~/.local/bin/daylog` 是它的命令入口。后台使用较低进程优先级和低优先级磁盘 I/O；截图由少量原生桥接代码调用 ScreenCaptureKit。Windows 首版使用低频按需 GDI 截图，原生缩小后编码 JPEG；指定窗口只捕获当前可见、前台窗口的客户区。采集无需 WebView 或 Node。

旧版 macOS 已有实机后台内存短样本约 27–30 MiB（不代表新版或 Windows 测量）；实际电池续航尚未测量。小网格可能漏掉细微文字修改，长时间无输入的阅读也可能被当作空闲。
