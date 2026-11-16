# zshai.plugin.zsh: natural language for zsh (oh-my-zsh plugin). See PLAN.md.
#
#   plugins=(git zshai)            in ~/.zshrc, with the `zshai` helper on PATH
#
# Type  zshai: <what you want>  (or the short  zai: …) and press
# Enter. "zshai: hvordan …" / "how do I …" / "forklar …" prints and explains the command
# without running it. Other lines run unchanged; ?text is short for zshai:, ,command forces
# the shell. "zshai: auto på" also sends plain-language lines without the prefix.
# zshai: hjelp / help lists zshai's own commands.
#
# Safety rule for this file: a command that needs confirmation is NEVER put in
# $BUFFER before the user said yes (or chose [e]dit, which still needs Enter).
# Every turn runs inside an `always` block that empties $BUFFER on any error.

zmodload zsh/parameter zsh/datetime zsh/zselect 2>/dev/null
autoload -Uz add-zle-hook-widget add-zsh-hook

typeset -g ZSHAI_PLUGIN_VERSION=dev   # set by make dist / make install
typeset -gx ZSHAI_ZSH_VERSION=$ZSH_VERSION   # for the helper: {shell} in the system prompt, zsh: in the context
typeset -g ZSHAI_BIN=${ZSHAI_BIN:-${commands[zshai]:-$HOME/.local/bin/zshai}}
# commands zshai writes may use  zshai fmt  (PowerShell-style tables and lists): make
# sure the name works even when the helper isn't on PATH
(( ${+commands[zshai]} )) || zshai() { "$ZSHAI_BIN" "$@" }
typeset -g _zshai_cache=${ZSHAI_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/zshai}
typeset -gA zshai_r
typeset -g _zshai_turn_id='' _zshai_ran='' _zshai_pending='' _zshai_resume=''
typeset -g _zshai_last_cmd='' _zshai_accept=.accept-line _zshai_show='' _zshai_autorun=''
typeset -gi _zshai_last_exit=0 _zshai_dumped=0
# "zshai: …" always goes to zshai. With auto-detect on (zshai: auto på), plain-language
# lines without the prefix do too.
typeset -g _zshai_auto=0 _zshai_fix=0 _zshai_errfile='' _zshai_errfd=''
typeset -ga _zshai_fixq
_zshai_settings() {
  emulate -L zsh
  local s
  s=$("$ZSHAI_BIN" settings 2>/dev/null)
  [[ $s == _zshai_auto=[01]' '_zshai_fix=[01] ]] && eval "$s"
}
_zshai_settings

# Colours (PLAN.md §1.5) as SGR codes, from the theme ("zshai: tema"): cmd and explain
# for show/explain mode, ask for questions, note for zshai's quiet lines, danger.
# Plain escape codes on purpose: model text never goes through prompt expansion
# (prompt_subst would run $(...) in it). The prompt itself is never touched.
typeset -gA ZSHAI_COLORS
typeset -g _zshai_bg=${ZSHAI_BG-}
# COLORFGBG ("15;0") is set by some terminals: background 0-6 or 8 = dark
if [[ -z $_zshai_bg && -n ${COLORFGBG-} ]]; then
  case ${COLORFGBG##*;} in
    ([0-6]|8) _zshai_bg=dark ;;
    (<->)     _zshai_bg=light ;;
  esac
fi

_zshai_colors() {
  local out
  out=$("$ZSHAI_BIN" colors ${_zshai_bg:+--bg} ${_zshai_bg:+$_zshai_bg} 2>/dev/null)
  if [[ $out == 'ZSHAI_COLORS=('* ]]; then
    eval "$out"
  else
    ZSHAI_COLORS=(cmd 1 explain 97 ask 33 note 90 danger '1;31' yes '1;32' no '1;31' show '1;33' edit '1;36' _auto 0)
  fi
}

_zshai_colors

# Ask the terminal for its background colour (OSC 11), once per shell, only for the
# "auto" theme and only from inside a widget (the reply must not end up in the line).
# Terminals that don't answer cost one 0.3 s wait the first time.
_zshai_detect_bg() {
  emulate -L zsh
  setopt extended_glob
  [[ -n $_zshai_bg || ${ZSHAI_COLORS[_auto]-} != 1 ]] && return
  _zshai_bg=unknown
  local saved reply='' c
  saved=$(stty -g </dev/tty 2>/dev/null) || return
  stty -icanon -echo min 0 time 3 </dev/tty 2>/dev/null
  print -n -- $'\e]11;?\e\\' >/dev/tty
  while read -rs -k1 -t 0.3 c </dev/tty; do
    reply+=$c
    [[ $reply == *($'\a'|$'\e\\') ]] && break
  done
  stty "$saved" </dev/tty 2>/dev/null
  local -a match mbegin mend
  if [[ $reply == (#b)*rgb:([[:xdigit:]]##)/([[:xdigit:]]##)/([[:xdigit:]]##)* ]]; then
    local -i r=16#${match[1][1,2]} g=16#${match[2][1,2]} b=16#${match[3][1,2]}
    (( (299 * r + 587 * g + 114 * b) / 1000 < 128 )) && _zshai_bg=dark || _zshai_bg=light
    _zshai_colors
  fi
}

# Keep any accept-line wrapper another plugin installed before us.
if [[ ${widgets[accept-line]-} == user:* && ${widgets[accept-line]-} != user:zshai-accept-line ]]; then
  zle -A accept-line _zshai_orig_accept_line
  _zshai_accept=_zshai_orig_accept_line
fi

# Write aliases and function names for the helper (risk rules resolve aliases like grhh).
_zshai_dump() {
  emulate -L zsh
  (( EPOCHSECONDS - _zshai_dumped < 300 )) && return
  _zshai_dumped=$EPOCHSECONDS
  [[ -d $_zshai_cache ]] || mkdir -p -m 700 $_zshai_cache
  alias -L >| $_zshai_cache/aliases.zsh 2>/dev/null
  print -rl -- ${(k)functions:#(_*|*:*)} >| $_zshai_cache/functions.txt 2>/dev/null
}

_zshai_is_cmd() {
  emulate -L zsh
  local w=$1
  (( ${+commands[$w]} || ${+aliases[$w]} || ${+functions[$w]} || ${+builtins[$w]} || ${+reswords[$w]} )) && return 0
  [[ $w == */* || $w == *=* || $w == '~'* || -d $w ]]
}

# Return 0 if the line should go to zshai (PLAN.md §2.1).
_zshai_route() {
  emulate -L zsh
  setopt extended_glob
  local line=${1##[[:space:]]#}
  [[ -z $line ]] && return 1
  case $line in
    ('?'*) return 0 ;;
    (','*|'!'*|'#'*) return 1 ;;
  esac
  [[ $_zshai_auto == 1 ]] || return 1
  local -a w
  w=( ${(z)line} )
  local first=${(Q)w[1]}
  if ! _zshai_is_cmd "$first"; then
    # lone codes like 69 or 10M35 are answers typed into other programs; one or two
    # letters are typos or stray keys. Let the shell say "command not found".
    [[ $#w == 1 && $first == [[:alnum:]]## && ( $first == *[0-9]* || ${#first} -le 2 ) ]] && return 1
    return 0
  fi
  (( $#w < 3 )) && return 1
  [[ $line == *[\|\&\;\<\>\(\)\$\`\\\*\{\}\[\]=\"]* || " $line" == *' -'* ]] && return 1
  "$ZSHAI_BIN" route --cwd "$PWD" -- "$line" >/dev/null 2>&1
}

_zshai_say() {  # _zshai_say colour text
  print -r -- $'\e['"$1"'m  '"$2"$'\e[0m'
}

_zshai_eval() {  # load the helper's  zshai_r=( ... )  output
  zshai_r=()
  if [[ $1 == 'zshai_r=('* ]]; then
    eval "$1"
  else
    zshai_r=(status error msg "${1:-no answer from the zshai helper}")
  fi
}

# One raw key from the terminal. Inside a widget stdin is not the terminal, and after
# `zle -I` the tty is in cooked mode, so open /dev/tty and set raw mode on it ourselves.
_zshai_getc() {
  local k='' saved rc
  saved=$(stty -g </dev/tty 2>/dev/null)
  stty -icanon -echo min 1 time 0 </dev/tty 2>/dev/null
  read -k1 k </dev/tty
  rc=$?
  [[ -n $saved ]] && stty "$saved" </dev/tty 2>/dev/null
  REPLY=$k
  return $rc
}

# Ask inline, under the ▸ line:  "  Skal jeg …? ja / nei / vis / endre > j"
# Colour the first letter of each choice in "ja / nei / vis / endre" (theme roles
# yes/no/show/edit): that letter is the key to press. Lines without " / " stay as they are.
_zshai_keys() {
  emulate -L zsh
  local s=$1 p role
  local -a parts out
  if [[ $s != *' / '* ]]; then
    REPLY=$s
    return
  fi
  parts=( "${(@s: / :)s}" )
  for p in $parts; do
    case ${p[1]} in
      ([jy]) role=yes ;;  (n) role=no ;;  ([vs]) role=show ;;  (e) role=edit ;;  (*) role='' ;;
    esac
    if [[ -n $role && -n ${ZSHAI_COLORS[$role]-} ]]; then
      out+=( $'\e['"${ZSHAI_COLORS[$role]}m${p[1]}"$'\e[0m'"${p[2,-1]}" )
    else
      out+=( "$p" )
    fi
  done
  REPLY=${(j: / :)out}
}

_zshai_key() {  # one key, lower-cased, in REPLY
  local k=''
  _zshai_keys "$2"
  print -rn -- $'\e['"$ZSHAI_COLORS[ask]"'m  '"$1"$'\e[0m  '"$REPLY"' > '
  _zshai_getc && k=$REPLY || k=n
  [[ $k == [[:print:]] ]] && print -rn -- "$k"
  print
  REPLY=${k:l}
}

_zshai_typed() {  # a short typed answer (for JA/YES), in REPLY
  local buf='' k
  print -rn -- $'\e['"$ZSHAI_COLORS[danger]"'m  '"$1"$'\e[0m  '"$2"' > '
  while _zshai_getc; do
    k=$REPLY
    case $k in
      ($'\n'|$'\r') break ;;
      ($'\x7f'|$'\b') [[ -n $buf ]] && { buf=${buf%?}; print -n $'\b \b' } ;;
      ($'\e') buf=''; break ;;
      (*) buf+=$k; print -rn -- "$k" ;;
    esac
  done
  print
  REPLY=$buf
}

# Run a command as if the user had typed it (it lands in history, runs in this shell).
# Outside a widget (the fix question is asked from precmd) there is no line to accept
# yet: the next prompt's line-init hook runs it. Only confirmed commands get here.
_zshai_run() {
  _zshai_ran=1
  if [[ -z ${WIDGET-} ]]; then
    _zshai_autorun=$1
    return
  fi
  BUFFER=$1
  CURSOR=$#BUFFER
  zle $_zshai_accept
}

_zshai_turn() {
  emulate -L zsh
  setopt local_traps extended_glob
  local line=$1 req out
  req=${line##[[:space:]]#}
  req=${req#(zshai|zai):}
  req=${req##[[:space:]]#}
  req=${req#\?}
  req=${req##[[:space:]]#}
  _zshai_dump
  [[ $line == [[:space:]]* ]] || print -s -- "$line"   # the request goes into history too
  _zshai_handle ask --cwd "$PWD" --last "$_zshai_last_cmd" --exit "$_zshai_last_exit" -- "$req"
}

# Ask the helper (ask … or a fix) and act on its answer.
_zshai_handle() {
  emulate -L zsh
  setopt local_traps extended_glob
  local out
  [[ -n ${WIDGET-} ]] && zle -I   # keeps the typed request on screen; our output goes below it
  BUFFER=''
  _zshai_detect_bg
  # the folder was replaced (deleted and created again, e.g. re-copied): step into the new one
  [[ -d $PWD && ! . -ef $PWD ]] && builtin cd -q -- "$PWD"
  print -rn -- $'\e['"$ZSHAI_COLORS[note]"'m  …'$'\e[0m'
  # the helper draws a spinner with a waving label on /dev/tty while it waits for the
  # model ("thinking", "retrying", "thinking harder"); ZSHAI_SPINNER is its colour
  out=$(ZSHAI_SPINNER=${ZSHAI_COLORS[note]-90} "$ZSHAI_BIN" "$@" 2>&1)
  print -n -- $'\r\e[2K\e[?25h'
  _zshai_eval "$out"
  _zshai_turn_id=${zshai_r[id]-}
  [[ -n ${zshai_r[failed]-} ]] && _zshai_say $ZSHAI_COLORS[ask] "${zshai_r[failed]}"
  case ${zshai_r[status]-} in
    (none) ;;   # e.g. no fix for a failed command
    (run)
      _zshai_run "${zshai_r[cmd]}" ;;
    (confirm|install)
      _zshai_say $ZSHAI_COLORS[note] "▸ ${zshai_r[cmd]}   [${zshai_r[label]}]"
      [[ -n ${zshai_r[note]-} ]] && _zshai_say $ZSHAI_COLORS[danger] "${zshai_r[note]}"
      _zshai_confirm ;;
    (show)
      # learn mode: print the command and what it does; never run it
      _zshai_say $ZSHAI_COLORS[cmd] "▸ ${zshai_r[cmd]//$'\n'/$'\n'    }"
      local e
      for e in "${(@f)zshai_r[explain]-}"; do
        [[ -n $e ]] && _zshai_say $ZSHAI_COLORS[explain] "  $e"
      done
      [[ -n ${zshai_r[note]-} ]] && _zshai_say $ZSHAI_COLORS[ask] "${zshai_r[note]}"
      if [[ -n ${zshai_r[hint]-} ]]; then
        _zshai_say $ZSHAI_COLORS[note] "${zshai_r[hint]}"
        # print -s inside a widget is only reachable with ↑ after a line is accepted:
        # remember it and add it in precmd, then accept an empty line to get there
        _zshai_show=${zshai_r[cmd]}
        _zshai_run ''
        _zshai_ran=''
      fi ;;
    (answer)
      _zshai_say $ZSHAI_COLORS[ask] "${zshai_r[msg]}"
      [[ -n ${zshai_r[followup]-} ]] && BUFFER='? ' && CURSOR=2 ;;
    (*)
      _zshai_say $ZSHAI_COLORS[danger] "${zshai_r[msg]:-$out}"
      [[ -n ${zshai_r[cmd]-} ]] && _zshai_say $ZSHAI_COLORS[note] "▸ ${zshai_r[cmd]}" ;;
  esac
}

_zshai_confirm() {
  emulate -L zsh
  setopt extended_glob
  local kind=${zshai_r[status]} level=${zshai_r[level]} id=${zshai_r[id]} request=${zshai_r[request]-}
  local cmd=${zshai_r[cmd]} cancel=${zshai_r[cancel]:-Avbrutt.} out
  # test hook: simulate an internal error at the worst moment (tests/pty)
  [[ -n ${ZSHAI_TEST_FAULT-} ]] && status=1
  while true; do
    if [[ $level == danger ]]; then
      _zshai_typed "${zshai_r[question]}" "${zshai_r[keys]}"
      [[ $REPLY == (JA|YES|ja|yes) ]] && REPLY=y || REPLY=n
    else
      _zshai_key "${zshai_r[question]}" "${zshai_r[keys]}"
    fi
    case $REPLY in
      (j|y)
        if [[ $kind == install ]]; then
          _zshai_pending=$request
          _zshai_run "$cmd"
          return
        fi
        out=$("$ZSHAI_BIN" confirm "$id" 2>&1)
        _zshai_eval "$out"
        case ${zshai_r[status]-} in
          (run)  _zshai_turn_id=$id; _zshai_run "${zshai_r[cmd]}" ;;
          (done) _zshai_say $ZSHAI_COLORS[note] "${zshai_r[msg]}" ;;
          (*)    _zshai_say $ZSHAI_COLORS[danger] "${zshai_r[msg]:-$out}" ;;
        esac
        return ;;
      (v|s)
        "$ZSHAI_BIN" show "$id" | sed 's/^/    /'
        continue ;;
      (e)
        # put the command in the line for editing; it runs only when the user presses Enter
        "$ZSHAI_BIN" outcome "$id" edited &>/dev/null &!
        [[ -z ${WIDGET-} ]] && print -z -- "$cmd"   # from precmd: the next prompt's line
        BUFFER=$cmd
        CURSOR=$#BUFFER
        return ;;
      (*)
        _zshai_say $ZSHAI_COLORS[note] "$cancel"
        "$ZSHAI_BIN" outcome "$id" cancelled &>/dev/null &!
        return ;;
    esac
  done
}

zshai-accept-line() {
  setopt local_options extended_glob
  local line=$BUFFER
  case $line in
    ([[:space:]]#,*)
      BUFFER=${line#*,}
      zle $_zshai_accept
      return ;;
    ([[:space:]]#(zshai|zai):[[:space:]]#(theme|tema)[[:space:]]#)
      # show the themes as they look and pick one with a key
      print -s -- "$line"
      BUFFER=''
      zle -I
      _zshai_eval "$("$ZSHAI_BIN" themes 2>&1)"
      print -rn -- "${zshai_r[list]-}"
      _zshai_key "${zshai_r[question]-}" ''
      if [[ $REPLY == <1-> ]] && (( REPLY <= ${zshai_r[count]:-0} )); then
        "$ZSHAI_BIN" meta -- "zshai: tema $REPLY"
        _zshai_colors
      fi
      return ;;
    ([[:space:]]#(zshai|zai):*)
      # zshai's own commands (hjelp, angre, …); anything else is a request (exit 3)
      local mout mst
      mout=$(ZSHAI_PLUGIN_VERSION=$ZSHAI_PLUGIN_VERSION "$ZSHAI_BIN" meta -- "${line##[[:space:]]#}" 2>&1)
      mst=$?
      if (( mst == 4 )); then
        # edit a file zshai named (zshai: prompt): the editor runs as a normal command line
        local ed=${VISUAL:-${EDITOR:-${commands[nano]:+nano}}}
        print -s -- "$line"
        BUFFER="${ed:-vi} ${(q)mout}"
        zle $_zshai_accept
        return
      fi
      if (( mst != 3 )); then
        print -s -- "$line"
        BUFFER=''
        zle -I
        [[ -n $mout ]] && print -r -- "$mout"   # $(...) dropped the final newline
        _zshai_settings
        _zshai_colors
        return
      fi ;;
  esac
  if [[ $line != [[:space:]]#(zshai|zai):* ]] && ! _zshai_route "$line"; then
    zle $_zshai_accept
    return
  fi
  _zshai_ran=''
  {
    _zshai_turn "$line"
  } always {
    if (( TRY_BLOCK_ERROR )); then
      # something failed inside zshai: never leave a command behind to run
      TRY_BLOCK_ERROR=0
      [[ -z $_zshai_ran ]] && BUFFER=''
      print -r -- $'\n  zshai: intern feil / internal error (nothing was run)'
    fi
  }
}
zle -N accept-line zshai-accept-line

# After each command: remember it and its exit status for the next request's context,
# and report the exit status of AI commands to the helper's log.
_zshai_precmd() {
  local st=$?
  # stop copying stderr (see _zshai_preexec); a failed zshai command may get a fix
  if [[ -n $_zshai_errfd ]]; then
    exec 2>&$_zshai_errfd
    exec {_zshai_errfd}>&-
    _zshai_errfd=''
    # let tee write its last bytes and exit now: when it exits later, zle redraws the
    # prompt in the middle of a question. The .done file appears when tee has ended
    # (it may not even have started yet when a quick command fails at once).
    local i
    for i in {1..50}; do
      [[ -e $_zshai_errfile.done ]] && break
      zselect -t 1 2>/dev/null || sleep 0.01
    done
    # also on exit 0: the helper looks for error messages ($(( $(date …) )) hides a failure)
    if [[ -n $_zshai_turn_id && -s $_zshai_errfile ]]; then
      _zshai_fixq=( "$_zshai_turn_id" "$st" "$_zshai_errfile" )
    fi
  fi
  if [[ -n $_zshai_show ]]; then
    print -s -- "$_zshai_show"
    _zshai_show=''
    print -n -- $'\e[1A\e[2K\r'   # erase the empty prompt line we accepted to get here
  fi
  _zshai_last_exit=$st
  _zshai_last_cmd=${history[$((HISTCMD-1))]-}
  if [[ -n $_zshai_ran && -n $_zshai_turn_id ]]; then
    if (( $#_zshai_fixq )); then
      # a fix question follows: a background job ending during it makes zle redraw the prompt
      "$ZSHAI_BIN" done "$_zshai_turn_id" "$st" &>/dev/null
    else
      "$ZSHAI_BIN" done "$_zshai_turn_id" "$st" &>/dev/null &!
    fi
  fi
  if [[ -n $_zshai_pending ]]; then
    (( st == 0 )) && _zshai_resume=$_zshai_pending
    _zshai_pending=''
    _zshai_dumped=0
  fi
  _zshai_ran='' _zshai_turn_id=''
  # a zshai command failed: offer a fix now, before the prompt is drawn (outside zle the
  # terminal is plain; inside line-init zle redraws the prompt mid-question). The fixed
  # command always asks first; if confirmed, line-init runs it.
  if (( $#_zshai_fixq )); then
    local -a q=( $_zshai_fixq )
    _zshai_fixq=()
    {
      _zshai_handle ask --cwd "$PWD" --fix-of "$q[1]" --fail-exit "$q[2]" --stderr "$q[3]"
    } always {
      if (( TRY_BLOCK_ERROR )); then
        TRY_BLOCK_ERROR=0
        _zshai_autorun='' _zshai_ran=''
        print -r -- $'\n  zshai: intern feil / internal error (nothing was run)'
      fi
    }
  fi
  return $st
}
add-zsh-hook precmd _zshai_precmd

# While a command zshai wrote runs, copy its stderr to a file (it still shows on the
# terminal), so a failure can be sent back to the model. Only zshai's own commands;
# the user's commands are never touched. tee ignores Ctrl-C so stderr stays open.
_zshai_preexec() {
  [[ $_zshai_fix == 1 && -n $_zshai_ran && -n $_zshai_turn_id ]] || return
  _zshai_errfile=$_zshai_cache/stderr.$$
  [[ -d $_zshai_cache ]] || mkdir -p -m 700 $_zshai_cache
  : >| $_zshai_errfile
  rm -f -- $_zshai_errfile.done
  exec {_zshai_errfd}>&2   # first: the fd number must exist before the tee below is started
  # sh -c marks the end of tee with a .done file; precmd waits for it
  # (stdout is pointed at the terminal in zsh first: dash can't name fds above 9)
  exec 2> >(trap '' INT; exec >&$_zshai_errfd; exec sh -c 'tee -a -- "$1"; : > "$1.done"' sh $_zshai_errfile)
}
add-zsh-hook preexec _zshai_preexec

# After an install that zshai suggested, finish the original request.
_zshai_line_init() {
  # a fixed command the user confirmed in precmd
  if [[ -n $_zshai_autorun ]]; then
    BUFFER=$_zshai_autorun
    CURSOR=$#BUFFER
    _zshai_autorun=''
    zle $_zshai_accept
    return
  fi
  [[ -z $_zshai_resume ]] && return
  local req=$_zshai_resume
  _zshai_resume=''
  {
    _zshai_turn "?$req"
  } always {
    if (( TRY_BLOCK_ERROR )); then
      TRY_BLOCK_ERROR=0
      [[ -z $_zshai_ran ]] && BUFFER=''
    fi
  }
}
add-zle-hook-widget line-init _zshai_line_init

# Optional status for the right prompt (robbyrussell has none):  RPROMPT='$(zshai_prompt_info)'
zshai_prompt_info() {
  [[ -x $ZSHAI_BIN ]] && print -n "%F{${ZSHAI_COLORS[note]}}✦%f"
}
