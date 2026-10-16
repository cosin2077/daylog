本版新增 Windows 10 1809+ / Windows 11 x64 支持，并修复日记时长、状态查询和分析故障诊断。现有分析、时间碎片和日／周／月报告继续共用。

- Windows 原生低频截图、前台应用识别、空闲／锁屏／显示器及电池状态检测，沿用采样、去重和省电策略。
- 当前用户后台和私有控制管道；默认不开机自启，`start --login` 显式启用，停止时移除。
- PowerShell 5.1/7 安装器、用户私有数据权限和 Credential Manager 凭据存储，无需安装 Go、Python 或 Node 来运行 Daylog。
- Windows Agent 进程通过 Job Object 管理，取消时退出整个子进程树；支持原生 CLI 和已适配的 Claude/Codex npm 入口。
- Windows 包验证固定发布者 RSA 签名、版本、架构和摘要，支持运行中自更新与下次命令清理已完成升级的备份。
- 单帧活动显示“时长未估”，不足一分钟的有效估计显示秒数；`today` 先给摘要，默认列出 20 条，可用 `--full` 或 `--limit` 展开，JSON 仍导出完整记录。
- 停止时 `status --json` 正常返回 `running:false`；控制连接异常与停止状态分开。macOS `doctor` 失败时显示中文原因、应用路径与下一步，JSON 不虚构权限状态。
- Agent 空终态不再被当作成功；终态错误、无完整结果、超时及期间发生的上游重试分别诊断。新增 `config --agent-timeout 240`，默认时限和回退顺序不变。
- 统一参数错误与近似命令提示，拒绝多余位置参数；pending 区分调用次数和整轮失败，提示查看全部批次；后台日志按私有权限准备。

Windows CI 已覆盖合成采集、报告、后台生命周期、安装拒绝和跨版本自更新；正式签名包另经 Windows PowerShell 5.1/7 验证。尚未进行 Windows 10/11 真实用户桌面、真实 Agent 识图或电池测试。首版采集主显示器；指定窗口需在前台可见，多显示器、混合 DPI、HDR/DRM 和 RDP 效果未验证。

Windows 的独立包签名不等于 Authenticode 或 SmartScreen 信誉；macOS 沿用固定自签名身份，尚无 Apple 公证。请按系统提示操作，无需关闭安全保护或导入根证书。

超时诊断不代表外部模型服务故障已解决。Hermes 的 `none` 工具集警告暂保留：当前已核验版本省略或传空会恢复默认工具，不能为消除警告而扩大分析进程的工具权限。

已有安装升级：`daylog update`。

通过 Agent 安装：

> 请按 https://github.com/cosin2077/daylog 的 README，为我安装 Daylog 和当前 Agent 的 skill，并验证安装成功。
