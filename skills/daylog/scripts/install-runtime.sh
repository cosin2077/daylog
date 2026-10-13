#!/bin/sh
# Public installs use macOS system tools; no Go, Node or Python needed.
set -eu
daylog_apps="$HOME/Applications"
daylog_bin="$HOME/.local/bin"
daylog_data=""
daylog_source='https://github.com/cosin2077/daylog/releases/latest/download/release.json'
while [ $# -gt 0 ]; do
  [ $# -ge 2 ] || { echo 'Options require a value.' >&2; exit 1; }
  case "$1" in
    --apps-dir) daylog_apps=$2 ;;
    --bin-dir) daylog_bin=$2 ;;
    --data-dir) daylog_data=$2 ;;
    --source) daylog_source=$2 ;;
    *) echo 'Use --apps-dir DIR, --bin-dir DIR, --data-dir DIR or --source HTTPS-URL/FILE.' >&2; exit 1 ;;
  esac
  shift 2
done
[ "$(uname -s)" = Darwin ] || { echo 'macOS 14+ required.' >&2; exit 1; }
daylog_major=$(/usr/bin/sw_vers -productVersion | /usr/bin/cut -d. -f1)
[ "$daylog_major" -ge 14 ] || { echo 'macOS 14+ required.' >&2; exit 1; }
if [ "$(/usr/sbin/sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then daylog_arch=arm64; daylog_macho=arm64; else daylog_arch=amd64; daylog_macho=x86_64; fi
daylog_temp=$(mktemp -d "${TMPDIR:-/tmp}/daylog-install.XXXXXX")
daylog_stage=''
daylog_lock=''
trap 'rm -rf "$daylog_temp"; if [ -n "$daylog_stage" ]; then rm -rf "$daylog_stage"; fi; if [ -n "$daylog_lock" ]; then rmdir "$daylog_lock"; fi' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
daylog_fetch() {
  case "$1" in
    https://*) curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-redirs 5 --connect-timeout 15 --max-time 120 --max-filesize "$3" --retry 2 --output "$2" -- "$1" || return 1 ;;
    http://*)
      printf '%s\n' "$1" | /usr/bin/grep -Eq '^http://(127\.0\.0\.1|localhost):[0-9]+/' || { echo 'HTTP is allowed only for local verification.' >&2; return 1; }
      curl --fail --silent --show-error --proto '=http' --max-redirs 0 --connect-timeout 5 --max-time 30 --max-filesize "$3" --output "$2" -- "$1" || return 1 ;;
    *://*) echo 'Downloads require HTTPS.' >&2; return 1 ;;
    *) cp "$1" "$2" || return 1 ;;
  esac
  [ "$(/usr/bin/stat -f %z "$2")" -le "$3" ] || { echo 'Download exceeds size limit.' >&2; return 1; }
}
case "$daylog_source" in
  https://*) ;;
  http://*) printf '%s\n' "$daylog_source" | /usr/bin/grep -Eq '^http://(127\.0\.0\.1|localhost):[0-9]+/' || { echo 'HTTP is allowed only for local verification.' >&2; exit 1; } ;;
  *://*) echo 'The manifest requires HTTPS or a local file.' >&2; exit 1 ;;
  *) daylog_source="$(CDPATH= cd -- "$(dirname -- "$daylog_source")" && pwd)/$(basename -- "$daylog_source")" ;;
esac
daylog_manifest="$daylog_temp/release.json"
daylog_fetch "$daylog_source" "$daylog_manifest" 1048576 || {
  echo '无法获取 Daylog Release。请确认已有正式发布的 Release（草稿/预发布不会成为 latest）；无需在用户电脑上编译。' >&2
  exit 1
}
daylog_version=$(/usr/bin/plutil -extract version raw -o - "$daylog_manifest")
printf '%s\n' "$daylog_version" | /usr/bin/grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo 'Invalid release version.' >&2; exit 1; }
daylog_url=$(/usr/bin/plutil -extract "artifacts.darwin-$daylog_arch.url" raw -o - "$daylog_manifest")
daylog_hash=$(/usr/bin/plutil -extract "artifacts.darwin-$daylog_arch.sha256" raw -o - "$daylog_manifest")
printf '%s\n' "$daylog_hash" | /usr/bin/grep -Eq '^[a-f0-9]{64}$' || { echo 'Invalid release checksum.' >&2; exit 1; }
case "$daylog_url" in
  https://*|http://*) ;;
  "daylog-macos-$daylog_arch.tar.gz") daylog_url="${daylog_source%/*}/$daylog_url" ;;
  *) echo 'Invalid release artifact URL.' >&2; exit 1 ;;
esac
# Resolve relative artifacts before passing a temporary manifest to the updater.
/usr/bin/plutil -replace "artifacts.darwin-$daylog_arch.url" -string "$daylog_url" "$daylog_manifest"
/usr/bin/plutil -convert json "$daylog_manifest"
mkdir -p "$daylog_apps" "$daylog_bin"
daylog_apps=$(CDPATH= cd -- "$daylog_apps" && pwd)
daylog_bin=$(CDPATH= cd -- "$daylog_bin" && pwd)
mkdir "$daylog_apps/.daylog-install.lock" 2>/dev/null || { echo 'Another install may be running: .daylog-install.lock exists.' >&2; exit 1; }
daylog_lock="$daylog_apps/.daylog-install.lock"
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
if [ -e "$daylog_target" ] || [ -L "$daylog_target" ]; then
  [ ! -L "$daylog_target" ] && [ "$(/usr/bin/plutil -extract CFBundleIdentifier raw -o - "$daylog_target/Contents/Info.plist")" = io.daylog.agent ] || {
    echo 'Daylog.app belongs to another application; nothing was overwritten.' >&2; exit 1;
  }
  /usr/bin/codesign --verify --deep --strict "$daylog_target"
  case "$daylog_source" in https://*|http://*) daylog_cli update --source "$daylog_manifest" >/dev/null ;; *) daylog_cli update --source "$daylog_source" >/dev/null ;; esac
else
  daylog_archive="$daylog_temp/app.tar.gz"
  daylog_fetch "$daylog_url" "$daylog_archive" 33554432
  daylog_actual=$(shasum -a 256 "$daylog_archive" | /usr/bin/awk '{print $1}')
  [ "$daylog_actual" = "$daylog_hash" ] || { echo 'Release checksum mismatch; nothing installed.' >&2; exit 1; }
  # Reject traversal, links, special entries and excessive expanded size first.
  /usr/bin/tar -tzf "$daylog_archive" > "$daylog_temp/paths"
  /usr/bin/awk 'BEGIN {bad=0} /\\|(^|\/)\.\.?(\/|$)/ {bad=1} !/^Daylog[.]app(\/|$)/ {bad=1} END {exit (bad || NR>1024 || NR==0)}' "$daylog_temp/paths" || { echo 'Unsafe archive paths.' >&2; exit 1; }
  /usr/bin/tar -tvzf "$daylog_archive" > "$daylog_temp/entries"
  /usr/bin/awk 'substr($1,1,1)!="-" && substr($1,1,1)!="d" {bad=1} $5 !~ /^[0-9]+$/ {bad=1} {size+=$5} END {exit (bad || size>67108864)}' "$daylog_temp/entries" || { echo 'Unsafe archive entries or size.' >&2; exit 1; }
  daylog_stage=$(mktemp -d "$daylog_apps/.daylog-install.XXXXXX")
  /usr/bin/tar -xzf "$daylog_archive" -C "$daylog_stage"
  daylog_app="$daylog_stage/Daylog.app"
  /usr/bin/codesign --verify --deep --strict "$daylog_app"
  [ "$(/usr/bin/plutil -extract CFBundleIdentifier raw -o - "$daylog_app/Contents/Info.plist")" = io.daylog.agent ] || { echo 'App identity mismatch.' >&2; exit 1; }
  [ "$(/usr/bin/plutil -extract CFBundleShortVersionString raw -o - "$daylog_app/Contents/Info.plist")" = "$daylog_version" ] || { echo 'App version mismatch.' >&2; exit 1; }
  /usr/bin/lipo "$daylog_app/Contents/MacOS/daylog" -verify_arch "$daylog_macho"
  mv "$daylog_app" "$daylog_target"
fi
if [ ! -L "$daylog_bin/daylog" ]; then ln -s "$daylog_exec" "$daylog_bin/daylog"; fi
daylog_cli init >/dev/null
daylog_cli update --source "$daylog_source" --remember --check >/dev/null
echo 'Daylog 已安装。运行 daylog start 开始记录。'
