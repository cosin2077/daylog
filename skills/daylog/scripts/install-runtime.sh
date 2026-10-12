#!/bin/sh
set -eu
daylog_skill=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
daylog_apps="$HOME/Applications"
daylog_bin="$HOME/.local/bin"
daylog_data="$HOME/Library/Application Support/Daylog"
while [ $# -gt 0 ]; do
  case "$1" in
    --apps-dir) daylog_apps=$2; shift 2 ;;
    --bin-dir) daylog_bin=$2; shift 2 ;;
    --data-dir) daylog_data=$2; shift 2 ;;
    *) echo 'Use --apps-dir DIR and/or --bin-dir DIR.' >&2; exit 1 ;;
  esac
done
[ "$(uname -s)" = Darwin ] || { echo 'macOS 14+ required.' >&2; exit 1; }
if [ "$(/usr/sbin/sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then daylog_arch=arm64; else daylog_arch=amd64; fi
daylog_archive="$daylog_skill/assets/daylog-macos-$daylog_arch.tar.gz"
[ -f "$daylog_archive" ] || { echo "This skill has no $daylog_arch build; build it on that architecture first." >&2; exit 1; }
(cd "$daylog_skill/assets" && shasum -a 256 -c checksums.txt)
mkdir -p "$daylog_apps" "$daylog_bin"
daylog_target="$daylog_apps/Daylog.app"
daylog_exec="$daylog_target/Contents/MacOS/daylog"
if [ -e "$daylog_target" ]; then
  echo 'Daylog.app already exists. Refusing to overwrite an existing installation.' >&2
  exit 1
fi
if [ -e "$daylog_bin/daylog" ] || [ -L "$daylog_bin/daylog" ]; then
  echo 'The command daylog already exists. Refusing to overwrite it.' >&2
  exit 1
fi
daylog_temp=$(mktemp -d "${TMPDIR:-/tmp}/daylog-install.XXXXXX")
trap 'rm -rf "$daylog_temp"' EXIT HUP INT TERM
tar -xzf "$daylog_archive" -C "$daylog_temp"
/usr/bin/codesign --verify --deep --strict "$daylog_temp/Daylog.app"
/usr/bin/ditto "$daylog_temp/Daylog.app" "$daylog_target"
ln -s "$daylog_exec" "$daylog_bin/daylog"
"$daylog_exec" version
"$daylog_exec" --data "$daylog_data" init
"$daylog_exec" --data "$daylog_data" update --source "$daylog_skill/assets/release.json" --remember --check
echo "Installed: $daylog_exec"
echo 'No screen recording or AI request has been started.'
