修复活动采集与分析恢复，并重新设计离线报告。

- 修复 macOS 长驻进程前台应用识别缓存，恢复失败批次的指数退避重试。
- 简化识图提示词和返回格式，默认 low effort；支持按指定 Agent 顺序分析，以及可选的自定义 API/备用模型回退。
- 增加私有 LLM 请求/响应 JSONL 日志，包含错误分类、脱敏、轮转和保留上限。
- 提高默认调用/图片预算，保留已有用户配置；更新和分析显示终端进度。
- 重做日/周/月离线报告，提供实际观察区间的时间统计、任务、失败原因和重试命令。
- 连续重试失败超过五次后提交一次系统通知；可用 `daylog config --notifications=false` 关闭。

已有用户：运行 `daylog update`。新用户：把下面这句话发给 Agent：

> 请按 https://github.com/cosin2077/daylog 的 README，为我安装 Daylog 和当前 Agent 的 skill，并验证安装成功。

安装器自动选择 Apple Silicon / Intel；安装不会开始录屏。配置和历史数据保留。此版本仍使用 ad-hoc 开发签名，尚未完成 Developer ID 签名与公证：macOS 可能在升级后要求刷新原 Daylog 录屏授权。校验签名完整性和 SHA-256 不等同于 Developer ID 发布者认证；首次安装和系统认证仍需用户本人操作。
