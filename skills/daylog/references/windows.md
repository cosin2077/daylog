# Windows runtime

Target: Windows 10 1809+ / Windows 11 x64, current user desktop. Windows ARM64 and Windows Server/service-session screenshot behavior are not supported claims. Release artifacts must include `windows-amd64`; source support does not mean the current latest Release already contains it.

## Installation and normal use

Run `scripts/install-runtime.ps1` with Windows PowerShell 5.1 or PowerShell 7. It uses OS-provided .NET APIs, downloads the matching ZIP and checks both package hash and the fixed publisher's RSA signature. Runtime use requires no Go, Python or Node. An existing npm-installed analysis agent still requires its own Node runtime. Respect system script policies and SmartScreen prompts; never silently weaken them.

Executable: `%LOCALAPPDATA%\Daylog\bin\daylog.exe`. Data: `%USERPROFILE%\.daylog`. Use the full executable path until a new terminal inherits the user PATH. Installation does not start capture. `daylog start`, `stop`, `today`, `report`, `config` and `update` have the same meaning on both platforms. Help includes the build version.

The background process belongs to the current user session, not Session 0 or an administrator service. `start --login` explicitly opts into the current user's `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` entry; ordinary start does not enable login startup. `stop` removes its registration and leaves records intact. `service.json` stores the executable/data paths and login preference.

## Capture and resource policy

- Primary-display capture uses GDI `CreateDIBSection` / `StretchBlt`, downsizing in native memory before JPEG conversion. No continuous video stream, OCR loop or wake lock. Reuse existing configurable sampling, deduplication and battery/idle policies.
- Selected-window mode captures only the visible foreground window's client area. It does not promise occluded/minimized window capture. Foreground changes during capture discard the image; configured blocked apps are matched case-insensitively by executable basename, e.g. `keepass.exe`.
- Foreground attribution uses the owner process of `GetForegroundWindow`. Lock/safe-desktop/disconnected-session detection checks the interactive desktop and WTS session state; it never switches or opens the secure desktop for capture.
- Battery/AC and battery-saver status use `GetSystemPowerStatus`; idle uses `GetLastInputInfo`. A sleeping message thread receives display-power notifications. Native Windows thermal pressure is not reported reliably here (`thermal=-1`), so do not describe it as measured normal temperature.
- Windows CI can validate synthetic capture, process behavior and blocked session behavior. It does not validate a user's multi-monitor, mixed-DPI, HDR/DRM, GPU, RDP, battery, or foreground app behavior. Dark/protected content may not be captured meaningfully by GDI. Consider Windows Graphics Capture only after testing the workload and overhead.

## Privacy and agents

The data-root DACL allows the current user and SYSTEM and propagates to new files. Reparse-point roots are rejected. Control uses a current-user-only named pipe with remote clients rejected, never a public TCP listener. Administrators can still take ownership; this is not encryption against a local administrator.

Optional secure API credentials use Windows Credential Manager (`Daylog/AI`); plaintext config remains supported when selected. Do not inspect or export credential contents when checking readiness.

Windows inherits the existing process environment and agent config files, without launching Unix login profiles. Native `.exe` agents are supported; official Claude/Codex npm `.cmd` shims resolve to their known Node entry points without shell interpolation. Hermes requires its native executable entry point. Cancellation uses a Job Object, assigned before resuming the initial process thread, to terminate the CLI and descendants together. Actual Windows provider login/vision compatibility requires a successful real invocation.

Repeated analysis failure submits a fixed-content Windows notification using a short-lived tray balloon; it contains no screenshot content or credentials, adds no service and requests no administrator privilege. Focus/notification settings can suppress display. Persisted status and report failures remain authoritative.

## Updates and release verification

Windows ZIP entries are restricted to `daylog.exe`, `signature.json`, `LICENSE` and `THIRD_PARTY_NOTICES.txt`, with size limits and no path traversal/symlinks. `signature.json` binds executable SHA-256, exact version and `windows-amd64` using RSA PKCS#1 v1.5 / SHA-256 and the existing pinned Daylog publisher certificate. A remote manifest cannot replace that certificate. The ZIP hash alone is insufficient.

The verified candidate is checked for PE architecture and runtime build version before recording stops. Replacement uses same-directory staging and restores the old executable if the second rename fails. A running mapped image may leave `previous.exe` in a `.daylog-update-*` directory until it exits. A crash between renames or a new version that fails to start is not automatic crash rollback. Never claim that signer validation provides Authenticode signing or SmartScreen reputation.

`scripts/build-windows.py` builds the x64 executable and ZIP. Production builds require `DAYLOG_RELEASE_KEY`, supplied only within the task-owned temporary signing environment. Development builds must explicitly set `DAYLOG_DEVELOPMENT_BUILD=1` and are refused by production installers/updaters. `DAYLOG_DEVELOPMENT_INSTALL=1` is restricted to local test feeds; do not enable it for public installation.

Windows CI runs Go tests, process-tree/pipe/DACL checks, installer rejection checks, synthetic recording, report rendering, background lifecycle and mapped-executable self-update. Release verification additionally tests the fixed-publisher signed ZIP on Windows using PowerShell 5.1/7. Report these separately from unperformed user-desktop tests.

## API references

- [Microsoft: capturing an image](https://learn.microsoft.com/en-us/windows/win32/gdi/capturing-an-image)
- [Microsoft: OpenInputDesktop](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-openinputdesktop)
- [Microsoft go-winio](https://github.com/microsoft/go-winio)
- [Go Windows system interfaces](https://pkg.go.dev/golang.org/x/sys/windows)
