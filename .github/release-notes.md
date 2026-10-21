修复 Notion 同步确认后缺少进行提示的问题。

- `daylog notion sync` 确认后显示「正在同步到 Notion」加载动画，覆盖凭据读取、本地记录整理和网络写入；完成、失败或取消后清理提示。
- `daylog notion check` 同样显示连接检查提示；非交互输出静态文字，`--json` 保持纯 JSON，无动画或额外进度输出。
- 沿用现有单日单页同步、日期确认、补传和人工备注保护。

已有安装运行 `daylog update`。
