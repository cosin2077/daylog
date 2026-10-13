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
    "category": "工作",
    "object": "报告页面",
    "actions": ["编辑", "预览"],
    "confidence": "medium",
    "task_id": "",
    "evidence": ["actual-frame-id"],
    "uncertainties": []
  }]
}
```

Up to 12 segments. Each needs unique known screenshot evidence; a frame can belong to only one segment. Omit unidentifiable frames, or return `segments: []` if none can be identified. Confidence is high/medium/low; category is 工作/学习/沟通/其他. Link only a task from the supplied context with continuity evidence; leave task_id empty for a new task. Low confidence cannot reuse a task ID. Titles <=120 characters, summaries <=1200, object <=240; actions/uncertainties <=8 items of <=240 characters. Both arrays are required, but may be empty.

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
