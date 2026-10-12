#!/bin/sh
set -eu
daylog_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$daylog_root"
[ "$(uname -s)" = Darwin ] || { echo 'Native build requires macOS and Xcode Command Line Tools.' >&2; exit 1; }
daylog_version=${DAYLOG_VERSION:-0.1.0}
case "$daylog_version" in *[!0-9.]*|'') echo 'DAYLOG_VERSION must be numeric major.minor.patch.' >&2; exit 1;; esac
daylog_host=$(go env GOARCH)
daylog_arches=$daylog_host
if [ "${1:-}" = --all-arches ]; then daylog_arches='arm64 amd64'; fi
mkdir -p dist skills/daylog/assets
for daylog_arch in $daylog_arches; do
  case "$daylog_arch" in arm64) daylog_ccarch=arm64;; amd64) daylog_ccarch=x86_64;; *) exit 1;; esac
  daylog_app="$daylog_root/dist/$daylog_arch/Daylog.app"
  mkdir -p "$daylog_app/Contents/MacOS" "$daylog_app/Contents/Resources"
  GOARCH="$daylog_arch" CGO_ENABLED=1 CGO_CFLAGS="-arch $daylog_ccarch" CGO_LDFLAGS="-arch $daylog_ccarch" go build -trimpath -ldflags "-s -w -X main.version=$daylog_version" -o "$daylog_app/Contents/MacOS/daylog" ./cmd/daylog
  cat > "$daylog_app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>io.daylog.agent</string>
<key>CFBundleName</key><string>Daylog</string>
<key>CFBundleExecutable</key><string>daylog</string>
<key>CFBundleShortVersionString</key><string>$daylog_version</string>
<key>CFBundleVersion</key><string>$daylog_version</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSMinimumSystemVersion</key><string>14.0</string>
<key>LSUIElement</key><true/>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
  cp LICENSE "$daylog_app/Contents/Resources/LICENSE"
  /usr/bin/codesign --force --sign - "$daylog_app"
  /usr/bin/codesign --verify --deep --strict "$daylog_app"
  /usr/bin/plutil -lint "$daylog_app/Contents/Info.plist"
  COPYFILE_DISABLE=1 tar -czf "skills/daylog/assets/daylog-macos-$daylog_arch.tar.gz" -C "dist/$daylog_arch" Daylog.app
done
/usr/bin/ditto "dist/$daylog_host/Daylog.app" dist/Daylog.app
(cd skills/daylog/assets && shasum -a 256 daylog-macos-*.tar.gz > checksums.txt)
printf '{"version":"%s","artifacts":{' "$daylog_version" > skills/daylog/assets/release.json
daylog_separator=''
for daylog_arch in arm64 amd64; do
  daylog_file="daylog-macos-$daylog_arch.tar.gz"
  if [ -f "skills/daylog/assets/$daylog_file" ]; then
    daylog_hash=$(shasum -a 256 "skills/daylog/assets/$daylog_file" | awk '{print $1}')
    printf '%s"darwin-%s":{"url":"%s","sha256":"%s"}' "$daylog_separator" "$daylog_arch" "$daylog_file" "$daylog_hash" >> skills/daylog/assets/release.json
    daylog_separator=,
  fi
done
printf '}}\n' >> skills/daylog/assets/release.json
COPYFILE_DISABLE=1 tar -czf dist/daylog-skill-macos.tar.gz -C skills daylog
echo "Built version $daylog_version: dist/Daylog.app and dist/daylog-skill-macos.tar.gz"
