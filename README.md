# zshai

**Natural language for zsh.** zshai is an oh-my-zsh plugin plus a small Go helper. You type what
you want in plain English or Norwegian after `zshai:` (or the short `zai:`). zshai asks a language
model for a shell command, checks the command, and then runs it, explains it or asks you first,
depending on how risky it is. Every other line you type is an ordinary shell command, and your
theme, aliases and history stay as they are.

It works on **macOS and Linux** (Homebrew, apt and pacman; dnf is best-effort). It knows the
common Unix tools (`jq`, `rg`, `fd`, `ffmpeg`, …), git and the Azure CLI. It talks to **your own
model** through any OpenAI-compatible server, such as LM Studio, llama.cpp or Ollama. Nothing is
sent anywhere else.

```
➜  data zshai: how many rows are in sales.csv?
➜  data tail -n +2 sales.csv | wc -l
12482
➜  data zai: how do I list only directories?
  ▸ ls -d */
    ls -d */ shows only the entries that match */, that is, the directories.
  (not run; ↑ brings the command back)
➜  data zshai: delete the log files older than a week
  ▸ find ./logs -name '*.log' -mtime +7 -delete   [move/delete]
  Delete 2 files (117.2 KB, changed Nov 1–3) in logs? They go to the trash.  yes / no / show / edit > y
  Moved 2 files (117.2 KB) to the trash. "zshai: undo" puts them back.
```

> **Built with Claude.** This project was designed and written with the help of
> [Claude](https://claude.ai) (Anthropic), working through [Claude Code](https://claude.com/claude-code):
> the plan, the Go helper, the plugin and the tests were developed in conversation, and tested
> live on macOS and Linux.

## What it does
- **Read-only commands run at once.** They appear on the prompt line and land in your history.
- **Changes ask first,** with one key: `yes / no / show / edit` (`ja / nei / vis / endre` in Norwegian).
  Moves and deletes ask with real numbers from a dry run. `edit` puts the command on the line, and it
  runs only when you press Enter.
- **Deletes go to the trash** when zshai can do them itself; `zshai: undo` reverses them.
  Dangerous commands need `YES` typed out.
- **Questions don't run anything.** "how do I …", "explain …" and "what does … do" print the command
  with an explanation. ↑ brings it back if you want to run it.
- **Commands are checked before you see them:** syntax (`zsh -n`), that the command exists, GNU vs
  BSD flags (macOS), git subcommands, Azure CLI commands and options (an embedded catalog), `jq`
  filters and SQLite queries against your real files, and read-only pipelines are tried once.
- **git:** before anything that can lose work, zshai saves `refs/zshai/backup/<id>`.
- **Missing tools:** zshai offers the right install command for your package manager.
- **Failed commands:** when a command zshai wrote fails, zshai offers a corrected one (it asks first).
- **A local log** of requests and results in SQLite (`zshai: last 5`, `zshai: stats`), which you can
  turn off.

## Requirements
- zsh 5.8 or newer, and [oh-my-zsh](https://ohmyz.sh) (the plugin can also be sourced directly).
- An OpenAI-compatible model server you control. It was developed with
  [LM Studio](https://lmstudio.ai) serving `qwen/qwen3-coder-30b`; a code model of similar size
  is recommended.
- Go 1.27 or newer to build from source, or a prebuilt binary from `make dist`.

## Install
### From source
```zsh
git clone https://github.com/MariusEng/zshai.git
cd zshai
make install        # builds ./zshai, installs ~/.local/bin/zshai and the plugin into
                    # ${ZSH_CUSTOM:-~/.oh-my-zsh/custom}/plugins/zshai
```
`make` uses the Go toolchain in `.tools/go` if you have put one there, and otherwise the `go` on
your `PATH`. Make sure `~/.local/bin` is on your `PATH`.

Then add `zshai` to the plugins in `~/.zshrc` and start a new shell:
```zsh
plugins=(git zshai)
```
```zsh
exec zsh
```

### On another machine
`make dist` cross-builds `dist/zshai-{darwin,linux}-{arm64,amd64}` and `dist/zshai.plugin.zsh`.
Copy the binary for that machine to `~/.local/bin/zshai` and the plugin file to
`~/.oh-my-zsh/custom/plugins/zshai/zshai.plugin.zsh`. Use both from the same build, because
`zshai: status` warns when they don't match.

### Without oh-my-zsh
```zsh
source /path/to/zshai.plugin.zsh     # in ~/.zshrc, with the zshai helper on PATH
```

## Configure
Settings live in `~/.config/zshai/config.json`. `zshai: config` shows the file and its contents.
The important ones are the model server and the model:
```json
{
  "server": "http://localhost:1234/v1",
  "model": "qwen/qwen3-coder-30b",
  "language": "en",
  "theme": "auto"
}
```
| Key | Meaning |
|---|---|
| `server`, `model` | OpenAI-compatible base URL and model id (default: LM Studio on localhost) |
| `apiKey` | Only if your server needs one |
| `language` | `en` or `no` (also `zshai: language english|norsk`) |
| `theme` | `auto`, `gold`, `light`, `classic`, `solarized`, `nord`, `dracula`, `mono` (also `zshai: theme`) |
| `packageManager` | `auto`, `apt`, `pacman`, `brew`, `dnf` |
| `suggestInstall` | `ask`, `mention` or `never` |
| `fixAttempts` | How many corrected commands to offer after a failure |
| `protectedSubscriptions` | Azure subscriptions where every change counts as dangerous |
| `log` | `false` turns the SQLite log off |

## Use
At the prompt:
- `zshai: <what you want>` or `zai: …` (or `?text`) sends a request. Every other line is a normal shell command.
- `zshai: auto on` also sends plain-language lines without the prefix; `zshai: auto off` turns that off.
- zshai's own commands: `zshai: help`, `zshai: undo`, `zshai: status`, `zshai: stats`, `zshai: last 5`,
  `zshai: language english|norsk`, `zshai: theme`, `zshai: config`, `zshai: remember …`
  (Norwegian forms work too: `hjelp`, `angre`, `siste 5`, `språk`, `tema`, `husk`).
- Colours: `zshai: theme` shows every theme and picks one with a key. `auto` asks the terminal for its
  background colour; `ZSHAI_BG=dark|light` in `~/.zshrc` skips that check. Only zshai's own lines change,
  never your prompt.

The helper also works on its own:
```zsh
zshai check -- 'rm -r build'    # risk, validation and dry run for a command
zshai log 10                    # the latest requests; zshai log --id <id> for one in full
zshai --version
```

## Privacy
- Requests go only to the model server in your config.
- The context sent with a request holds your OS, installed tools, the current folder's listing, git
  status, `az` account name and ssh host *names* (never addresses or keys). For files you name, it
  includes their shape (JSON keys, CSV header and first lines). Files that look like secrets (`.env`,
  keys, anything named token, password or credential) are never read.
- The log is local: `~/.local/state/zshai/zshai.db`.

## Development
```zsh
make build            # ./zshai
make test             # go vet + unit tests
make dist             # cross-builds into dist/ (version from VERSION)
PYTHONPATH=<dir with pyte> python3 tests/pty/run.py   # end-to-end terminal tests (real zsh, fake model)
python3 tests/practice/mkfiles.py DIR && zsh tests/practice/run.zsh DIR tests/practice/files.txt
                      # live practice against your real model, with isolated config and log
make catalog          # re-extract the Azure CLI catalog from this machine's az
```
`scripts/rank-history.zsh` ranks your own zsh history by command name, to see which tools matter.
Its output is personal and is ignored by git.

| Path | What |
|---|---|
| `plugin/zshai.plugin.zsh` | The plugin: Enter widget, router, inline questions, history |
| `cmd/zshai/` | The `zshai` helper: ask / confirm / undo / meta / check / route |
| `internal/shell` | zsh parser (mvdan.cc/sh): simple commands, aliases, sudo/xargs/ssh unwrapped |
| `internal/risk` | Deterministic risk rules: read / change / install / delete / danger |
| `internal/validate` | Syntax, command exists, GNU/BSD flag traps, jq/SQLite checks, trial runs |
| `internal/dryrun` | What a move or delete would touch |
| `internal/azcat` | Azure CLI catalog (6,105 commands, options, examples), embedded |
| `internal/contextinfo` | The context sent to the model |
| `internal/{llm,msg,theme,store,trash,journal,router,config,tools}` | Model client, texts, themes, log, trash, undo, routing, config, tool list |
| `tests/pty/` | End-to-end terminal tests |
| `tests/practice/` | Live practice set: sample files and requests |

[PLAN.md](PLAN.md) has the full design, decisions and status.

## License
MIT, see [LICENSE](LICENSE).
