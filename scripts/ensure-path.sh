#!/bin/sh
# Add BINDIR to the user's PATH in their shell startup file if it is missing.
set -e
bindir=${1:-}
if [ -z "$bindir" ]; then
  echo "usage: ensure-path.sh BINDIR" >&2
  exit 2
fi

case "$bindir" in
  /*) ;;
  *)
    echo "ensure-path.sh: BINDIR must be an absolute path" >&2
    exit 2
    ;;
esac
if ! printf '%s' "$bindir" | grep -Eq '^[A-Za-z0-9/._+-]+$'; then
  echo "ensure-path.sh: BINDIR contains unsupported characters" >&2
  exit 2
fi

case ":$PATH:" in
  *":$bindir:"*)
    echo "$bindir is already on PATH"
    exit 0
    ;;
esac

shell=$(basename "${SHELL:-/bin/sh}")
marker="# glci PATH"
# bindir is validated above; keep PATH expansion literal for the next shell.
# shellcheck disable=SC2016
line="export PATH=\"$bindir:\$PATH\""
rc="$HOME/.profile"
case "$shell" in
  zsh) rc="${ZDOTDIR:-$HOME}/.zshrc" ;;
  bash) rc="$HOME/.bashrc" ;;
  fish)
    rc="$HOME/.config/fish/config.fish"
    line="fish_add_path -- \"$bindir\""
    ;;
esac

mkdir -p "$(dirname "$rc")"
if [ -f "$rc" ] && grep -F "$bindir" "$rc" >/dev/null 2>&1; then
  echo "$bindir is already listed in $rc (not on PATH in this shell; open a new terminal)"
  exit 0
fi

printf '\n%s\n%s\n' "$marker" "$line" >>"$rc"
echo "added $bindir to PATH in $rc"
echo "open a new terminal, or run: . $rc"
