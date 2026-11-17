#!/bin/sh
# Installs a zshai release bundle for this user (no sudo):
#   bin/zshai              -> ~/.local/bin/zshai
#   share/zshai/*          -> ~/.local/share/zshai/ (llama-server, models/, licenses)
#   zshai.plugin.zsh       -> ${ZSH_CUSTOM:-~/.oh-my-zsh/custom}/plugins/zshai/
# and, if there is no config yet, one that uses the built-in model ("server": "local").
#
#   ./install.sh [--model 1.5b|3b]
# The model is a separate download (zshai model install). Without --model: one that fits
# this machine, unless a model is installed already.
set -eu
model=""
while [ $# -gt 0 ]; do
  case $1 in
    --model) model=$2; shift 2 ;;
    --model=*) model=${1#--model=}; shift ;;
    *) echo "usage: $0 [--model 1.5b|3b]" >&2; exit 2 ;;
  esac
done
here=$(cd "$(dirname "$0")" && pwd)
bin=${ZSHAI_BIN_DIR:-$HOME/.local/bin}
data=${XDG_DATA_HOME:-$HOME/.local/share}/zshai
cfg=${XDG_CONFIG_HOME:-$HOME/.config}/zshai
plug=${ZSH_CUSTOM:-$HOME/.oh-my-zsh/custom}/plugins/zshai

mkdir -p "$bin" "$data/models" "$plug" "$cfg"
cp "$here/bin/zshai" "$bin/zshai.new" && mv -f "$bin/zshai.new" "$bin/zshai"
# a running llama-server keeps the old files open; replace, don't overwrite in place
"$bin/zshai" model stop >/dev/null 2>&1 || true
cp "$here/share/zshai/llama-server" "$data/llama-server.new" && mv -f "$data/llama-server.new" "$data/llama-server"
for m in "$here"/share/zshai/models/*.gguf; do [ -e "$m" ] && cp "$m" "$data/models/"; done
cp "$here"/share/zshai/*LICENSE* "$data/" 2>/dev/null || true
cp "$here/share/zshai/zshai.plugin.zsh" "$plug/zshai.plugin.zsh"
# macOS: files from a downloaded archive are quarantined; these are ours
command -v xattr >/dev/null 2>&1 && xattr -dr com.apple.quarantine "$bin/zshai" "$data/llama-server" 2>/dev/null || true
[ -e "$cfg/config.json" ] || printf '{\n  "server": "local"\n}\n' > "$cfg/config.json"

echo "Installed zshai to $bin/zshai and llama-server to $data."
case ":$PATH:" in *":$bin:"*) ;; *) echo "Add $bin to your PATH.";; esac
grep -q 'zshai' "$HOME/.zshrc" 2>/dev/null || echo "Add zshai to plugins=(...) in ~/.zshrc, then run: exec zsh"
# no model of a current size yet (an upgrade from 0.4 has only the 0.5B): get the one for this machine
if [ -z "$model" ] && ! ls "$data"/models/zshai-mini-1.5b-*.gguf "$data"/models/zshai-mini-3b-*.gguf >/dev/null 2>&1; then
  case $(uname -s)/$(uname -m) in
    Darwin/arm64) model=3b ;;
    *) model=1.5b ;;
  esac
fi
if [ -n "$model" ]; then
  echo "Downloading the zshai-mini $model model …"
  "$bin/zshai" model install "$model"
fi
"$bin/zshai" model list
