#!/bin/sh
# Installs a zshai release bundle for this user (no sudo):
#   bin/zshai              -> ~/.local/bin/zshai
#   share/zshai/*          -> ~/.local/share/zshai/ (llama-server, models/, licenses)
#   zshai.plugin.zsh       -> ${ZSH_CUSTOM:-~/.oh-my-zsh/custom}/plugins/zshai/
# and, if there is no config yet, one that uses the built-in model ("server": "local").
set -eu
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
cp "$here"/share/zshai/models/*.gguf "$data/models/"
cp "$here"/share/zshai/*LICENSE* "$data/" 2>/dev/null || true
cp "$here/share/zshai/zshai.plugin.zsh" "$plug/zshai.plugin.zsh"
# macOS: files from a downloaded archive are quarantined; these are ours
command -v xattr >/dev/null 2>&1 && xattr -dr com.apple.quarantine "$bin/zshai" "$data/llama-server" 2>/dev/null || true
[ -e "$cfg/config.json" ] || printf '{\n  "server": "local"\n}\n' > "$cfg/config.json"

echo "Installed zshai to $bin/zshai and the built-in model to $data."
case ":$PATH:" in *":$bin:"*) ;; *) echo "Add $bin to your PATH.";; esac
grep -q 'zshai' "$HOME/.zshrc" 2>/dev/null || echo "Add zshai to plugins=(...) in ~/.zshrc, then run: exec zsh"
"$bin/zshai" model
