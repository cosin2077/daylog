#!/bin/sh
# Run only after editing the SVG. Normal builds use the checked-in PNG/ICNS.
set -eu
daylog_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
daylog_icon="$daylog_root/assets/icon"
command -v rsvg-convert >/dev/null 2>&1 || { echo 'Regenerating the icon requires rsvg-convert (librsvg).' >&2; exit 1; }
[ "$(uname -s)" = Darwin ] || { echo 'ICNS generation requires macOS iconutil.' >&2; exit 1; }
daylog_tmp=$(mktemp -d "${TMPDIR:-/tmp}/daylog-icon.XXXXXX")
trap 'rm -rf "$daylog_tmp"' EXIT HUP INT TERM
daylog_iconset="$daylog_tmp/Daylog.iconset"
mkdir -p "$daylog_iconset"
for daylog_size in 16 32 128 256 512; do
  rsvg-convert -w "$daylog_size" -h "$daylog_size" "$daylog_icon/daylog.svg" -o "$daylog_iconset/icon_${daylog_size}x${daylog_size}.png"
  daylog_retina=$((daylog_size * 2))
  rsvg-convert -w "$daylog_retina" -h "$daylog_retina" "$daylog_icon/daylog.svg" -o "$daylog_iconset/icon_${daylog_size}x${daylog_size}@2x.png"
done
/usr/bin/iconutil -c icns "$daylog_iconset" -o "$daylog_tmp/Daylog.icns"
cp "$daylog_iconset/icon_512x512@2x.png" "$daylog_icon/daylog.png"
cp "$daylog_tmp/Daylog.icns" "$daylog_icon/Daylog.icns"
echo 'Generated assets/icon/daylog.png and Daylog.icns'
