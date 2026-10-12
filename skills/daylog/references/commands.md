# CLI operations

Run `daylog help` for exact commands. `--data` selects a private alternative data directory; the default is `~/Library/Application Support/Daylog`. The directory must have mode 0700. All stored files are local plain files with mode 0600, not application-encrypted.

## Configuration

`daylog config` prints the current config. To edit, save that full JSON to a private temporary file, modify the necessary fields, then `daylog config --file <file>` and remove the temporary file. Restart a running service to apply changes. Do not include an API key; unknown fields are rejected.

- Default foreground/power polling: AC 30 seconds, battery 120, low-power mode 180.
- Actual capture is also bounded by `min_save_seconds` (default 90); high-frequency polling does not require screenshot encoding at every tick.
- JPEG height defaults to 720 and width is capped at 1920; quality 60. No cursor.
- `window_id: 0` captures the primary display. `daylog windows` lists window IDs and titles; titles may be sensitive. A selected window closing causes capture to pause, never fall back to full screen. Window IDs may change after an app restarts.
- `blocked_apps` contains bundle IDs, for example `com.agilebits.onepassword7`. Prefer the actual IDs on the machine. The capture excludes these apps, and a blocked foreground app suppresses capture entirely. Website-specific redaction is not implemented.
- Hardware idle >300 seconds suppresses capture. Reading without keyboard/mouse input may also count as idle; set `idle_after_seconds: 0` only when the user prefers completeness over that energy saving.
- Below 20% battery, lock, display sleep or serious thermal pressure: pause capture. No sleep-prevention assertions or power-settings changes.
- Batches default to 15 minutes and at most six selected screenshots. Raw processed images retain three days; pending evidence is protected. Raw cap 256 MiB, pending cap 96, text reports 90 days. At a limit, capture pauses rather than deleting pending evidence.

## API analysis

`agent.enabled` defaults true. `daylog agents` reports installation and adapter availability, never authentication success merely because a CLI exists. `agent.order` defaults to `claude`, `codex`, `hermes`; `agent.timeout_seconds` is 90. Disable agent analysis for capture-only use. Daylog searches PATH plus standard user/Homebrew bin directories so LaunchAgents find common installs. Unsupported flags, missing login, unavailable vision and invalid output fail the invocation, then eligible fallback is tried within the shared budget. Grok/Antigravity currently have no verified image adapter.

Codex receives image attachments and a JSON Schema in read-only, ephemeral mode with shell/desktop/browser features disabled. Claude uses stdin image messages with builtin tools disabled and no session persistence. Hermes accepts one image, so a contact sheet ordered by position is created; small text may lose detail, and its session persistence remains governed by Hermes. Prompts and copied images are private temporary files removed after the child exits. No permission-bypass flags are used. Child groups are killed on timeout/cancellation. Existing provider configuration and auth are retained.

Supports a full OpenAI-compatible Chat Completions endpoint with image input. This does not require an OpenAI-owned service. Other protocols can be used through the active agent's vision tools and `commit`.

Set `ai.endpoint`, `ai.model` and `ai.enabled: true` only after the destination is selected. HTTPS is required except localhost/loopback for local models. Put the key in the system Keychain with `daylog auth set --stdin`; use an already-authorized credential source to pipe it without printing it. `DAYLOG_API_KEY` is supported for a foreground run, but LaunchAgents do not automatically inherit terminal environment variables.

The direct API request has a 60-second timeout and 1,800 output-token limit. Invalid or truncated JSON is rejected. No HTTP retry occurs inside one API attempt; each batch has three attempts and a 15-minute retry cooldown. Immediate local-Agent fallback may use the remaining attempts; all invocations count toward the daily budget (`ai.max_requests_per_day`, 48). Agent-internal network retries are outside Daylog's accounting. Redirects are refused for screenshot API calls. Missing evidence, budget exhaustion and failed batches remain visible via `pending`.

By default automatic analysis waits for AC power. `analyze --batch ID --allow-battery` is an explicit one-off override; do not use it silently.

## Background service

`service install` writes a scoped LaunchAgent but does not immediately load or run it. `service start` loads and starts it; `service stop` unloads it. `stop` stops a currently running process without deleting history. Add `service install --login` only for requested login startup. The service uses Background process type, nice +10 and low-priority I/O; screenshot work also uses Utility QoS. No keep-alive restart loop.

`service uninstall` removes only the service. Removing the app or skill also leaves data and Keychain credentials. Use `auth delete` only when credential removal is requested. There is intentionally no implicit full-history deletion operation.

## Updates

`daylog update --check` reports the current/available version for the running binary's platform; `daylog update` installs a newer one. The installer remembers the skill's local `assets/release.json`. Switch to a trusted HTTPS manifest with `update --source URL --remember --check`. Local manifests may reference sibling tar.gz filenames; HTTPS manifests may use absolute or relative package URLs. Schema: `{"version":"0.1.0","artifacts":{"darwin-arm64":{"url":"daylog-macos-arm64.tar.gz","sha256":"64 lowercase hex characters"}}}`; include `darwin-amd64` for Intel.

Packages are limited to 32 MiB compressed / 64 MiB expanded. Traversal, symlinks, hardlinks and special files are rejected. Code signature, bundle ID `io.daylog.agent`, version and native executable architecture are verified before stopping recording. This checks package integrity, not Developer ID notarization or a guarantee that a compromised feed is trustworthy. No scheduled update is installed. Data and credentials are retained. `--force` explicitly reinstalls the current version and is useful for isolated verification.
