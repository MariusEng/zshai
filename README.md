# zshai

**Natural language for zsh.** zshai is an oh-my-zsh plugin plus a small Go helper. You type what
you want in plain English or Norwegian after `zshai:` (or the short `zai:`). zshai asks a language
model for a shell command, checks the command, and then runs it, explains it or asks you first,
depending on how risky it is. Every other line you type is an ordinary shell command, and your
theme, aliases and history stay as they are.

It works on **macOS and Linux** (Homebrew, apt and pacman; dnf is best-effort). It knows the
common Unix tools (`jq`, `rg`, `fd`, `ffmpeg`, …), git and the Azure CLI. It runs **its own small
model on your machine** (`zshai-mini`, served by a bundled llama.cpp), or talks to any
OpenAI-compatible server you choose, such as LM Studio or Ollama. Nothing is sent anywhere else.

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
- A model: the built-in `zshai-mini` (see below), or an OpenAI-compatible server you control.
  zshai was developed with [LM Studio](https://lmstudio.ai) serving `qwen/qwen3-coder-30b`, which
  is also the teacher `zshai-mini` was trained from.
- Go 1.27 or newer to build from source.

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

### The built-in model
`zshai-mini` is a fine-tuned Qwen2.5-Coder-0.5B, quantized to GGUF (about 400 MB). zshai runs it
with llama.cpp's `llama-server`, which it starts on 127.0.0.1:8093 on the first request and leaves
running (`zshai model stop` ends it). On a Mac it uses the GPU (Metal); on Linux the CPU is enough.

The model weights are too big for git; a release bundle has everything for one platform:
```zsh
tar xzf zshai-<version>-darwin-arm64.tar.gz
zshai-<version>-darwin-arm64/install.sh    # ~/.local/bin/zshai, ~/.local/share/zshai/{llama-server,models/}, the plugin
```
From source, with a model file in `models/`:
```zsh
make install          # zshai + plugin
make install-model    # builds llama-server for this machine (needs cmake and a C++ compiler) and installs it with the model
zshai model           # where the model is and whether it is running
```
`"server": "local"` (the default) uses it. Set `server` and `model` in the config to use another
model server instead. How the model is made: [Training zshai-mini](#training-zshai-mini).

### Without oh-my-zsh
```zsh
source /path/to/zshai.plugin.zsh     # in ~/.zshrc, with the zshai helper on PATH
```

## Configure
Settings live in `~/.config/zshai/config.json`. `zshai: config` shows the file and its contents.
The important ones are the model server and the model:
```json
{
  "server": "local",
  "language": "en",
  "theme": "auto"
}
```
or, with your own model server:
```json
{
  "server": "http://localhost:1234/v1",
  "model": "qwen/qwen3-coder-30b"
}
```
| Key | Meaning |
|---|---|
| `server`, `model` | `local` (default: the built-in zshai-mini), or an OpenAI-compatible base URL and model id |
| `apiKey` | Only if your server needs one |
| `language` | `en` or `no` (also `zshai: language english|norsk`) |
| `theme` | `auto`, `gold`, `light`, `classic`, `solarized`, `nord`, `dracula`, `mono` (also `zshai: theme`) |
| `packageManager` | `auto`, `apt`, `pacman`, `brew`, `dnf` |
| `suggestInstall` | `ask`, `mention` or `never` |
| `fixAttempts` | How many corrected commands to offer after a failure |
| `protectedSubscriptions` | Azure subscriptions where every change counts as dangerous |
| `log` | `false` turns the SQLite log off |

### The system prompt
`zshai: prompt` (or `zshai: system prompt`) opens the system prompt in your `$EDITOR`. It is the file
`~/.config/zshai/prompt.txt`, created from this default the first time:
```
{zshai}

About this machine and user:
- OS: {os} on {arch} ({flavour}); shell: {shell}; packages: {packages}.
- Write questions and explanations in {language}.
- User: {user} on {host}; home folder {home}.
```
- `{zshai}` is zshai's built-in rules and answer format. Keep it, and add your own lines below,
  such as "prefer rg over grep" or "my projects are in ~/git". Without `{zshai}` the file replaces
  the whole prompt, and then it must still ask for zshai's answer format.
- The other placeholders are filled in for this machine: `{os} {arch} {flavour} {shell} {packages}
  {language} {user} {host} {home}`. Lines starting with `#` are comments.
- `zshai: prompt show` prints the prompt exactly as the model gets it, with warnings for unknown
  placeholders. `zshai: prompt reset` restores the default.
- The prompt stays the same between requests, so the model server can reuse its cache.

## Use
At the prompt:
- `zshai: <what you want>` or `zai: …` (or `?text`) sends a request. Every other line is a normal shell command.
- `zshai: auto on` also sends plain-language lines without the prefix; `zshai: auto off` turns that off.
- zshai's own commands: `zshai: help`, `zshai: undo`, `zshai: status`, `zshai: stats`, `zshai: last 5`,
  `zshai: language english|norsk`, `zshai: theme`, `zshai: config`, `zshai: prompt`, `zshai: remember …`
  (Norwegian forms work too: `hjelp`, `angre`, `siste 5`, `språk`, `tema`, `husk`).
- Colours: `zshai: theme` shows every theme and picks one with a key. `auto` asks the terminal for its
  background colour; `ZSHAI_BG=dark|light` in `~/.zshrc` skips that check. Only zshai's own lines change,
  never your prompt.

The helper also works on its own:
```zsh
zshai check -- 'rm -r build'    # risk, validation and dry run for a command
zshai log 10                    # the latest requests; zshai log --id <id> for one in full
zshai model [start|stop]        # the built-in model's server
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

## Training zshai-mini
zshai-mini is distilled from a bigger model (the teacher, here `qwen3-coder-30b` in LM Studio)
through zshai itself, so it learns zshai's context, rules and answer format:

1. **Fixture homes** (`datagen/fixtures.py`): fake `$HOME`s with documents, pictures, downloads,
   git projects, CSV/JSON/JSON Lines/TSV/YAML files and logs. Names, counts and dates vary per seed.
2. **Requests** (`datagen/gen_requests.py`): the teacher writes what a user would type, for 29
   categories (files, sizes, CSV/JSON, logs, git, processes, network, packages, media, "how do I",
   "explain", vague requests, risky requests …) in English and Norwegian.
3. **Answers** (`datagen/run_teacher.py`): each request goes through `zshai ask` with the teacher,
   with all of zshai's checks and repair rounds, in a fixture home on **macOS and on Linux** (BSD and
   GNU tools), sometimes with the optional tools hidden from `PATH` (so it learns `n: <tool>`).
   Answers are executed (read commands in the fixture, changes in a throwaway copy) and dropped if they
   fail. Vague requests are judged by the teacher and become clarifying questions.
4. **Dataset** (`datagen/build_dataset.py`): the first prompt zshai sent and the final, checked
   answer, with the default system prompt (`zshai: prompt`) rendered for that machine.
5. **Training** (`train/run-mac.sh`, on an Apple Silicon Mac with mlx-lm): LoRA on all layers of
   Qwen2.5-Coder-0.5B-Instruct (`train/lora.yaml`), fused, converted to GGUF with llama.cpp and
   quantized to Q4_K_M.
6. **Evaluation** (`datagen/bench.jsonl`, `datagen/compare.py`): 48 hand-written requests in two
   fixture homes never used for training, run through zshai with the student and the teacher.
   *valid* = passed zshai's checks and ran; *match* = did what the teacher did (same output, same
   files afterwards, or same kind of answer).

Results (Linux CPU numbers on an 8-core Skylake; Mac on an M5 Pro with Metal):

| Model | Size | Data | Bench (48): valid / match | Held-out (181): valid / match / first try | Time per request |
|---|---|---|---|---|---|
| teacher `qwen3-coder-30b` | ~17 GB | — | 0.98 kept | 0.85 kept | ~2–5 s (on the Mac, over the network) |
| untuned Qwen2.5-Coder-0.5B | 398 MB | — | not run: failed the answer format on both requests tried | — | — |
| zshai-mini 0.5B v0b | 398 MB | 343 | 0.83 / 0.47 | — | — |
| zshai-mini 1.5B v1 | 986 MB | 1,523 | 0.89 / 0.64 | — | Linux 3.4 s |
| zshai-mini 0.5B v1 | 398 MB | 1,523 | 0.94 / 0.68 | 0.91 / 0.63 / 0.89 | Linux 1.3 s |
| **zshai-mini 0.5B v2** (shipped) | **398 MB** | **2,821** | **0.94 / 0.66** | **0.93 / 0.65 / 0.92** | **Linux 1.5 s, Mac 0.3 s** |

Times are a whole `zshai ask` (context, model, checks) with the model warm; the first request after a
start adds 1–5 s to load it. The 1.5B was not better on this data, so the 0.5B ships. 4-bit (Q4_K_M)
scored the same as 8-bit (Q8_0, 531 MB). "match" is strict: many non-matching answers are different
but correct commands (`grep -r` vs `find -exec grep`).

## License
MIT, see [LICENSE](LICENSE).
