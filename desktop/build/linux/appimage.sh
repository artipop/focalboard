#!/usr/bin/env bash
# Package the Wails Linux binary (desktop/build/bin/Focalboard) into an AppImage.
# Run from the repo root, after `make linux-app-wails`.
#
# The AppImage bundles the (single, frontend-embedded) binary, a .desktop entry
# and an icon, but relies on the host providing libwebkit2gtk / libgtk-3 (as any
# Wails/WebKitGTK app does). Requires appimagetool on PATH, or it is downloaded
# to desktop/build/linux/.
set -euo pipefail

DESKTOP="$(cd "$(dirname "$0")/.." && pwd)"   # desktop/build
BIN="$DESKTOP/bin/Focalboard"
[ -x "$BIN" ] || { echo "missing $BIN — run 'make linux-app-wails' first" >&2; exit 1; }

APPDIR="$DESKTOP/bin/Focalboard.AppDir"
rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/bin"

cp "$BIN" "$APPDIR/usr/bin/focalboard"
cp "$DESKTOP/linux/focalboard.desktop" "$APPDIR/focalboard.desktop"
cp "$DESKTOP/appicon.png" "$APPDIR/focalboard.png"

cat > "$APPDIR/AppRun" <<'EOF'
#!/bin/sh
HERE="$(dirname "$(readlink -f "$0")")"
exec "$HERE/usr/bin/focalboard" "$@"
EOF
chmod +x "$APPDIR/AppRun"

APPIMAGETOOL="$(command -v appimagetool || true)"
if [ -z "$APPIMAGETOOL" ]; then
  APPIMAGETOOL="$DESKTOP/linux/appimagetool"
  if [ ! -x "$APPIMAGETOOL" ]; then
    echo "downloading appimagetool..."
    curl -sSL -o "$APPIMAGETOOL" \
      "https://github.com/AppImage/AppImageKit/releases/download/continuous/appimagetool-x86_64.AppImage"
    chmod +x "$APPIMAGETOOL"
  fi
fi

# APPIMAGE_EXTRACT_AND_RUN lets the appimagetool AppImage run without FUSE
# (GitHub runners have no libfuse2 by default).
APPIMAGE_EXTRACT_AND_RUN=1 ARCH=x86_64 "$APPIMAGETOOL" "$APPDIR" "$DESKTOP/bin/Focalboard-x86_64.AppImage"
rm -rf "$APPDIR"
echo "Built $DESKTOP/bin/Focalboard-x86_64.AppImage"
