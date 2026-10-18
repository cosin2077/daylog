# Agent report format

Read `batch.schema_version` from `daylog pending --json`. New batches use v2; older batches without that field still use the legacy format below. Never submit a stored report back as model output.

## v2: interpretation only

Use actual batch/frame IDs and optional task IDs from `batch.context`. Return this shape, without timestamps or durations:

```json
{
  "schema_version": 2,
  "batch_id": "actual-batch-id",
  "segments": [{
    "title": "调整报告布局",
    "summary": "可见正在编辑布局并查看预览；无法确认完成。",
    "category": "写代码",
    "object": "报告页面",
    "actions": ["编辑", "预览"],
    "confidence": "medium",
    "task_id": "",
    "evidence": ["actual-frame-id"],
    "uncertainties": []
  }]
}
```

Up to 12 segments. Each needs unique known screenshot evidence; a frame can belong to only one segment. Omit unidentifiable frames, or return `segments: []` if none can be identified. Confidence is high/medium/low; category is 写代码/写东西/查资料/聊天沟通/看内容/玩游戏/处理杂事/未细分. Link only a task from the supplied context with continuity evidence; leave task_id empty for a new task. Low confidence cannot reuse a task ID. Titles <=120 characters, summaries <=1200, object <=240; actions/uncertainties <=8 items of <=240 characters. Only title, summary and evidence are required per segment. Optional arrays default to empty; omitted category defaults to 未细分, confidence to medium. Schema version and batch ID may be omitted for automatic analysis because the invocation supplies them; manual `commit --file` still needs batch_id for routing. Plain JSON and JSON/unlabelled Markdown code fences are accepted. Extra fields are ignored and cannot set timestamps or durations. Unknown frame IDs, missing evidence, conflicting batch IDs and cross-segment evidence reuse still fail.

Keep brief unrelated chat, feeds or games as separate tasks even if only one frame shows them; do not absorb them into a broad work segment. Classify visible content, not app names. A browser may show coding, research or feeds; WeChat is not evidence of entertainment by itself. Reuse a supplied task_id when concrete evidence shows a return to that same task.

The CLI computes time from local observations, preserves unknown intervals and assigns IDs. Never fill gaps or infer completed work. Commit through `daylog commit --file ...`, then reread the report. Repeating a commit refuses overwrite. Use `daylog prompt` for current v2 instructions.

## Legacy batches

Use actual values from `daylog pending --json`; the following is a schema sketch, not sample data to commit:

```json
{
  "batch_id": "actual-batch-id",
  "cards": [
    {
      "start": "actual-RFC3339-time",
      "end": "actual-RFC3339-time",
      "title": "简短的可见活动",
      "summary": "可见事实；有必要时明确不确定性。",
      "category": "工作",
      "evidence": ["actual-screenshot-id"]
    }
  ]
}
```

One to eight ordered cards, with no overlaps. Each interval must stay within the batch's start/end; gaps are allowed. Each card needs at least one screenshot ID from that batch. Titles are limited to 120 Unicode characters, summaries to 1,200 and categories to 60. Do not supply `source` or `created_at`; the CLI owns them. A batch with a report cannot be overwritten.

Group related IDE, terminal and browser activity by the immediate task goal. Screenshot sampling cannot establish precise time spent or successful completion; use conservative summaries. If the batch contains only one frame, one short card is acceptable. Do not stretch it to a long duration to fill a gap.

Use a private temporary file (0600), commit with the CLI, delete the temporary file, and verify with `today --json`. Reports and screenshot data remain sensitive even without an API key.

同一目标的不同动作（编码、查文档、相关沟通）分别输出活动片段与 category；相同动作的相似截图可合并，不要求逐图拆分。若这些片段有明确共同的新目标，可添加相同 `task_key`（ASCII 字母/数字/下划线/短横线，1–40位）与相同 `task_title`（1–120字符）。不与 `task_id` 同填，低置信片段不填关联；已有目标使用请求 context 中的 task_id。程序生成稳定于本批的任务 ID，类别分布仍统计片段动作。
