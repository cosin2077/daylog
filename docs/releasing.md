# 构建与发布

用户入口保持一句话：让 Agent 按 README 安装 Daylog 和当前 Agent 的 skill。维护者发布对应平台的二进制，用户无需 Go、Xcode 或 Python。

## Workflow

`.github/workflows/ci.yml` 在 PR / main 推送时运行 macOS arm64 (`macos-15`) 和 Intel (`macos-15-intel`) 的 race 测试、静态检查、开发构建、隔离运行和安装验证。各架构独立执行，不共享构建目录。`.github/workflows/windows.yml` 在 Windows Server 2022 runner 上运行 Windows 测试、静态检查、PowerShell 5.1/7 安装、合成采集和后台/自更新验证。Actions 固定到 commit SHA。

`.github/workflows/release.yml` 响应 `v*` 标签和手动触发，严格验证 `vMAJOR.MINOR.PATCH`。普通发布检出已存在的标签，在 macOS runner 上完成测试和固定签名构建，产出 macOS arm64 / amd64 及 Windows x64 包，上传 Actions artifact 并创建 Release 草稿。随后 `windows-release-check` 在 Windows runner 上验证固定发布者签名包的安装和自更新。Intel 原生执行由 CI 覆盖。

普通 PR / CI 没有签名私钥。Release 签名步骤使用仓库 Secrets `DAYLOG_SIGNING_P12_BASE64` 和 `DAYLOG_SIGNING_PASSWORD`，缺少时失败，禁止自动降级为开发包；配置见 [固定签名说明](signing.md)。创建草稿的任务有 `contents:write`，Windows 签名检查只有 `contents:read`。

## 发布一个版本

先将发布改动提交并推送到 main，确认 macOS 两个架构和 Windows CI 均成功。可以先对当前 ref 运行签名预检：

```sh
gh workflow run release.yml --ref main -f tag=v0.8.0 -f verify_only=true
```

`verify_only` 使用所选 ref 的代码，tag 输入仅指定版本；它不要求标签存在，也不创建 Release。预检成功后，再创建指向已验证提交的标签：

```sh
git tag -a v0.8.0 -m "Daylog 0.8.0"
git push origin v0.8.0
```

这些是维护者显式执行的发布操作，构建脚本不会自动推送。普通手动发布必须输入已存在的标签，不能把未提交改动或任意分支当作该标签内容。

“Build release” 产物为：

```text
daylog-macos-arm64.tar.gz
daylog-macos-amd64.tar.gz
daylog-windows-amd64.zip
daylog-skill.tar.gz
release.json
checksums.txt
```

Windows ZIP 含 `daylog.exe`、`signature.json` 和两份许可说明。轻量 skill 包仅含说明和安装器，不附带运行时；安装器只下载用户平台的包。`checksums.txt` 覆盖安装包、skill 包及 manifest，本身不是签名清单。

草稿可能在 Windows 签名检查结束前已创建。必须等待整个工作流成功，再下载核对产物、版本和固定签名，才能公开为正式 Release 并设为 Latest。草稿/预发布不通过 `releases/latest/download/...` 提供；没有对应平台附件时安装器明确拒绝。重跑同标签 workflow 只能更新草稿附件，已发布版本必须使用新标签。

## 更新源

固定发现地址：

```text
https://github.com/cosin2077/daylog/releases/latest/download/release.json
```

清单中的包地址固定到具体版本标签。安装器不会把临时下载清单写成长期更新源；需要切换已有安装的更新源时：

```sh
daylog update --source https://github.com/cosin2077/daylog/releases/latest/download/release.json --remember
```

该命令可能停止并重启后台。macOS 录屏授权由用户按系统提示处理；Windows 后台运行在当前用户会话。

## 本地验证

macOS 普通开发构建使用显式开发标记：

```sh
go test -race ./...
go vet ./...
DAYLOG_DEVELOPMENT_BUILD=1 DAYLOG_VERSION=0.8.0 sh scripts/build.sh --all-arches
python3 scripts/verify-runtime.py --skip-native-check
python3 scripts/verify-installer.py
```

`--all-arches` 同时生成 Windows 包。正式包使用签名包装器构建，再执行 `python3 scripts/package-release.py --version 0.8.0`；开发包不能通过正式打包检查。发布附件位于 `dist/release/`，本地 `skills/daylog/assets/release.json` 保留相对路径。打包要求三个平台包均为本轮同一版本。

Windows runner 用 `DAYLOG_DEVELOPMENT_BUILD=1` 环境变量运行 `python scripts/build-windows.py`，再运行 `python scripts/verify-windows.py`。正式签名检查使用 `python scripts/verify-windows-signed.py <release-assets-directory>`。测试仅使用独立临时目录、合成图片和本地结果，不调用真实模型；安装脚本遵守系统执行策略。

## 当前发布边界

macOS 正式包使用固定长期自签名证书，开发包才使用 ad-hoc；尚无 Developer ID 签名或 Apple 公证。Windows 独立 RSA 签名固定发布身份，但不是 Authenticode，也不保证 SmartScreen 信誉。安装不要求导入证书、关闭 Gatekeeper、修改 TCC 或降低 PowerShell 执行策略。

安装器和更新器验证包摘要、版本/架构和固定发布身份；更新器有并发锁，第二次重命名失败时尝试恢复旧包。两次重命名间的崩溃和新版本启动失败仍没有自动回滚。清单仍依赖 HTTPS/GitHub 分发，不能把摘要文件当作独立签名。

Windows Server CI 的安装与进程测试不等同于 Windows 10/11 用户桌面实测。多显示器、混合 DPI、HDR/DRM、RDP、电池续航以及真实 Agent 登录/视觉调用仍需相应设备验证；不将编译、合成图片或 CI 成功作为这些结论的依据。
