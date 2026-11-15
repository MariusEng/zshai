#!/bin/zsh
# rank-history.zsh: rank the commands in your zsh history (zshai PLAN.md §3.2).
#
# Prints only command names (plus git/brew/... subcommands and the package names
# you installed). It never prints arguments, paths or file names, and it sends
# nothing anywhere.
#
#   zsh rank-history.zsh                  # reads $HISTFILE or ~/.zsh_history
#   zsh rank-history.zsh -n 60 -m 2       # top 60, add-candidates used >= 2 times
#   zsh rank-history.zsh -f other_history > ranking.txt
#
# Needs only zsh (5.8+; macOS ships 5.9). Aliases, functions and PATH come from
# your own interactive login shell (`zsh -lic`), so oh-my-zsh aliases like
# `gst` are counted as `git status`.

emulate -L zsh
setopt extendedglob no_unset pipe_fail
zmodload zsh/parameter

# ---------------------------------------------------------------- options
integer top=40 min_add=3
local file=${HISTFILE:-$HOME/.zsh_history}
while (( $# )); do
  case $1 in
    (-n|--top)  top=$2;      shift 2 ;;
    (-m|--min)  min_add=$2;  shift 2 ;;
    (-f|--file) file=$2;     shift 2 ;;
    (-h|--help) sed -n '2,15s/^# \{0,1\}//p' $0; exit 0 ;;
    (*) print -u2 "rank-history: unknown option '$1' (try --help)"; exit 2 ;;
  esac
done
[[ -r $file ]] || { print -u2 "rank-history: can't read $file"; exit 1 }

# ---------------------------------------------------------------- the common-tools list (PLAN.md §3.2)
# binary -> area. Alternate binary names (fdfind, batcat, 7zz, convert) map to the same area.
typeset -A common=(
  jq data  yq data  mlr data  sqlite3 data
  rg search  fd search  fdfind search  fzf search  bat search  batcat search  eza search  tree search  ncdu search
  7z archive  7zz archive  zip archive  unzip archive  xz archive  zstd archive  rsync archive  rclone archive
  wget network  http network  dig network  whois network  mtr network  nc network  yt-dlp network
  magick images  convert images  mogrify images  exiftool images  optipng images  jpegoptim images  cwebp images  sips images
  ffmpeg media  ffprobe media  sox media  mediainfo media
  pandoc docs  pdftotext docs  pdfinfo docs  pdfunite docs  qpdf docs  gs docs  soffice docs  tesseract docs
  git dev  gh dev  tmux dev  htop dev  btop dev  watch dev  tldr dev  docker dev  podman dev  code dev  fastfetch dev
  ssh remote  scp remote  ping remote
  cargo project  make project  go project  npm project  pnpm project  python3 project  uv project
  gdate gnu  gsed gnu  gawk gnu  gfind gnu
)
# Commands whose first non-flag argument is worth counting (no other arguments are kept).
typeset -A has_sub
local c
for c in git brew apt apt-get pacman yay paru dnf port docker podman gh npm pnpm yarn bun pip pip3 \
         uv cargo go kubectl helm systemctl launchctl tmux rclone defaults diskutil xcode-select mas; do
  has_sub[$c]=1
done

# ---------------------------------------------------------------- your shell's aliases, functions and PATH
typeset -A alias_of user_funcs
local brew_prefix='' line
for line in "${(@f)$(zsh -lic '
    print -r -- "@@PATH $PATH"
    print -r -- "@@BREW ${HOMEBREW_PREFIX:-}"
    local k; for k in ${(k)aliases}; do print -r -- "@@ALIAS $k ${aliases[$k]//$'"'"'\n'"'"'/ }"; done
    for k in ${(k)functions}; do print -r -- "@@FUNC $k"; done
  ' 2>/dev/null </dev/null)}"; do
  case $line in
    ('@@PATH '*)  [[ -n ${line#@@PATH } ]] && path=( ${(s.:.)${line#@@PATH }} ) ;;
    ('@@BREW '*)  brew_prefix=${line#@@BREW } ;;
    ('@@ALIAS '*) line=${line#@@ALIAS }; alias_of[${line%% *}]=${line#* } ;;
    ('@@FUNC '*)  user_funcs[${line#@@FUNC }]=1 ;;
  esac
done
rehash
if [[ -z $brew_prefix ]]; then
  if   [[ -x /opt/homebrew/bin/brew ]]; then brew_prefix=/opt/homebrew
  elif [[ -x /usr/local/bin/brew ]];    then brew_prefix=/usr/local
  elif [[ -x /home/linuxbrew/.linuxbrew/bin/brew ]]; then brew_prefix=/home/linuxbrew/.linuxbrew
  fi
fi

# ---------------------------------------------------------------- read the history with zsh itself
HISTSIZE=100000000
fc -R $file
print -s __rank_history_end__     # makes the last real entry visible in $history

# ---------------------------------------------------------------- parsing
typeset -A count sub alias_used pkg via_path
inc() {  # inc ASSOC KEY: add $weight (assoc += would append strings)
  local cur=${${(P)1}[$2]:-0}
  eval "$1[\$2]=$(( cur + weight ))"
}
integer total=0 entries=${#history} weight=1

# parse_line TEXT DEPTH: split TEXT into simple commands and count each one.
parse_line() {
  local text=$1; integer depth=$2
  (( depth > 5 )) && return
  local -a toks seg
  toks=( ${(z)text} )
  local t inner
  for t in $toks; do
    case $t in
      ('|'|'||'|'&&'|';'|'&'|'|&'|'&!'|'&|'|';;'|';&'|';|'|'('|')'|'{'|'}'|$'\n')
        (( $#seg )) && count_simple $depth $seg
        seg=() ;;
      (*) seg+=( $t ) ;;
    esac
    # command substitutions $(...) and `...` inside a word
    if [[ $t == *'$('*')'* ]]; then
      inner=${t#*\$\(}; inner=${inner%\)*}
      parse_line $inner $(( depth + 1 ))
    elif [[ $t == *'`'*'`'* ]]; then
      inner=${t#*\`}; inner=${inner%\`*}
      parse_line $inner $(( depth + 1 ))
    fi
  done
  (( $#seg )) && count_simple $depth $seg
}

# count_simple DEPTH WORDS...: one simple command (no pipes/lists).
count_simple() {
  integer depth=$1; shift
  local -a w; w=( "$@" )
  integer i=1 n=$#w
  local t name bypass=0

  while (( i <= n )); do
    t=$w[i]
    case $t in
      # skip the whole segment: loops, tests, arithmetic, function definitions
      (for|select|case|function|repeat|'[['|'(('|done|fi|esac|in|']]'|'))') return ;;
      # keywords and plain prefixes
      (if|then|else|elif|do|while|until|'!'|time|coproc|nohup|command|builtin|exec|noglob|nocorrect|-) (( i++ )); continue ;;
      # VAR=value
      ([A-Za-z_][A-Za-z0-9_]#(|\[*\])(|+)=*) (( i++ )); continue ;;
      # redirections: '>' file, '2>' file, '>file', '<<<' word ...
      ((|<->|'&')('>'|'>>'|'<'|'<<'|'<<<'|'>|'|'>&'|'<&'|'&>'|'&>>'|'>!'))       (( i += 2 )); continue ;;
      ((|<->|'&')('>'|'>>'|'<'|'<<<'|'>&'|'<&'|'&>'|'&>>')?*)                     (( i++ )); continue ;;
      # wrappers that run another command: count the wrapper, then go on to its command
      (sudo|doas)
        inc count $t; (( total += weight, i++ ))
        while (( i <= n )) && [[ $w[i] == -* ]]; do
          [[ $w[i] == (-u|-g|-C|-D|-h|-p|-r|-t|-U|-T) ]] && (( i++ ))
          (( i++ ))
        done
        continue ;;
      (env)
        (( i++ ))
        while (( i <= n )) && [[ $w[i] == (-*|*=*) ]]; do
          [[ $w[i] == (-u|-C|-S|-P) ]] && (( i++ ))
          (( i++ ))
        done
        continue ;;
      (nice|ionice|stdbuf|caffeinate|timeout|xargs|watch|chronic|unbuffer)
        inc count $t; (( total += weight, i++ ))
        while (( i <= n )) && [[ $w[i] == -* ]]; do
          case $t:$w[i] in
            (nice:-n|ionice:-[cn]|caffeinate:-[tw]|timeout:-[sk]|watch:-[nd]|xargs:-[InPLsdEJR]) (( i++ )) ;;
          esac
          (( i++ ))
        done
        [[ $t == timeout ]] && (( i++ ))          # the duration
        if [[ $t == watch ]] && (( i == n )); then  # watch 'df -h'
          parse_line ${(Q)w[i]} $(( depth + 1 )); return
        fi
        continue ;;
    esac
    break
  done
  (( i > n )) && return

  name=$w[i]
  [[ $name == \\* ]] && { bypass=1; name=${name#\\} }
  name=${(Q)name}

  # aliases: resolve and parse the expansion with the rest of the line
  if (( ! bypass )) && [[ -n ${alias_of[$name]-} ]]; then
    local exp=$alias_of[$name]
    local -a etoks; etoks=( ${(z)exp} )
    inc alias_used $name
    if [[ ${(Q)etoks[1]} != $name ]]; then
      parse_line "$exp ${(j: :)w[i+1,-1]}" $(( depth + 1 ))
      return
    fi
  fi

  if [[ $name == */* ]]; then
    via_path[${name:t}]=$name
    name=${name:t}
  fi
  [[ $name == [[:alnum:]_.+@:-]## && $name != [-.]* ]] || return
  # lines like '69' or '10M35' are answers typed into another program, not commands
  # (single digits stay: oh-my-zsh's dir-stack aliases 1-9 are resolved above)
  [[ $name == [0-9]* && $name != [1-9] ]] && return

  inc count $name
  (( total += weight ))

  # subcommand (first non-flag word) for a fixed set of tools
  if (( ${+has_sub[$name]} )); then
    integer j=i+1
    while (( j <= n )); do
      case $name:$w[j] in
        (git:-[Cc]|brew:--prefix|docker:--context|docker:-H|kubectl:-[nc]|kubectl:--context|kubectl:--namespace|systemctl:-M)
          (( j += 2 )); continue ;;
        (*:-*) (( j++ )); continue ;;
      esac
      break
    done
    local s=${(Q)w[j]-}
    if [[ $name == pacman ]]; then
      local f
      for f in $w[i+1,n]; do [[ $f == -[A-Z]* ]] && { s=${f[1,2]}; break }; done
    fi
    [[ $s == (-[A-Z]|[a-z][a-z0-9-]#) ]] && inc sub "$name $s"

    # packages installed through a package manager (names only)
    local mgr='' k
    case $name:$s in
      (brew:install|brew:reinstall|apt:install|apt-get:install|dnf:install|port:install|yay:-S|paru:-S|mas:install) mgr=$name ;;
      (pacman:-S) [[ ${(j: :)w} != *-S[a-z]#[sicgpl]* ]] && mgr=pacman ;;
    esac
    if [[ -n $mgr ]]; then
      local -a args; args=( $w[j+1,n] )
      [[ $mgr == (pacman|yay|paru) ]] && args=( $w[i+1,n] )
      for k in $args; do
        k=${(Q)k}
        [[ $k == [a-z0-9@][a-z0-9@._+/-]# && $k != */*/* && $k != ($mgr|install|reinstall) ]] && inc pkg "$mgr ${k:t}"
      done
    fi
  fi
}

# Parse each distinct line once and weight it by how often it occurs.
typeset -A uniq
local key
for key in ${(k)history}; do
  key=$history[$key]
  uniq[$key]=$(( ${uniq[$key]:-0} + 1 ))
done
for key in ${(k)uniq}; do
  weight=$uniq[$key]
  parse_line $key 0
done
weight=1

# ---------------------------------------------------------------- classification
kind_of() {
  local n=$1 p
  [[ $(whence -w $n 2>/dev/null) == *': '(builtin|reserved) ]] && { print builtin; return }
  if [[ -n ${via_path[$n]-} && ${via_path[$n]} == (./*|../*|'~'*|\$*|[^/]*) ]]; then print 'own script'; return; fi
  p=$(whence -p $n 2>/dev/null)
  if [[ -z $p ]]; then
    (( ${+user_funcs[$n]} )) && { print function; return }
    print 'not found'; return
  fi
  # classify by the PATH entry, not the symlink target (Ubuntu 26.04's /usr/bin/ls -> rust coreutils)
  [[ -n $brew_prefix && ${p:A} == $brew_prefix/* ]] && { print brew; return }
  if [[ $OSTYPE == darwin* ]]; then
    case $p in
      (/usr/bin/*) [[ -e /Library/Developer/CommandLineTools/usr/bin/$n || -e /Applications/Xcode.app/Contents/Developer/usr/bin/$n ]] \
                     && { print 'macOS (CLT)'; return } ;;
    esac
  fi
  case $p in
    (${brew_prefix:-/nonexistent}/*) print brew ;;
    (/bin/*|/usr/bin/*|/sbin/*|/usr/sbin/*|/usr/libexec/*|/System/*)
      if [[ $OSTYPE == darwin* ]]; then print macOS; else print system; fi ;;
    ($HOME/*) print home ;;
    (*) print other ;;
  esac
}

# ---------------------------------------------------------------- output
sorted() {  # sorted ASSOC-NAME: "count<TAB>key" lines, biggest first
  local k
  for k in ${(kP)1}; do print -r -- "${${(P)1}[$k]}"$'\t'"$k"; done | sort -t$'\t' -k1,1nr -k2,2
}

local first='' last=''
if [[ $(head -c 2 $file) == ': ' ]]; then
  first=$(grep -m1 -o '^: [0-9]*' $file | cut -c3-)
  last=$(grep -o '^: [0-9]*' $file | tail -1 | cut -c3-)
  zmodload zsh/datetime
  strftime -s first '%Y-%m-%d' $first
  strftime -s last  '%Y-%m-%d' $last
fi

print "zshai rank-history: ${file/#$HOME/~}"
print "  $entries history entries${first:+, $first → $last}; $total commands counted (aliases resolved; sudo/env/xargs/time unwrapped)"
print "  $(uname -s) $(uname -r), zsh $ZSH_VERSION${brew_prefix:+, Homebrew at $brew_prefix}"
print

print "Top $top commands"
printf '  %4s %7s %6s  %-18s %-12s %s\n' '#' count '%' command kind 'zshai list'
integer r=0
local nm kind tool sc
for line in "${(@f)$(sorted count)}"; do
  (( ++r > top )) && break
  c=${line%%$'\t'*}; nm=${line#*$'\t'}
  kind=$(kind_of $nm)
  printf '  %4d %7d %6.1f  %-18s %-12s %s\n' $r $c $(( 100.0 * c / (total ? total : 1) )) $nm $kind ${common[$nm]:+common: $common[$nm]}
done
print

print 'Subcommands (git, brew, docker, ...): top 8 each'
typeset -A by_tool tool_n
for line in "${(@f)$(sorted sub)}"; do
  [[ -z $line ]] && continue
  c=${line%%$'\t'*}; nm=${line#*$'\t'}
  tool=${nm%% *} sc=${nm#* }
  (( ${tool_n[$tool]:-0} >= 8 )) && continue
  tool_n[$tool]=$(( ${tool_n[$tool]:-0} + 1 ))
  by_tool[$tool]+="${by_tool[$tool]:+, }$sc $c"
done
for nm in ${(k)by_tool}; do printf '  %-10s %s\n' $nm $by_tool[$nm]; done | sort
print

print 'Most used aliases'
r=0
local -a al
for line in "${(@f)$(sorted alias_used)}"; do
  [[ -z $line ]] && continue
  (( ++r > 15 )) && break
  nm=${line#*$'\t'}
  al+=( "$nm→${${(z)alias_of[$nm]}[1,2]} (${line%%$'\t'*})" )
done
print -r -- "  ${(j:, :)al:-none}"
print

print 'Packages installed from history (package manager: name ×count)'
if (( ${#pkg} )); then
  local -a pl
  for line in "${(@f)$(sorted pkg)}"; do pl+=( "${line#*$'\t'} ×${line%%$'\t'*}" ); done
  print -r -- "  ${(j:, :)pl}" | fold -s -w 110 | sed '2,$s/^/  /'
else
  print '  none found'
fi
print

print "Candidates to ADD to the common-tools list (used ≥ $min_add×, not part of the OS, not in the list)"
local -a add gone
for line in "${(@f)$(sorted count)}"; do
  c=${line%%$'\t'*}; nm=${line#*$'\t'}
  (( c < min_add )) && break
  (( ${+common[$nm]} )) && continue
  kind=$(kind_of $nm)
  case $kind in
    (brew|home|other) add+=( "$nm ($c, $kind)" ) ;;
    ('not found')     gone+=( "$nm ($c)" ) ;;
  esac
done
print -r -- "  ${(j:, :)add:-none}" | fold -s -w 110 | sed '2,$s/^/  /'
print
print "Used ≥ $min_add× but not found now (uninstalled tools, typos, or plain-language attempts)"
print -r -- "  ${(j:, :)gone:-none}" | fold -s -w 110 | sed '2,$s/^/  /'
print

print 'Common-tools list entries you never used (installed now: ✓)'
local -a never
for nm in ${(ok)common}; do
  (( ${+count[$nm]} )) && continue
  never+=( "$nm${commands[$nm]:+ ✓}" )
done
print -r -- "  ${(j:, :)never}" | fold -s -w 110 | sed '2,$s/^/  /'
print
print 'Only command names, subcommands and package names are shown. Paste this output back to Claude.'
