# Agent report format

Use actual values from `daylog pending`; the following is a schema sketch, not sample data to commit:

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
