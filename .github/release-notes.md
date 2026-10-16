本版新增 Windows 10 1809+ / Windows 11 x64 支持，复用现有分析、时间碎片和日／周／月报告，macOS 保留原有使用方式。

- Windows 原生低频截图、前台应用识别、空闲／锁屏／显示器及电池状态检测，沿用采样、去重和省电策略。
- 当前用户后台和私有控制管道；默认不开机自启，`start --login` 显式启用，停止时移除。
- PowerShell 5.1/7 安装器、用户私有数据权限和 Credential Manager 凭据存储，无需安装 Go、Python 或 Node 来运行 Daylog。
- Windows Agent 进程通过 Job Object 管理，取消时退出整个子进程树；支持原生 CLI 和已适配的 Claude/Codex npm 入口。
- Windows 包验证固定发布者 RSA 签名、版本、架构和摘要，支持运行中自更新与下次命令清理已完成升级的备份。

Windows CI 已覆盖合成采集、报告、后台生命周期、安装拒绝和跨版本自更新；正式签名包另经 Windows PowerShell 5.1/7 验证。尚未进行 Windows 10/11 真实用户桌面、真实 Agent 识图或电池测试。首版采集主显示器；指定窗口需在前台可见，多显示器、混合 DPI、HDR/DRM 和 RDP 效果未验证。

Windows 的独立包签名不等于 Authenticode 或 SmartScreen 信誉；macOS 沿用固定自签名身份，尚无 Apple 公证。请按系统提示操作，无需关闭安全保护或导入根证书。

已有安装升级：`daylog update`。

通过 Agent 安装：

> 请按 https://github.com/cosin2077/daylog 的 README，为我安装 Daylog 和当前 Agent 的 skill，并验证安装成功。
