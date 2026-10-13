# 构建与发布

用户入口保持一句话：让 Agent 按 README 安装 Daylog 和当前 Agent 的 skill。维护者负责发布二进制；用户不需要 Go 或 Xcode。

## Workflow

`.github/workflows/ci.yml` 在 PR / main 推送时运行 arm64 (`macos-15`) 和 Intel (`macos-15-intel`) 测试、构建、隔离运行及安装器验证。各架构独立执行，不共享构建目录。Actions 固定到已核对的 commit SHA。

`.github/workflows/release.yml` 响应 `v*` 标签和手动触发，严格验证 `vMAJOR.MINOR.PATCH`，检出该标签。它在 arm64 macOS runner 上测试并交叉构建两个架构，上传 Actions artifact，然后创建 Release 草稿；Intel 原生执行由 CI 覆盖。手动输入必须是已存在标签，不能用任意分支代替。Release 任务有 contents:write 权限，PR CI 只有 contents:read。

首次使用：仓库 Settings → Actions 允许使用 GitHub Actions 及引用的官方 actions。如果组织限制 `GITHUB_TOKEN` 创建 Release，需管理员调整对应权限；不需要额外 PAT。当前流程不需要 Apple 证书 secrets。

## 发布一个版本

先将发布改动提交并推送到 main，确认 CI 的两个架构通过，再创建指向该提交的标签，例如：

```sh
git tag -a v0.4.0 -m "Daylog 0.4.0"
git push origin v0.4.0
```

这两个命令会创建/推送标签，不是构建脚本自动执行的步骤。不要把未提交的本地改动当作标签内容。

在 Actions 中查看 “Build release”。工作流会产出：

```text
daylog-macos-arm64.tar.gz
daylog-macos-amd64.tar.gz
daylog-skill.tar.gz
release.json
checksums.txt
```

`daylog-skill.tar.gz` 是轻量说明和安装器，不含双份二进制。运行它的安装器时，仅下载用户平台的包。源码的 skill 也支持相同流程。`checksums.txt` 覆盖两个 App 包、skill 包和 manifest；它不是签名清单。

草稿在维护者验证后手动发布为正式 Release，并设为 Latest。草稿/预发布不通过 `releases/latest/download/...` 提供，因此第一次公开发布前，默认安装器会提示无可用 Release。重跑同标签 workflow 可更新草稿附件；已发布 Release 不允许本流程覆盖，修复应使用新版本标签。

## 更新源

固定发现地址：

```text
https://github.com/cosin2077/daylog/releases/latest/download/release.json
```

清单中的安装包地址固定到 `releases/download/v0.4.0/...` 这样的具体标签。安装器登记在线发现地址，不会把临时下载目录写成长期更新源。已有旧版本仍记住本地源时，可在有正式 Release 后运行：

```sh
daylog update --source https://github.com/cosin2077/daylog/releases/latest/download/release.json --remember
```

该命令会尝试升级，可能停止并重启原有后台；升级后的录屏授权需本人按系统提示处理。

## 本地验证

```sh
go test -race ./...
go vet ./...
DAYLOG_VERSION=0.4.0 sh scripts/build.sh --all-arches
python3 scripts/verify-runtime.py --skip-native-check
python3 scripts/verify-installer.py
python3 scripts/package-release.py --version 0.4.0 --repository cosin2077/daylog
```

发布附件位于 `dist/release/`；`skills/daylog/assets/release.json` 保留本地相对路径，供离线测试使用。普通 build 只把本轮实际构建的架构写进清单，不会混入遗留的另一架构旧包；打包 Release 必须提供同版本双架构。

安装器测试使用隔离临时目录和本地 HTTP 服务，实际验证下载、平台选择、校验、重复安装及更新源持久化。HTTP 仅允许显式 localhost/127.0.0.1 的开发验证；正式下载和重定向使用 HTTPS。所有测试数据均为合成资料，不调用真实模型或录制桌面。

## 当前发布边界

当前包使用 ad-hoc 开发签名，未做 Developer ID 签名或 Apple 公证。没有修改系统 Gatekeeper、TCC 或移除隔离属性的安装步骤。干净 Mac 的首次下载/启动、录屏授权和升级授权连续性仍需实测。

正式签名应在 App 组装后、打包前完成；公证和 staple 后再打包并计算最终 SHA-256。签名证书及公证凭据应使用受保护的发布 secrets，不提供给 PR 构建。

更新器已有哈希、包边界、版本/架构和签名完整性校验，但没有固定发布者验证、更新器并发锁或启动失败自动回滚。安装器的互斥锁只防止它自身并行执行，不等同于更新器完整事务保障。不要将 CI 构建通过当作正式发行安全性或电池续航证明。
