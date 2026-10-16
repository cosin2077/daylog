报告现在先回答“主要做了什么”，再提供需要展开的记录细节。日报、周报和月报有各自的节奏视图；CLI 更简短，取消、失败和缺少记录的提示保持一致。

- 日报首屏：一句话结论、三个关键数字、分类色条；全宽任务 Top 5 和当天时间轴前置。长标题完整换行，手机端紧凑单栏。
- 短时切换按真实应用来源聚合 Top 3，其他来源、重复明细和估时方法默认折叠。连续观察称为“最长连续一段”，不等同于深度专注。
- 周报增加每天趋势，月报增加日历热力图；关键数字与上期比较，未结束周期按本地日历进度对齐，无可比记录不显示变化百分比。离线 HTML 仍需重新生成才会更新。
- today 和报告统一日期裁剪与活动计数，保留零时长单帧、不虚构估时。today --json 新增 activities/activity_count，原始 reports 保留；长方法说明移至 --explain。
- status 默认六行，--verbose 查看详情；队列实时读取，旧取消记录也归为“已中断”，不误计失败。pending 默认最多十段，编号与调用计数保留在 JSON。
- 每个命令有自己的 -h / help COMMAND；支持 today yesterday、report week、report month；未知选项保留原横杠，不回显值。
- 暂停仅接受 1–1440 分钟，同步确认暂停/恢复；缺失的整理编号在环境检查前明确报错。空日期提示最近有记录的日期，保留已有 HTML。
- doctor 对齐中文列，展示模型安装与最近整理结果，提供下一步；不额外截图或调用模型，也不将历史成功当作当前连通。
- 配置分常用/高级；**config --json 现在默认打码密钥**，确需完整导出用 --json --show-secrets，打码配置导回本机保留现有密钥。

完整取舍见 [UX 评审处理](https://github.com/cosin2077/daylog/blob/main/docs/ux-review-v3.md)。报告和统计复用已有记录，不增加截图或模型调用。

沿用 macOS arm64 / Intel 与 Windows x64 支持、固定发布身份验证和运行中自更新。macOS 使用固定自签名身份，尚无 Apple 公证；Windows 的包签名不等于 Authenticode 或 SmartScreen 信誉。Windows CI 使用合成图片，不代表 Windows 10/11 用户桌面、多屏、HDR/DRM、RDP、电池或真实模型识图实测。

已有安装升级：`daylog update`。

通过 Agent 安装：

> 请按 https://github.com/cosin2077/daylog 的 README，为我安装 Daylog 和当前 Agent 的 skill，并验证安装成功。
