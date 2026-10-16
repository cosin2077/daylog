# Read Daylog results (v0.4 with legacy support)

Use this reference when answering questions about recorded activity, time spent, or local files. The default root is `~/.daylog`; respect an explicitly selected `--data` root. Do not read the whole directory into model context.

## Read the smallest useful result

| User question | CLI |
| --- | --- |
| What did I do today / on a date? | `daylog report --period day --date YYYY-MM-DD --json` |
| Show original daily cards | `daylog today --date YYYY-MM-DD --json` |
| This week's / month's work | `daylog report --period week --json` / `--period month --json` |
| Visual report | `daylog report --no-open` |
| Recording status | `daylog status --json` |
| Analyze unprocessed images, when requested | `daylog pending --limit 1 --json` then follow `report-format.md` |

If not on PATH, use `~/.local/bin/daylog`. Reading reports does not start capture or invoke the configured analysis provider. The agent answering the user still processes the returned text through its own provider. Use `daylog report --from RFC3339 --to RFC3339 --json` for an arbitrary range up to 366 days; both timestamps must include an offset. Do not combine these flags with date/period. Without --json, it generates a range HTML report. Prefer period JSON because it clips cards and exposes draft/failed counts. Do not use raw `today` batch counts to claim every card belongs entirely inside the requested day.

`today --json` returns `{date, reports, pending_batches}`. A stored report has `batch_id`, `source`, `created_at`, `cards`; each card has `start`, `end`, `title`, `summary`, `category`, `evidence` (frame IDs). New reports have schema_version=2, revision=1, prompt_version, model (unknown for Agent CLI), input_hash, start/end, unknown_spans and unknown_seconds. Cards add task_id, activity_id, apps, actions, object, confidence, uncertainties, spans, estimated_seconds and duration_method. `spans[].interpolated` and period/activity `interpolated_seconds` mark bounded estimates between matching keyframes with continuous local samples. This is a subset of estimated_seconds, never add it again. Only spans are valid estimated time; a card's bounding start/end can contain gaps. The current revision is fixed; editing/revision workflows are not implemented. Legacy reports lack these fields.

`report --json` returns one period object, not the HTML's multi-view overview. Important fields: `start`, `end`, `activities`, `coverage_seconds`, `analyzed_batches`, `pending_batches`, `failed_batches`, `draft_frames`, `days`, `categories`. Activities include `batch_id` and `source`. `failed_batches` is a subset of pending, not an additional count. Category counts count fragments, not time. `estimated_seconds` is the union of v2 supported observation intervals; `legacy_coverage_seconds` is old model range coverage. `coverage_seconds` combines both, with overlap removed. `unknown_seconds` covers only gaps inside analyzed v2 batches, not all unrecorded time in the query. `tasks` groups linked task IDs and their estimated_seconds. None establishes precise work time, attention or productivity. Null/empty collections mean no existing results, not that the user did nothing.

For manual range calculations, clip v2 spans (legacy card ranges) and merge overlaps before summing; do not sum day/week/month views together. Pending and draft counts for a queried day are day-level indicators, not exact counts for a shorter requested interval. Timezone and offset matter; do not group by filename date alone.

## File map

| Path under root | Content / when useful |
| --- | --- |
| `reports/YYYYMMDD/<batch-id>.json` | Validated reports; read if CLI access is unavailable. Directory date follows UTC batch ID, not necessarily the user's local day. |
| `reports/report.html` | Most recently generated private static HTML snapshot; regenerated in place. Prefer JSON for questions. |
| `archive/YYYYMMDD/<batch-id>.json` | Processed batch metadata: actual bounds, frame IDs, timestamps, app IDs and file references. Images may already have expired. |
| `batches/<batch-id>.json` | Pending batches, frames and attempt/error metadata. Do not modify to force retries. |
| `draft.json` | Unsealed capture batch, if present; not an analyzed activity. |
| `frames/YYYY-MM-DD/*.jpg` | Original screenshots grouped by local capture date. Old flat paths can still exist. Use manifest paths or `pending`'s `image_paths`, not guessed filenames. |
| `observations/YYYY-MM-DD.jsonl` | Local sample and state events; repeated sampled observations reuse frame_id. Retained with journal retention. Not a substitute for interpreted activities. |
| `usage/YYYY-MM-DD.json` | Attempts, submitted_images (including retries), known API input/output tokens, known_usage_calls and unknown_usage_calls. Agent token counts are unknown, not zero. Not a complete provider bill. |
| `config.json` | User settings; can contain plaintext API keys. Not needed to answer activity questions. Read only relevant, redacted fields for configuration tasks. |
| `logs/llm/YYYY-MM-DD*.jsonl` | Private, bounded request/response diagnostics: payload, response, error_kind, timing, redacted stderr; no image Base64/auth headers. Default retention 7 days, cap 32 MiB. |
| `daemon.log`, `run.sock`, lock files | Diagnostics / runtime coordination; not activity evidence. |

If manually reading files, use RFC3339 timestamps to select intervals, including batches spanning midnight; do not assume the archive folder matches `frames`' local date. Never run text search over config or logs just to produce a work summary. Processed raw screenshots expire by file age (3 days by default, not 3 days after analysis); unselected/orphan frames can be removed earlier. Reports are retained for 90 days by default; pending/draft evidence is protected. A missing screenshot does not invalidate an existing summary, but prevents visual revalidation.

## Answer in human terms

Lead with the main tasks, then give a short timeline and approximate recorded coverage. Name visible progress; do not upgrade editing into completion or terminal text into a successful test. Explicitly distinguish analyzed records, pending/draft data, and uncovered time. Do not call every blank interval idle or rest. Use v2 task_id to aggregate linked activities. Task links are model judgments, not proof; old cards have no stable task identity. Prefer estimated_seconds and show legacy coverage separately. Do not sum card bounding ranges.

Example wording, not real user data: “这段时间主要在修改报告界面，并查阅相关文档；已整理片段覆盖约 40 分钟，另有 2 批尚未分析，暂时无法完整估计总耗时。”

Only give behavioral suggestions when asked, at most two, with supporting observations. Do not infer distraction, health, mood, or ability from app switches. A normal summary request is satisfied by existing JSON; do not resubmit the screenshot library or start recording. For requested new image analysis, use the skill's pending/commit flow and the current report schema.

For v0.4.1, batches/reports record app_source=window_server. Older v2 records may have cached app IDs; the overview exposes legacy_app_metadata and warns about application/time reliability. Do not silently rewrite historical app labels. Retry metadata includes failure_rounds and retry_after; attempts is a lifetime count, not a permanent three-attempt gate.

Period activity entries include clipped `spans` and available `apps`, and period reports include merged `unknown_spans`. Use these exact intervals for timelines instead of spreading the activity bounding start/end across gaps. HTML additionally embeds day views for date navigation inside the snapshot; it does not fetch new data or call a model when changing dates.

## Time fragments and continuity

New analysis reports add `task_observations`: local `at`, reliable `app`, identified `task_id` (empty means a boundary) and `max_gap_seconds`. This is derived from existing observations, not extra capture. Historical reports without it are not reanalyzed. Broad historical categories retain `legacy_category` in period activities; 工作/学习/其他 become 未细分 in the view, not guessed specific activities. Activity `uncertainties` remain visible.

Period JSON adds `continuity`. Start with `fragments` when the user asks where little pieces of time went: show the timestamp, `from_title`, `title`, `next_title`, and whether `returned` is true. `start`/`end` are first/last observations of the inserted task; `next_at` is the first observation of the following task, not its exact start. A fragment requires another identified task on both sides, with the entire window from the prior task's last observation to the following task's first observation <=5 minutes. Single-frame visits are retained with zero estimated_seconds; say duration could not be estimated. The 5-minute threshold is a fixed observation rule, not a scientific focus threshold or a configurable setting yet.

`fragment_destinations` groups these visits by linked task ID and exposes count/estimated_seconds. These and `fragment_seconds` are subsets of existing activity estimates; do not add them to total time. `task_switches` differs from `app_switches`: switching software for the same task is not a task switch. `brief_returns` counts A-B-A patterns within that same five-minute window. `returns` shows at most the last 20 such events; `fragments` contains all qualifying short visits in the period, including A-B-C. `comparable_pairs=0` means insufficient evidence, not zero distractions. `missing_observation_batches` records batches excluded from switch statistics.

`longest_seconds`/`longest_span` merge only touching, nonconflicting same-task spans within a local day; gaps, low-confidence evidence and legacy unreliable app metadata do not become focus time. `runs`/`short_runs` describe evidence runs (shorter than 5 minutes), not a measured fragmentation rate. Task associations are model judgments; sparse sampling can miss brief switches. Never turn these metrics into a psychological diagnosis, moral judgment or fixed context-recovery cost. Optional goals and persisted user corrections are not implemented. Use concrete examples of observed interruptions when asked about distraction, and qualify the interpretation.

## Recap and comparison (0.8.0)

Period JSON adds deterministic `review.headline` / `advice`, `comparison`, `interrupted_batches` and `continuity.fragment_sources`. A comparison reports previous `start` / `end`, `current_end`, `partial` and `available`, plus the previous period metrics. Incomplete periods align by local calendar days and clock time, not elapsed UTC seconds; a shorter prior month is capped at month end. `available:false` is missing history, not zero activity. Duration comparisons also require positive estimated durations, and switch comparisons require comparable observation pairs on both sides. Coverage and recorded-day differences can affect every delta.

Fragment sources group actual application metadata for each short visit; counts sum to the number of visits. A multi-application visit remains one combined source, and a missing application is explicitly unknown. Do not infer family, group identity or intent from an app. `interrupted_batches` is a pending subset, distinct from `failed_batches`; older canceled attempts count as interrupted.

`today --json` retains raw `reports` and adds `activities` / `activity_count` using the same date-clipped activity list as `report --json`. Point observations at the beginning of a day are included, ones at the next day boundary are excluded, and no duration is invented. Cross-midnight cards appear once per local day; counts across a week can therefore exceed raw card count.
