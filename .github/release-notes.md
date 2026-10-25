Notion Token 改为随 Daylog 数据目录迁移的本地加密凭据，不再依赖系统钥匙串。

- Token 保存在 `~/.daylog/integrations/credentials.enc`，使用同目录的 32 字节随机密钥和 AES-256-GCM 加密；目录 `0700`、文件 `0600`。
- 旧 macOS 钥匙串或 Windows Credential Manager 条目在首次读取时迁移；只有加密文件写入并回读一致后才删除旧条目。写入失败或文件损坏时保留原凭据并明确报错。
- Token 不出现在配置、日志或报告；`notion status` 只报告本地加密文件状态，不读取 Token。
- 备份或迁移 `~/.daylog` 时需同时保留 `credentials.key` 与 `credentials.enc`。

注意：密钥与密文位于同一数据目录，可避免明文暴露和系统钥匙串弹窗，但不能抵御能以当前用户身份读取整个数据目录的恶意程序。
