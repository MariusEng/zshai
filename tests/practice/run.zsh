#!/bin/zsh
# Live practice against the real model: asks zshai each request in a file, runs the answers
# that are read-only, and prints command, exit status and the first lines of output.
#   python3 tests/practice/mkfiles.py DIR      # sample files (JSON array/Lines/object, CSV, TSV, log, YAML, md)
#   zsh tests/practice/run.zsh DIR tests/practice/files.txt
# Uses a separate config/state under DIR/.zshai so the real log is untouched.
dir=${1:?usage: run.zsh DIR REQUESTS}; reqs=${${2:?usage: run.zsh DIR REQUESTS}:A}
bin=${ZSHAI_BIN:-${0:A:h:h:h}/zshai}
export ZSHAI_CONFIG_DIR=$dir/.zshai/cfg ZSHAI_STATE_DIR=$dir/.zshai/state ZSHAI_CACHE_DIR=$dir/.zshai/cache
mkdir -p $ZSHAI_CONFIG_DIR
[[ -e $ZSHAI_CONFIG_DIR/config.json ]] || print -r -- '{"language":"no"}' > $ZSHAI_CONFIG_DIR/config.json
cd $dir || exit 1
typeset -A zshai_r
while IFS= read -r q; do
  [[ -z $q || $q == \#* ]] && continue
  zshai_r=()
  eval "$("$bin" ask --cwd $PWD -- "$q" 2>&1)"
  print -r -- "=== $q"
  print -r -- "  [$zshai_r[status] $zshai_r[level]] $zshai_r[cmd]"
  if [[ $zshai_r[level] == read && $zshai_r[status] == run ]]; then
    out=$(timeout 10 zsh -c "$zshai_r[cmd]" 2>&1); st=$?
    print -r -- "  exit $st"
    print -r -- "$out" | head -8 | sed 's/^/  | /'
  fi
done < $reqs
