#!/bin/sh
set -eu
daylog_skill=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
daylog_apps="$HOME/Applications"
daylog_bin="$HOME/.local/bin"
daylog_data=""
while [ $# -gt 0 ]; do
  case "$1" in
    --apps-dir) daylog_apps=$2; shift 2 ;;
    --bin-dir) daylog_bin=$2; shift 2 ;;
    --data-dir) daylog_data=$2; shift 2 ;;
    *) echo 'Use --apps-dir DIR, --bin-dir DIR or --data-dir DIR.' >&2; exit 1 ;;
  esac
done
[ "$(uname -s)" = Darwin ] || { echo 'macOS 14+ required.' >&2; exit 1; }
if [ "$(/usr/sbin/sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then daylog_arch=arm64; else daylog_arch=amd64; fi
daylog_archive="$daylog_skill/assets/daylog-macos-$daylog_arch.tar.gz"
[ -f "$daylog_archive" ] || { echo "This skill has no $daylog_arch build; build it on that architecture first." >&2; exit 1; }
(cd "$daylog_skill/assets" && shasum -a 256 -c checksums.txt >/dev/null)
mkdir -p "$daylog_apps" "$daylog_bin"
daylog_target="$daylog_apps/Daylog.app"
daylog_exec="$daylog_target/Contents/MacOS/daylog"
daylog_cli() {
  if [ -n "$daylog_data" ]; then "$daylog_exec" --data "$daylog_data" "$@"; else "$daylog_exec" "$@"; fi
}
if [ -e "$daylog_bin/daylog" ] || [ -L "$daylog_bin/daylog" ]; then
  [ -L "$daylog_bin/daylog" ] && [ "$(readlink "$daylog_bin/daylog")" = "$daylog_exec" ] || {
    echo 'The command daylog belongs to another installation; nothing was overwritten.' >&2; exit 1;
  }
fi
if [ -e "$daylog_target" ]; then
  [ "$(/usr/bin/plutil -extract CFBundleIdentifier raw -o - "$daylog_target/Contents/Info.plist")" = io.daylog.agent ] || {
    echo 'Daylog.app belongs to another application; nothing was overwritten.' >&2; exit 1;
  }
  /usr/bin/codesign --verify --deep --strict "$daylog_target"
  daylog_cli update --source "$daylog_skill/assets/release.json" --remember >/dev/null
else
  daylog_temp=$(mktemp -d "${TMPDIR:-/tmp}/daylog-install.XXXXXX")
  trap 'rm -rf "$daylog_temp"' EXIT HUP INT TERM
  tar -xzf "$daylog_archive" -C "$daylog_temp"
  /usr/bin/codesign --verify --deep --strict "$daylog_temp/Daylog.app"
  /usr/bin/ditto "$daylog_temp/Daylog.app" "$daylog_target"
fi
if [ ! -L "$daylog_bin/daylog" ]; then ln -s "$daylog_exec" "$daylog_bin/daylog"; fi
daylog_cli init >/dev/null
daylog_cli update --source "$daylog_skill/assets/release.json" --remember --check >/dev/null
echo 'Daylog 已安装。运行 daylog start 开始记录。'
