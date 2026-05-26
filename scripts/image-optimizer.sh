#!/usr/bin/env bash

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Must be run from the repository root
# ─────────────────────────────────────────────────────────────────────────────
if ! [[ "$0" =~ scripts/image-optimizer.sh ]]; then
  echo "❌ Must be run from repository root: bash scripts/image-optimizer.sh" >&2
  exit 1
fi

# ─────────────────────────────────────────────────────────────────────────────
# Resolve target directory
# ─────────────────────────────────────────────────────────────────────────────
REPO_ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )/.." && pwd )"
DIR="${REPO_ROOT}/internal/web"

if [[ ! -d "${DIR}" ]]; then
  echo "❌ Target directory not found: ${DIR}" >&2
  exit 1
fi

# ─────────────────────────────────────────────────────────────────────────────
# Check required binaries
# ─────────────────────────────────────────────────────────────────────────────
MISSING=()
command -v jpegoptim &>/dev/null || MISSING+=("jpegoptim")
command -v optipng   &>/dev/null || MISSING+=("optipng")

if [[ ${#MISSING[@]} -gt 0 ]]; then
  echo "❌ Missing required tools: ${MISSING[*]}" >&2
  echo "   Install via:" >&2
  echo "     apt:  sudo apt install ${MISSING[*]}" >&2
  echo "     brew: brew install ${MISSING[*]}" >&2
  exit 1
fi

# ─────────────────────────────────────────────────────────────────────────────
# Collect files
# ─────────────────────────────────────────────────────────────────────────────
mapfile -t JPEGS < <(find "${DIR}" -type f \( -name "*.jpg" -o -name "*.jpeg" \))
mapfile -t PNGS  < <(find "${DIR}" -type f -name "*.png")

if [[ ${#JPEGS[@]} -eq 0 && ${#PNGS[@]} -eq 0 ]]; then
  echo "ℹ No images found in ${DIR}"
  exit 0
fi

# ─────────────────────────────────────────────────────────────────────────────
# Optimize JPEGs
# ─────────────────────────────────────────────────────────────────────────────
if [[ ${#JPEGS[@]} -gt 0 ]]; then
  echo "▶ Optimizing ${#JPEGS[@]} JPEG(s)…"
  jpegoptim --strip-all --preserve --max=85 "${JPEGS[@]}"
  echo "✓ JPEGs done"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Optimize PNGs
# ─────────────────────────────────────────────────────────────────────────────
if [[ ${#PNGS[@]} -gt 0 ]]; then
  echo "▶ Optimizing ${#PNGS[@]} PNG(s)…"
  optipng -fix -o5 -preserve "${PNGS[@]}"
  echo "✓ PNGs done"
fi

echo ""
echo "✓ All images optimized"
exit 0