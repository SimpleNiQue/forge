#!/bin/bash
set -e

REPO="SimpleNiQue/forge"
BINARY="forge"
INSTALL_DIR="/usr/local/bin"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64)          ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *)
    echo "❌ Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

case "$OS" in
  linux | darwin) ;;
  *)
    echo "❌ Unsupported OS: $OS"
    echo "   Windows: download forge-windows-amd64.exe from https://github.com/$REPO/releases/latest"
    exit 1
    ;;
esac

echo "🔍 Fetching latest forge release..."

VERSION=$(curl -fsSL \
  -H "Accept: application/vnd.github+json" \
  "https://api.github.com/repos/$REPO/releases/latest" \
  | grep '"tag_name"' \
  | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')

if [ -z "$VERSION" ]; then
  echo "❌ Could not fetch latest version."
  exit 1
fi

echo "📦 Latest version: $VERSION"

FILENAME="${BINARY}-${OS}-${ARCH}"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$VERSION/$FILENAME"
TMP_FILE=$(mktemp)

echo "⬇️  Downloading $FILENAME..."
HTTP_STATUS=$(curl -fsSL \
  -H "Authorization: Bearer $GITHUB_TOKEN" \
  -H "Accept: application/octet-stream" \
  "$DOWNLOAD_URL" -o "$TMP_FILE" -w "%{http_code}")

if [ "$HTTP_STATUS" != "200" ]; then
  echo "❌ Download failed (HTTP $HTTP_STATUS)."
  rm -f "$TMP_FILE"
  exit 1
fi

chmod +x "$TMP_FILE"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP_FILE" "$INSTALL_DIR/$BINARY"
else
  echo "🔑 Need sudo to install to $INSTALL_DIR..."
  sudo mv "$TMP_FILE" "$INSTALL_DIR/$BINARY"
fi

echo ""
echo "✅ forge $VERSION installed!"
echo ""
echo "Next step — log in once:"
echo "  forge auth login"
echo ""