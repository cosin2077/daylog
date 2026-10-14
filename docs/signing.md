# 固定发布签名

Daylog 的正式构建使用长期自签名代码签名证书，并固定 Bundle ID `io.daylog.agent`。签名的 designated requirement 锚定证书；二进制内容变更不改变此身份。它不是 Apple Developer ID，不提供 Apple 公证，也不保证消除首次安装的 Gatekeeper 提示。

用户只安装 App，无需导入证书、配置钥匙串或关闭 Gatekeeper。旧 ad-hoc 版本迁移到此身份可能需重新授权一次。后续跨版本是否保持授权应通过 App 拥有的 doctor 检查及真实截图确认，不能只看系统开关或签名校验。

## 维护者身份

- 公共证书：`internal/update/signing-certificate.der`。安装器同时固定其 SHA-1（Apple requirement 表达形式）和 SHA-256；更新器验证 requirement 并比较完整证书 DER。
- 私有身份在仓库外生成一次：`python3 scripts/create-signing-identity.py --directory <private-directory>`。目录必须不存在，避免意外轮换证书。
- 生成的 `identity.p12` 和 `private-key.pem` 均加密，`password` 文件及目录使用私有权限。将加密身份及密码另行安全保管；不要提交仓库或在日志展示。证书/私钥丢失或更换不能当成普通升级处理。
- GitHub 仓库 Secrets：`DAYLOG_SIGNING_P12_BASE64`（P12 的 Base64）和 `DAYLOG_SIGNING_PASSWORD`。只有标签发布工作流的签名步骤使用，普通 PR/CI 不接触私钥。

## 构建

```sh
python3 scripts/with-signing.py --identity-dir <private-directory> -- sh scripts/build.sh --all-arches
python3 scripts/package-release.py --version 0.6.0
```

包装器创建临时钥匙串。Apple 签名工具需要构建机认可签名身份，因此仅在构建期间添加该证书的 codeSign 用途信任，并加入临时钥匙串搜索项；结束时移除本任务添加的信任和搜索项，删除临时钥匙串。已有同证书信任不覆盖、不删除。CI 在一次性 macOS runner 中使用受限 codeSign 信任，公开证书放入临时使用的系统证书存储，私钥始终留在 runner 用户的临时钥匙串；结束后删除本任务添加的公开证书；用户运行 App 时不需要这些设置。

GitHub 托管 runner 必须有 `RUNNER_ENVIRONMENT=github-hosted` 才采用一次性 VM 的信任清理策略：公开的 codeSign 信任记录随 VM 销毁；公开证书副本、私钥、P12 和临时钥匙串在签名步骤结束时删除。本地及自托管环境仍要求显式移除本任务添加的信任，不能套用这项例外。

不把 P12/密码传给实际编译子进程。失败和中断走清理路径；若清理失败会报告，不假装成功。强制杀死构建进程或断电仍应检查构建机是否有遗留临时钥匙串；CI runner 销毁后不持久保留。

正式发布必须有固定证书，`package-release.py` 还会逐个验证 App。缺少 Secrets、签名不匹配或者仅 ad-hoc 签名均不得发布，不允许自动降级。

普通开发/CI 可显式使用 `DAYLOG_DEVELOPMENT_BUILD=1` 构建 ad-hoc 包。其更新器仅允许本地/loopback 开发来源接受 ad-hoc；正式二进制始终固定证书。开发安装还需 `DAYLOG_DEVELOPMENT_INSTALL=1` 且清单和附件都来自本地/loopback，不能为互联网安装跳过身份校验。

## 验收

1. 构建 A/B 两个不同版本，验证 Mach-O 哈希不同、designated requirement 一致。
2. A 首次授权并截图；保持系统授权不动，通过 `daylog update` 升级到 B。
3. B 的 App 录屏预检必须为 true，并实际保存新截图；验证配置哈希和后台恢复。
4. 测试包哈希正确但签名不符的包必须在停止旧进程前被拒绝，首次/重复安装也需拒绝。
5. 完成云端构建后再次校验固定证书，从公开更新源验证本机升级。

更新清单仍依赖 HTTPS/GitHub 分发。固定证书保证更新 App 的发布身份，并非 Apple 的开发者背书。证书轮换需要显式迁移设计，不能直接覆盖公共证书和 Secrets 后假装老客户端能自动接受。

首次接入 Secrets 或修改签名脚本时，可手动运行 Release workflow 并开启 `verify_only`：构建当前所选 ref，tag 输入只指定数字版本，不创建 Release。预检通过后再给该提交打正式标签，避免反复创建失败的版本标签。
