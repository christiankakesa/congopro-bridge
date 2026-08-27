#!/usr/bin/env bash
# Sets one KEY=value inside a systemd EnvironmentFile.
#
# The value is read from STDIN, never from argv — anything passed as an
# argument is visible to every user on the box via `ps`, and lands in shell
# history. Existing occurrences of the key are replaced, so re-running is
# idempotent and never leaves two definitions (systemd would take the last,
# silently).
set -euo pipefail

KEY="${1:?usage: secret-set.sh KEY ENVFILE  (value on stdin)}"
FILE="${2:?usage: secret-set.sh KEY ENVFILE  (value on stdin)}"

case "$KEY" in
  [A-Za-z_][A-Za-z0-9_]*) ;;
  *) echo "✗ invalid env key: $KEY" >&2; exit 2 ;;
esac

IFS= read -r VALUE || true
if [ -z "${VALUE:-}" ]; then
  echo "✗ empty value — nothing written" >&2
  exit 1
fi

umask 077
mkdir -p "$(dirname "$FILE")"
touch "$FILE"
TMP="$(mktemp "${FILE}.XXXXXX")"
grep -v "^${KEY}=" "$FILE" > "$TMP" || true
printf '%s=%s\n' "$KEY" "$VALUE" >> "$TMP"
mv "$TMP" "$FILE"
chown root:root "$FILE" 2>/dev/null || true
chmod 600 "$FILE"
echo "✓ ${KEY} set in ${FILE}"
