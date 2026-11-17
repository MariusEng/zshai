# zshai — plan

## Round 2: model sizes and choice (started 2026-11-16)
Real use on the Mac showed zshai-mini 0.5B can't write files with content, and answers zshai's
feedback ("That reply failed zshai's checks") with unrelated trained recipes: it never saw a repair
round. Decision (user, 2026-11-16): train **1.5B, 3B and 7B** on new data, measure, ship them as
separate downloads and let users choose; the 0.5B is dropped if the measurements agree.
- **Data:** new categories `create` (files with content) and `fmt` (PowerShell-style tables/lists,
  `zshai fmt`); csv/json re-run with the new rules (round 1 taught `jq … | column`); **repair
  examples** from real student failures (0.5B v2 on Linux, 1.5B v1 on macOS) + zshai's feedback →
  the teacher's verified answer (`datagen/gen_repairs.py`). Filters drop table/list answers without
  `zshai fmt` and empty-file answers to "create … with …" (`build_dataset.py`).
- **Training:** `train/lora-{1.5b,3b,7b}.yaml`, same recipe, lr 3e-5 / 2e-5 / 1.5e-5.
- **Eval:** bench (48) + held-out (214) + new held-out create/fmt (homes 9–10), teacher references
  re-run with zshai 0.4.1; quality on the Mac, speed on Mac and Linux CPU.
- **Shipping:** program bundles without a model; models in one release (`models-v3`), anything over
  GitHub's 2 GiB/file limit split (`make models`); `zshai model install|use|list|remove`,
  `zai: model [size]`, `install.sh --model`, config `localModel`.
- **Result (2026-11-17, shipped in 0.5.0):** 3B v4 (= 3B v3 + table/list examples from
  `datagen/gen_fmt.py`) valid 0.96 / first try 0.95, default on Apple Silicon; 1.5B v3 valid 0.94 /
  first try 0.90, default elsewhere. 0.5B dropped (0.90 / 0.87). 7B dropped: undertrained at lr 1.5e-5
  (val loss 0.36 vs 0.19 for the 3B; valid 0.86). Numbers in README "Training zshai-mini".

## Status (updated 2026-11-15)
**zshai-mini v2 shipped as the default model (2026-11-15).** Phases 0/2/3 in a first, small form:
- **Data:** `datagen/` (fixture homes, teacher-written requests in 29 categories × en/no, answers through
  `zshai ask` with the teacher on macOS *and* Linux, executed in the fixture or a copy, vague requests
  judged into clarifying questions). 1,668 requests → 2,821 kept examples (both OSes).
- **Model:** Qwen2.5-Coder-0.5B-Instruct, LoRA on all layers (lr 3e-5; 1e-4 diverged), 1,000 steps, fused,
  GGUF Q4_K_M (398 MB). Bench (48 hand-written, unseen homes): valid 0.94, match 0.66; held-out (181):
  valid 0.93, match 0.65, first try 0.92. The 1.5B was not better (0.89/0.64) at 2.5× the size.
  Q8_0 = Q4_K_M. Full numbers in README "Training zshai-mini".
- **Runtime:** bundled llama.cpp `llama-server` (b12287; Linux built in an Ubuntu 22.04 container →
  glibc 2.34, AVX2; macOS Metal, shaders embedded), started by `zshai` on 127.0.0.1:8093 on first use
  (`internal/localmodel`, `zshai model`). Warm request: Mac M5 Pro 0.3 s, Linux Skylake CPU 1.5 s.
  `"server": "local"` is the default. `make llama`, `make install-model`, `make bundle` (→ ~380 MB tarball
  with `install.sh`).
- **System prompt:** `~/.config/zshai/prompt.txt` (`zshai: prompt` edits, `prompt show`, `prompt reset`);
  `{zshai}` + machine placeholders. zshai-mini gets `llm.SystemMini` as `{zshai}`; training uses the
  same rendered default.
- Vague requests: v2 asks on 7 of 8 held-out ones (teacher: 7 of 8), after weighting asks ×4.
- **Next:** more data for the weak spots (JSON tables, git diff/log choice, tool choice),
  real-usage examples from the log, held-out on macOS, Linux arm64 build,
  hosting the model files (GitHub release).

**Phase 1 (MVP) started 2026-11-14.** zshai is psai's sibling for zsh. Wherever this plan says "as in psai", it means `~/git/powerai/PLAN.md` and the code in `~/git/powerai`.

**Built so far (Go 1.27.4 in `.tools/go`, mvdan.cc/sh v3.14.1 with its zsh dialect):**
- **Plugin** (`plugin/zshai.plugin.zsh`):
  - wraps Enter (keeping any existing `accept-line` wrapper) and routes lines; `?` forces zshai, `,` forces the shell, `!` lines are left to zsh;
  - keeps the typed request on screen and in history;
  - read-only commands run on the prompt line; other commands get inline one-key questions, and dangerous ones need `JA`/`YES` typed;
  - the turn after an install resumes the original request;
  - the exit status is preserved, so robbyrussell's arrow stays truthful (tested).
- **Helper** (`zshai`):
  - context (OS and GNU/BSD flavour, including uutils on Ubuntu 26.04; package manager; installed common tools; cwd listing v2 with CSV headers; git; az from files; ssh hosts);
  - model client with the line format (§4.3) and up to 2 repair rounds;
  - validator, risk rules and dry runs;
  - trash on Linux (freedesktop) and macOS (Finder);
  - undo journal for trash, moves and git backups;
  - `zshai:` commands.
- **Azure catalog:** az 2.91.0, 6,105 commands, 39,966 options, help examples for 3,896 commands; embedded at 281 KB.
- **Invocation (changed 2026-11-14, user's request):** `zshai: <request>` is the way to call zshai, so it is always clear when it runs. Words after `zshai:` that match zshai's own commands (`hjelp`, `angre`, `status`, …) run those; anything else is a request. `zai:` is the short form (added 2026-11-14), and `?text` also works. Auto-detection of plain-language lines (§2.1) is **off by default**; `zshai: auto på` turns it on (`autoDetect` in the config).
- **Show / explain mode (2026-11-14):** "how do I …", "hvordan …", "forklar …", "explain …", "what does … do", "hva gjør …" print the command and an explanation and **never run it**. The helper detects these phrases itself (`llm.WantsShow`) on top of the model's `m: show`. The command goes into history so ↑ brings it back (not for danger-level commands).
- **Themes (2026-11-14):** `zshai: tema` previews 7 themes plus `auto` and picks one with a key. Roles: cmd, explain (gold on dark), ask, note, danger, and the key letters yes/no/show/edit (the first letters of `ja / nei / vis / endre` in green/red/gold/blue; no brackets). `auto` detects the background once per shell with OSC 11 (COLORFGBG first, `ZSHAI_BG` overrides); no answer within 0.3 s means dark.
- **awk check (2026-11-14):** `print $1 > 0 ? …` (redirect, not comparison) is caught, and the local awk parses the program when that is side-effect free (no BEGIN/END, no `@`). Found on the Mac: BWK awk rejected a generated command.
- **Deleted cwd (2026-11-14):** if the folder was replaced, the plugin steps into the new one; if it is gone, zshai says so instead of running anything.
- **Tests:**
  - risk rules: 100 cases;
  - router: ~110 lines, including the Mac history patterns;
  - answer parser (runaway answers with several alternatives are cut after the first command), awk checks;
  - themes (every role in every theme, key colouring);
  - **31 end-to-end terminal scenarios** (including log rows, fixes, themes, `zai:`) (`tests/pty/run.py`: real zsh + oh-my-zsh + robbyrussell + git, fake model), all passing.
- **First live run** against llmhost's `qwen3-coder-30b`: 0.4–0.7 s per request when warm (5 s cold). Six en/no requests came out right, covering a row count, a delete with a dry run, an ffmpeg install suggestion and `az group list`.

**Bug found and fixed during testing:** the first plugin version put the unconfirmed command into the edit line before asking. When the confirm code hit a zsh error, zsh ran the command anyway; it deleted scratch test files. The plugin now never puts an unconfirmed command in `$BUFFER`, runs every turn inside an `always` block that clears the line on error, and the scenario `internal-error-runs-nothing` guards this.

**Different from the plan so far (on purpose, for now):**
- The plugin starts the helper per request (~5 ms) instead of a long-running daemon over `zsocket`. A daemon only pays off once context caching matters.
- The SQLite log lives in `~/.local/state/zshai/zshai.db` (XDG state dir), not `~/.config`. Effects come from zshai's own actions (trash, moves, git backups, undo), not yet from FSEvents/inotify watchers.
- Finder trash and BSD flag traps are written but **not yet tested on a Mac**.

**Done 2026-11-14 (afternoon):**
- **SQLite log (§5.1)** `~/.local/state/zshai/zshai.db`, psai's tables `turns` / `responses` / `effects` plus `uid`, `mode`, `exit_code`, `problems`, `repairs`, `fix_of`. Every model call is stored with its prompt and raw reply (training data). Outcomes come from the plugin too: cancelled (`n`), edited (`e`), exit status after running. An old `turns.jsonl` is imported once. `zshai log [n] [--changed]`, `zshai log --id <id>`; `zshai: siste` reads the database.
- **Fix-after-failure:** while a command zshai wrote runs, its stderr is copied to a file (tee; it still shows on the terminal; the user's own commands are never touched). If it fails with error output, zshai sends the command, exit code and error to the model and offers the corrected command, which **always asks first** (`Kjøre den rettede kommandoen?`). No fix for: no stderr (grep without a match, diff), Ctrl-C/Ctrl-Z/SIGPIPE, a fix of a fix (`fixAttempts`, default 1; 0 turns it off), or the model repeating the failed command. The question is asked from `precmd` (outside zle; inside `line-init` zle redraws the prompt mid-question), and a confirmed fix runs from the next prompt's `line-init`. Known cost: stderr is a pipe for zshai's commands, so tools that only show progress on a terminal (git clone) don't show it.
- **Versioned builds:** `VERSION` file (0.3.0) + git commit + build date via `-ldflags`; `make dist` writes `dist/zshai.plugin.zsh` with the same version. `zshai version`, `zshai: versjon`, `zshai: status` and `zshai: stats` show version, OS/arch (and Rosetta), and warn when plugin and helper versions differ.
- **`zshai: stats` / `statistikk`:** status plus numbers from the log (requests, ran, failed, cancelled, edited, shown, fixes, refused, undone, model time, most used programs).

**Done 2026-11-14 (evening), on the Mac (llmhost, macOS 27.0.1 arm64):** pty suite 31/31 (three runs in a row) with the darwin-arm64 build. Fixed: a race where a quick failing command ended before tee had started (precmd now waits for a `.done` file instead of a pid file); `ZSHAI_TRASH_DIR` so tests never use the real Finder trash; BSD `wc` padding in the tests. Live against qwen3-coder on the Mac: BSD flags are right (`stat -f`, `find -mtime`, `sw_vers`); the validator now catches `ps --sort` on macOS; the model's own `m: show` no longer wins for "vis/show/list <things>" requests (`llm.WantsResult`) or when it gives no explanation. The model trimming `uptime`/`df` output with awk is handled by `validate.Trimmed`: a soft problem (sent back once, accepted if the model keeps it) unless the request asks for a part ("bare", "only", "hvor mange"). Show mode is now decided by the request alone (`llm.WantsShow`, which also covers "hvilken kommando …"/"what's the command …"); zshai tells the model `mode: show|run` instead of letting it pick (it picked show for "vis meg config" and "how long has this machine been running"). `validate.LsTotal` (soft) catches `ls -l… | head -n N`, which loses one entry to the "total" line. Prompt: free memory = `top -l 1 | grep PhysMem`/`vm_stat` on macOS, `free -h` on Linux. After llmhost's update to macOS 27.2.1: pty suite 31/31 (twice), live run 15/15 after adding `validate.YesNo` (soft): a computed "Ja"/"Nei" (`brew outdated | wc -l | awk … ? "Ja" : "Nei"`) is sent back so the model prints the things themselves. From real use on the Mac the same evening:
  - A command that exits 0 but prints errors (`$(( … $(date …) … ))`, `ps … | sed` where only the last command's status counts) now also gets a fix offer: the plugin hands any stderr to the helper, which offers a fix on exit 0 only when the text looks like an error (`usage:`, `illegal option`, `failed conversion`, …). pty: `fix-when-exit-0-but-errors`, `no-fix-for-progress-on-stderr`.
  - BSD `ps`: any long option (`--no-headers`, `--sort`) and the `cmd` column are caught on macOS.
  - Remote commands (soft checks): a written-out home path (`/Users/…`, `/home/…`) or this machine's current folder (`~/git/tv`) inside `ssh host '…'` is sent back ("use ~/… there, or find it"); a name the user put in quotes ("zshai") must appear in the command as a word or path part; `Trimmed` also sees through `ssh host 'df …' | awk`.
  - zshai's own file paths are only added to the context for questions about its config/log/history, not for "the zshai project on server".
  - Prompt facts: uptime in hours from `kern.boottime` / `/proc/uptime`; CSV with quoted fields.
- **Formatting and displaying files (2026-11-14, night).** New live practice set `tests/practice/` (sample JSON array / JSON Lines / object, CSV with `,` and `;`, TSV, log, YAML, Markdown; 33 requests; `run.zsh` asks zshai and runs the read-only answers). First run on llmhost: 10 of 20 wrong; final run: 32 of 32 run answers correct and exit 0, 0 refused. What did it:
  - Context: `named:` files get their shape: JSON array/object/JSON Lines with keys and types in file order (`contextinfo.JSONShape`), CSV delimiter and first lines. Secrets (`.env`, keys, "token", …) are never peeked.
  - Checks that run things (all read-only, short timeouts): `jq` filters on the real input files (with "this file is JSON Lines / already an array" explanations and the exact working table filter); `TryRun` runs a whole pipeline of read-only tools (`jq column sort head tail cut tr uniq wc grep cat nl paste …`, no redirections or `$(…)`), and checks the jq stage in front of `column` prints text rows, not JSON, and no repeated header.
  - Static checks: `~/` inside quotes; `@tsv`/`@csv` without `-r`; jq JSON into `column`; GNU-only awk functions (`asort`, `gensub`, …) when the local awk isn't GNU awk; command names in the wrong case (macOS file systems find `Head`).
  - Soft checks (sent back once): sort after a finished table; CSV sorted without keeping the header; `jq -c` when not asked for compact; "som tabell" without a table.
  - Prompt: recipes for tables (`column -s, -t`, the jq table filter with `tostring`, `-s` for JSON Lines, `sort_by`/`map(select())` before it), real line numbers, count per value, filter-then-tail, keys of a JSON file; two worked examples (CSV sorted with header, JSON array sorted as a table). Later the same evening (your real Mac): `zai: config` meta command; zshai's own files (config, log with the `turns` columns) go into the context when a request mentions zshai, so it no longer invents `~/.zshai/config`; any "vis/show/list …" request runs (unless it asks for "kommandoen"/"the command"); new `sqlite3` check compiles SELECT queries read-only against the real database (also `echo "…" | sqlite3 db`) and sends invented tables/columns or a missing database back to the model with the real schema.

**Next:**
1. Install on the Mac for real use (`~/.local/bin` + oh-my-zsh custom plugin) and use it for a while; brew casks still untested.
2. More dry runs (remote rsync, `brew uninstall` dependents).
3. Phase 0 bench and datagen: port psai's 38 tasks; az static checker using the catalog's examples.
4. Effects from FSEvents/inotify for commands zshai runs (§5.1).

`zshai` brings natural language to **zsh** on macOS and Linux. It runs inside your normal zsh session as an **oh-my-zsh plugin**, so your theme, aliases, history and completions stay as they are. You type what you want in English or Norwegian, and zshai writes and runs the command using the basic Unix tools that ship with macOS and Linux. Normal shell commands typed at the prompt run unchanged.

```
➜  data hvor mange rader er det i sales.csv?
  ▸ tail -n +2 sales.csv | wc -l                                       [lese, kjørt]
  12482

➜  Pictures lag en mappe for de nyeste bildene mine og flytt dem dit
  Skal jeg flytte 37 bilder (412 MB, endret 3.–10. nov) fra ~/Pictures til ~/Pictures/2026-11-10?
  ja / nei / vis kommando / endre >
```
(`➜  data` is your **robbyrussell** prompt. zshai never changes it.)

## 0. Decisions

| # | Decision | Status |
|---|---|---|
| Platforms | **macOS and Linux, equal targets**: both are tested for every release and the bench score is the lower of the two. Linux means **Ubuntu LTS (apt), the reference, and Arch (pacman)**; Debian works through apt. Fedora/dnf is best-effort only (not tested, not a release gate). **No Snap or Flatpak.** No Windows. | decided 2026-11-14 |
| Form | An **oh-my-zsh plugin** (`zshai.plugin.zsh`, a ZLE widget) plus a small **`zshai` helper binary** that the plugin talks to over a Unix socket. zsh stays the shell; zshai does not host or replace it (unlike psai, which hosts pwsh). The helper is written in **Go**. | decided 2026-11-14 |
| Theme | **Your theme, `robbyrussell`, is kept as-is** (plugins: `git`). zshai prints its lines under the prompt and redraws with `zle reset-prompt`. It adds an optional `zshai_prompt_info` function (like `git_prompt_info`) that the theme *can* use, but nothing is injected unless you choose to. See §1.5. | decided |
| Tools | **Three levels** (§3): (1) the basic tools that ship with macOS/Linux, always available; (2) **~45 common tools** zshai knows well (jq, rg, fd, fzf, ffmpeg, ImageMagick, pandoc, …), used whenever they're installed; (3) any other program on `$PATH`, which zshai may also use. When a task needs a common tool that isn't installed, **zshai suggests installing it** with the right package manager and then finishes the task (§2.5). | decided 2026-11-14 |
| Package managers | **apt** (Ubuntu/Debian), **pacman** (Arch), **Homebrew** (macOS, and Linux if you use it there); dnf is best-effort only; Snap and Flatpak are out of scope. zshai handles both installing tools and ordinary package requests ("is ffmpeg installed?", "update everything", "what takes the most space?") (§2.6). | decided 2026-11-14 |
| git | **First-class:** repo state in the context, every subcommand validated, dry runs and a backup ref before anything that can lose work, `angre` for git too (§2.7). | decided 2026-11-14 |
| Azure CLI | **`az` with all core modules** (az 2.91: 98 top-level groups, 1,399 groups in all, 6,105 commands, 46,508 options) plus installed extensions. Commands and options are validated against a catalog extracted from your installed `az`; risk comes from the verb; deletes ask with real resource counts (§2.8). | decided 2026-11-14 |
| Language | English and Norwegian (Bokmål), as in psai. | as psai |
| Brain | **zshai's own model** (`zshai-mini`), separate from psai's (no shared weights or server), running locally (MLX on Macs, llama.cpp on Linux). llmhost is the workshop (teacher, datagen, training, eval) and an optional runtime escalation. Same split as psai §1.4b. | decided 2026-11-14 |
| Speed | **Answer in under 1 s** for normal requests. | as psai |
| Safety | Move/delete always asks a concrete question built from a dry run; deletes go to the trash; undo journal. | as psai |
| Compute | All LLM work on llmhost; no external tokens. | as psai |

### Facts checked 2026-11-14
- **Linux dev box ready (2026-11-14):** this server (Ubuntu 26.04.1, x86_64) runs zsh 5.9 (`/usr/bin/zsh`, login shell) and oh-my-zsh `6010fb6` (2026-11-09) with `ZSH_THEME="robbyrussell"` and `plugins=(git)`, the same as the Mac.
  - 236 aliases are defined, most of them from the `git` plugin.
  - Options in effect: `autocd autopushd extendedhistory sharehistory histverify histignorespace interactivecomments promptsubst …`. Two of these affect the router (§2.1).
- psai is in `~/git/powerai` (not `~/git/psai`). These pieces can be reused or ported directly:
  - the training pipeline (`train/`, `scripts/llmhost.sh`, `scripts/pipeline.sh`);
  - the datagen structure (`datagen/generate_scenarios.py`, `paraphrase.py`, `ask_examples.py`, `cannot_examples.py`);
  - the bench runner and summarizer (`eval/run_bench.py`, `summarize.py`, `sandbox.py`);
  - the Mac install and MLX model-server scripts (`scripts/mac/`);
  - the designs of the SQLite log, the `psai:` meta commands and the standards.
- psai lessons that carry over:
  - The 3B model at 4-bit with **group size 32** is the sweet spot; group 64 broke the 3B.
  - **Context v2** (one level of folder contents, plus files named in the request found up to 3 levels down) lifted the held-out score from 0.31 to 0.55.
  - **MLX is about 2× faster than llama.cpp on Apple Silicon.**
  - **Small models break JSON** when the script itself contains quotes (§4.3 changes the output format because of this).
  - When the teacher writes both the script and the checker, the two can share the same bug, so two independent solutions must agree.
- One big difference from psai: base models already know bash and Unix tools well, while their PowerShell was weak (the untuned 1.5B scored 0.24 and mostly wrote bash). **The untuned baseline should be much higher here**, so the 1.5B may be enough, which would be faster. Phase 0 measures this.

---

## 1. The plugin and the helper

### 1.1 Pieces
| Piece | Language | Job |
|---|---|---|
| `zshai.plugin.zsh` | zsh | Installed in `$ZSH_CUSTOM/plugins/zshai` and added to `plugins=(… zshai)`. Wraps `accept-line` in a ZLE widget, routes lines (§2), shows results, reads one-key answers (`read -k1`), puts the final command into `BUFFER` and runs it in **your own shell**, so `cd`, `export`, aliases and history all behave normally. Talks to the helper with `zsh/net/socket` (`zsocket`), with no extra process per request. |
| `zshai` helper | **Go** | A long-running daemon started by the plugin on first use. It handles the context builder, model client, validator, risk rules, dry run, trash/undo, SQLite log, `zshai:` meta commands and standards. One static binary per target (`darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`), with millisecond startup. |
| Local model server | MLX (Mac) / llama.cpp (Linux) | Serves **zshai-mini only**, on its own port (8093). It's installed the same way as psai's `scripts/mac/` server but never shared with it, because psai's server on 8092 runs psai's own model. |

Why Go (decided 2026-11-14):
- **`mvdan.cc/sh`** is a mature shell parser and AST (it powers `shfmt`) that also expands words and globs against the real file system. The validator, risk rules and dry run need exactly that, and nothing comparable exists for .NET.
- Pure-Go SQLite (`modernc.org/sqlite`) keeps it a single static binary.
- `os/exec`, `fsnotify` and cross-compiling to all four targets come built in.
- psai's C# isn't reused directly. Most of it is pwsh hosting; the parts worth keeping (Context, Store, Standards, Journal, Messages) are small and get ported.

### 1.2 Running in the user's shell
- AI commands are put into `BUFFER` and run with `zle .accept-line`, exactly as if you had typed them. History shows the real command; with `"historyNote": true` the request is appended as a comment (`… # zshai: hvor mange rader …`).
- Before running, you can press `e` to edit the generated command right in the prompt line.
- **zsh semantics apply** (no word-splitting of unquoted variables, `nomatch` errors on globs that match nothing, oh-my-zsh's aliases such as `ls --color` and `grep --color`). The sandbox (§4.2) therefore runs zsh with oh-my-zsh's default options, not `sh` or `bash`.
- The model writes **portable POSIX/bash-style commands** that also run correctly in zsh. In v1 it avoids zsh-only glob qualifiers (`*(om[1,10])`), because the helper's parser can't dry-run them. This can be revisited later.

### 1.3 Settings
Settings live in `~/.config/zshai/config.json`. The keys are those of psai §1.2:
- `language`, `server`, `model`, `autonomy`, `escalate`, `log`, `fixAttempts`;
- `tools`: `"installed"` (default: shipped tools + common tools + anything else on `$PATH`) or `"shipped"` (only tools that ship with the OS, e.g. for scripts you'll share);
- `suggestInstall`: `"ask"` (default; offer to install a missing common tool), `"mention"` (only name the install command) or `"never"`;
- `packageManager`: `"auto"` (default; brew on macOS, the distro's own manager on Linux) or `"apt"|"pacman"|"brew"|"dnf"`;
- `aur`: `false` (default). When `true`, yay/paru may be used for AUR packages on Arch, always with a warning;
- `historyNote`;
- `colors` (zshai's own line colours; defaults chosen to sit apart from robbyrussell's, see §1.5).

`zshai --lang no` switches the language, as in psai.

### 1.4 `zshai:` commands and standards
These are ported from psai §1.2b and answered by the helper with no model involved: `zshai: hjelp/help`, `vis statistikk/show stats`, `siste kommando`, `siste N`, `logg N`, `feil`, `endringer`, `status`, `språk en|no`, `angre/undo`, `database`, `husk …` / `glem …`, `standarder`.

Standards (date format, sizes, region) work differently from psai, because zsh doesn't own the formatting of other programs' output:
- **Sizes:** the model is trained to use `-h` forms (`ls -lh`, `du -sh`, `df -h`). A command whose result is a single raw byte count (`stat -c %s` / `stat -f %z`, `wc -c`) is shown converted to the user's units, as in psai.
- **Dates:** when the model formats a date, it uses the user's pattern (`date +%d.%m.%Y`). For listings it uses the user's time style where the OS supports it: `ls --time-style=+…` (GNU) and `ls -D …` (BSD).
- zshai's own messages always use the standards.
- Notes go into the context as `notes: …`, as in psai.

### 1.5 Fitting robbyrussell and the `git` plugin
- **The prompt:** robbyrussell draws `➜  <dir> git:(<branch>) ✗`.
  - The arrow is bold green after exit code 0 and bold red otherwise. The directory is cyan, `git:(` is bold blue, the branch is red and the dirty mark `✗` is yellow.
  - There is no right prompt (`RPROMPT`).
- **The arrow must stay truthful:**
  - When zshai runs a command, it runs through `accept-line`, so `$?` and the arrow come from that command.
  - When zshai only answers, asks, or the user says no, the widget **restores the previous `$?`** before redrawing. A cancelled request must not turn the arrow red.
  - A refused or failed AI turn does not fake an exit code either.
- **zshai's own colours** avoid robbyrussell's green, red, cyan and blue:
  - the `▸ command` line is dim/grey (`%F{8}`), with its `[lese, kjørt]` tag in the same grey;
  - questions ("Skal jeg …?") are yellow (`%F{yellow}`), matching robbyrussell's dirty mark;
  - dangerous actions are bold red on their own line, with the `JA`/`YES` prompt;
  - the "tolket som spørsmål" hint is dim italic.
- **Optional status:** `zshai_prompt_info` shows `✦` when the model is loaded and `✦?` while it's thinking. Because robbyrussell has no `RPROMPT`, this can go there (`RPROMPT='$(zshai_prompt_info)'` in `.zshrc`) without touching the theme. It is off by default.
- **The `git` plugin** adds ~180 short aliases (`g`, `gst`, `gco`, `gp`, `grhh`, `gpf`, …).
  - The router sees them through `whence`, so they always count as shell commands.
  - The risk rules resolve them before classifying (§2.2), so `grhh` is treated as `git reset --hard`.
- **zshai won't generate** commands built on those aliases: the model writes plain `git …`, which is clearer in history and in the `▸` line.
- **Accept-line wrapping:** robbyrussell + `git` wrap nothing, so there's no plugin clash today. zshai still wraps whatever `accept-line` is current at load time, in case you later add zsh-autosuggestions or zsh-syntax-highlighting.

### 1.6 Hardware and runtime
- **Macs:** MLX first, measured against Core ML, as psai §1.4b decided.
- **Linux:** llama.cpp (Vulkan/CUDA if present, otherwise CPU).
- `zshai --devices` reuses psai's detection logic, ported to Go.
- Windows and NPUs are out of scope.

---

## 2. Request flow

```
 Enter ──► zshai accept-line widget (before zsh parses the line, so "what's here?" doesn't hang at quote>)
              │
            Router ── shell command? ──────────────────────────────────────► zle .accept-line (unchanged)
              │ natural language (or prefix `?`; `,` forces shell)
              ▼
            helper: Cache → Context (OS, GNU/BSD flavour, package manager, installed common tools, zsh version,
              │                    cwd, listing v2, named files, ssh host names, last command + exit code, notes) → local model
              ▼
            Validator ── zsh -n syntax check; mvdan AST; every command in the catalog for this OS;
              │           every flag valid for *this* OS's version of the tool (§3); 1 repair round
              ▼
            needs a missing tool? → install question (§2.5) → install → ask the model again
              ▼
            Risk (AST rules, aliases resolved) → Dry run for move/delete/remove → Confirm (one key)
              ▼
            BUFFER=cmd; zle .accept-line ── on non-zero exit: stderr tail → model → fix (≤ fixAttempts)
              ▼
            Log turn (SQLite) + effects
```

### 2.1 Router (the hard part in zsh)
**As built:** `zshai: …` (or `?…`) is the default and only trigger. The rules below apply when the user turns on auto-detection (`zshai: auto på`).

In psai, PowerShell syntax rarely looked like English. In a Unix shell it often does: *"find all pdfs bigger than 10 MB"*, *"sort this csv by date"*, *"kill the process using port 3000"*. Routing rules, in order:
1. `?…` goes to the AI and `,…` forces the shell.
   - **Not `!` as in psai:** zsh uses `!` for history expansion (`!!`, `!$`, `!git`), and with `histverify` it shows the expanded line first. zshai leaves all `!` lines to zsh.
   - **Lines starting with a space:** with `histignorespace`, these stay out of history. zshai still routes them, and AI turns started this way aren't written to history either.
2. If the first word isn't a command, alias, function, builtin, path or **directory name** (with `autocd`, a lone `Pictures` means `cd Pictures`) (checked with `whence`, which takes zero time inside the widget), the line goes to the AI. This covers all Norwegian (`finn`, `lag`, `flytt`, `hvor`, `vis`) and most English.
3. If the first word *is* a command, the line goes to the AI only if it **looks like prose**: no flags, quotes, pipes, redirections or `$`; at least 2 stopwords or question words (`all`, `the`, `my`, `bigger`, `than`, `that`, `using`, …); and arguments that aren't existing paths. zshai then shows a hint, *"↳ tolket som spørsmål (`,` foran for å kjøre som kommando)"*.
4. Everything else goes to the shell.

The router gets its own offline test set: ~500 real shell lines from history plus ~500 requests, en + no. The shell lines include the `git` plugin's aliases (`gst`, `gco main`, `gp`). **Target: 0 shell commands sent to the AI** (that is the annoying mistake) and ≥ 97% of requests sent to the AI.

### 2.2 Risk rules
Risk is decided deterministically over the `mvdan.cc/sh` AST, with the user's aliases and functions resolved first (the plugin sends `alias -L` and function names to the helper at startup), so a personal `alias del=rm` is still caught, and so is a `git` plugin alias like `grhh`.

- **read-only:** `ls cat head tail less wc sort uniq cut paste tr grep find (without -delete/-exec) stat file du df date cal ps top uptime who id uname hostname env which whence diff cmp comm join basename dirname realpath readlink md5/md5sum shasum awk/sed (without -i and without output redirection) tar -t unzip -l curl (GET to stdout)`, plus pipes made only of these. Runs without asking.
- **git** (git is on Linux, and on macOS once the Command Line Tools are installed; the `git` plugin aliases resolve to these):
  - **read-only:** `status`, `log`, `diff`, `show`, `branch` (list), `remote -v`, `blame`, `stash list`;
  - **other changes:** `add`, `commit`, `checkout`/`switch` (clean tree), `pull`, `push` (no force), `stash`, `branch -d`;
  - **move/delete:** `rm`, `mv`, `clean -n` → `clean -f` (dry run with `git clean -n` and the question lists the files), `branch -D`, `stash drop`;
  - **dangerous:** `reset --hard`, `checkout -- .`/`restore .` with local changes, `push --force`/`--force-with-lease`, `clean -fdx`, `rebase` on pushed commits, `filter-branch`/`filter-repo`.
- **common tools:** each tool in the catalog (§3.2) has its own rules, for example:
  - **read-only:** `jq`, `yq` (without `-i`), `rg`, `fd`, `bat`, `eza`, `tree`, `ffprobe`, `exiftool` (reading), `pdftotext` to stdout, `mlr` (without `-I`), `sqlite3` with only `SELECT`;
  - **other changes:** `ffmpeg` to a new file, `magick in out`, `pandoc -o`, `yt-dlp`, `wget`, `curl -O`, `rclone copy`, `7z a/x`;
  - **move/delete** (asked like a delete, with a count): `ffmpeg -y` / `magick` / `pandoc -o` overwriting existing files, `mogrify` and `exiftool` writing in place, `sed -i`/`yq -i`/`mlr -I` on more than one file, `rclone sync`, `rsync --delete`, `docker rm`/`prune`;
  - **unknown tools** on `$PATH` count as **other changes** (asked) unless the user marks them read-only (`zshai: lesetrygg <tool>`).
- **packages:** see §2.6. Installs zshai builds itself ask a short y/n even though they use `sudo`; removals get the move/delete-style question; adding PPAs or third-party repos and `curl … | sh` installers are dangerous.
- **move/delete:** `mv`, `rm`, `rmdir`, `find … -delete`, `find … -exec rm`, `xargs rm`, `rsync --delete`, `> existing-file` (truncation), `unlink`, `trash`. Always asks the dry-run question (§2.3).
- **other changes:** `cp mkdir touch ln chmod chown tar -c/-x zip unzip gzip sed -i tee >> >`, `curl -o`, `kill pkill`, `open/xdg-open`, `crontab -e`, `defaults write`, other executables. Asks a short y/n with the command shown.
- **dangerous:** `sudo`/`doas` (except zshai's own package commands, §2.6), `dd`, `mkfs*`, `diskutil erase*`, `shutdown`/`reboot`/`halt`, `kill -9 1`, `chmod/chown -R` on `/`, `~` or system paths, `curl … | sh` and `eval` of downloaded code, writes under `/etc`, `/System` or `/usr`, `crontab -r`, `launchctl`/`systemctl` changes, fork bombs, and `rm` on `/`, `~` or a whole volume. Shown in red; the user must type `JA`/`YES`. Some are blocked unless a setting allows them.

### 2.3 Move and delete questions
These work as in psai §2.2: zshai writes the question from a dry run, never the model, so the numbers are always real.
- **Dry run:** mvdan expands the argument words against the real file system, with command substitution switched off. If a word contains `$(…)`, the dry run is refused and the command needs `v` (view) plus `j`.
  - `find … -delete` / `-exec rm` is probed by running the same `find` with `-print` instead.
  - `xargs rm` pipelines are probed by running the left-hand side of the pipe.
- **Trash instead of `rm`:** after confirmation, the helper performs the delete itself on the resolved list:
  - **macOS:** `~/.Trash`, through Finder via `osascript` so "Put Back" works, or a plain move as fallback;
  - **Linux:** the freedesktop Trash spec (`~/.local/share/Trash/{files,info}`), or `gio trash` when present.
  - The user's `rm` command is not run. `mv` runs as the command, with every move written to the undo journal first.
  - Permanent delete happens only if the user says so ("slett permanent").
- The questions use the same texts as psai (Skal jeg flytte/slette …?), with conflict handling (skip / rename / replace).
- `angre`/`undo` reverses the last AI move or delete.

### 2.4 macOS vs Linux (GNU vs BSD)
This is the main cross-platform risk, as Windows paths were for psai.
- The context carries `os=macos|linux` and the **flavour of each core tool** (detected once with `--version` and cached): `sed=bsd|gnu`, `date=…`, `stat=…`, `find=…`, `ls=…`. On a Mac with Homebrew's GNU coreutils, `date`/`sed` stay BSD; the context lists `gdate`/`gsed` under installed tools, and the model may use them.
- The catalog (§3) records the flags of each tool **per OS**, so the validator rejects `sed -i ''` on Linux, `date -d` on macOS, `stat -c` on macOS, `find -printf` on macOS, `du --max-depth` on macOS, `grep -P` on macOS, and so on.
- The training data contains **both dialects** of every task that differs (§4.1), and the bench is run on both OSes.

### 2.5 Suggesting a missing tool
The model never writes package names or install commands itself. It says which **tool** it needs (`n: ffmpeg`, §4.3), and zshai builds the install command from the catalog. That way the package name is always right: `fd` is `fd-find` on apt, `exiftool` is `libimage-exiftool-perl`, LibreOffice is a cask on brew.

```
➜  Videos gjør om ferie.mov til mp4
  Dette trenger ffmpeg, som ikke er installert.
  Installer med `brew install ffmpeg` (ca. 95 MB)? ja / nei >  j
  …brew output…
  ▸ ffmpeg -i ferie.mov -c:v libx264 -c:a aac ferie.mp4                     [endring]
  Kjør? [j/n] >
```
- **When to suggest:**
  - Only suggest when the shipped tools can't do the job (convert video, OCR, read docx), or when the user asks for that tool.
  - If shipped tools can do it reasonably (`grep -r` instead of `rg`), just do it. zshai doesn't nag.
  - The same tool is suggested at most once per day once the user has said no. `zshai: installer <tool>` installs it directly.
- **Size comes from the manager's own dry run:** `apt-get -s install`, `pacman -Sp --print-format '%s'`, or `brew install --dry-run` plus the formula size from the API cache.
  - On apt, if the package index is old or the package isn't found, the command becomes `sudo apt update && sudo apt install …`.
- **Installing runs in your shell** through `accept-line`, so the `sudo` password prompt and the manager's progress look normal. Afterwards zshai rescans installed tools and **asks the model again** with the new context, so the original request is finished.
- **No package manager:**
  - **Mac without Homebrew:** zshai shows Homebrew's official install line and the URL, but **never runs it** (it is a `curl | bash` installer).
  - **Tool in no repository:** zshai says so and gives the project's URL from the catalog.
- **Arch:** pacman only, unless `"aur": true`. AUR packages always get a warning, *"AUR-pakker vedlikeholdes av brukere, ikke Arch"*.

### 2.6 Package managers as tasks
Package requests are ordinary zshai requests. They go through the model and are validated against each manager's flags in the catalog, like any other tool.

| Request | apt | pacman | brew | Risk |
|---|---|---|---|---|
| Is X installed? / which version? | `dpkg-query -W X` | `pacman -Q X` | `brew list --versions X` | read-only |
| Search for X | `apt search X` | `pacman -Ss X` | `brew search X` | read-only |
| Which package owns a file | `dpkg -S /path` | `pacman -Qo /path` | `brew which-formula`* / path under Cellar | read-only |
| What's outdated | `apt list --upgradable` | `pacman -Qu` (after `checkupdates` if present) | `brew outdated` | read-only |
| Biggest packages | `dpkg-query -Wf '${Installed-Size}\t${Package}\n' \| sort -n` | `pacman -Qi \| …` / `expac` | `du -sh $(brew --cellar)/*` | read-only |
| Install X | `sudo apt install -y X` | `sudo pacman -S --needed --noconfirm X` | `brew install X` | install: y/n with size |
| Update everything | `sudo apt update && sudo apt upgrade` | `sudo pacman -Syu` | `brew update && brew upgrade` | change: y/n with the number of packages from the dry run |
| Remove X / clean up | `sudo apt remove X`, `autoremove` | `sudo pacman -Rns X` | `brew uninstall X`, `brew autoremove` | move/delete: lists what goes (`apt-get -s remove`, `pacman -Rnsp`, `brew autoremove --dry-run`) |
| Add a repo / PPA / tap | `add-apt-repository` | edit `pacman.conf` | `brew tap` | dangerous (tap: change) |

\* `brew which-formula` needs the `command-not-found` tap; otherwise zshai looks the path up under `$(brew --prefix)`.
- **Detection:** macOS uses `brew` (`/opt/homebrew` or `/usr/local`). Linux reads `/etc/os-release` `ID`/`ID_LIKE`: ubuntu/debian → apt; arch/manjaro/endeavouros → pacman; fedora → dnf. Linuxbrew is used only if `packageManager` says so.
- **Partial upgrades on Arch** (`pacman -Sy X` without `-u`) are rejected by the validator and rewritten to `-S` (or `-Syu` when the user asked to update).
- `sudo` inside these commands is allowed only in the exact shapes above. Any other `sudo` stays dangerous.

### 2.7 git
- **Context**, when the cwd is in a repo: one `git status --porcelain=v2 --branch` call (plus `git stash list`) gives:
  - branch and upstream, ahead/behind;
  - staged, unstaged, untracked and conflicted counts;
  - stash count, remote names and the default branch;
  - any operation in progress (merge, rebase, cherry-pick, bisect).
  This costs ~10 ms. Outside a repo the context has no git line.
- **Tasks the data covers (en + no):**
  - **Changes and commits:** "hva har jeg endret?", "commit alt med meldingen …", "angre siste commit men behold endringene" → `git reset --soft HEAD~1`;
  - **Branches and remotes:** "lag en branch for …", "rydd bort merga brancher", "oppdater fra main" (follows your `pull.rebase` setting), "push og sett upstream";
  - **History:** "hvem endret linje 40 i x.go" (`git blame -L`), "finn commiten som fjernet foo" (`git log -S`), "hva skjedde i går" (`git log --since`);
  - **Stash, tags and recovery:** stash and tags, plus "jeg har mistet en commit" (`git reflog`);
  - **GitHub:** `gh` for PRs and issues when it's installed.
- **Validation:** a catalog of subcommands (`git help -a`) and their flags (`git <cmd> -h`) for the installed git version. The plugin's aliases (`gst`, `grhh`, …) are resolved first (§1.5).
- **Dry runs** (zshai writes the question, never the model):
  - `git clean` → `git clean -n` lists the files. The files then go to the **trash**, like `rm` (§2.3).
  - `reset --hard`, `checkout -- .`, `restore .` → show the files that would lose changes (`git status --porcelain`) and the commits that would no longer be on any branch.
  - `branch -D` → show the number of commits not merged anywhere.
  - `push --force` → `git push --dry-run` plus the remote commits that would be overwritten (`git log HEAD..@{u}`). `--force-with-lease` is preferred and always used by the model.
  - `rebase` on commits already pushed → the dangerous class.
- **Safety net:** before any git command that can lose work, zshai saves `HEAD` and a `git stash create` snapshot under `refs/zshai/backup/<timestamp>`. That costs nothing and doesn't touch the working tree. `angre` restores from it (`git reset --keep <old HEAD>` + `git stash apply <snapshot>`). Backups older than 30 days are pruned.
- **Never without being asked:** `--no-verify`, `--force` (without lease), rewriting pushed history, or changing `git config --global`.

### 2.8 Azure CLI (`az`)
- **Catalog:** `catalog/az_extract.py` runs inside az's own Python and loads the **whole command table**. For every command it records:
  - all option spellings, which options are required, and choice lists;
  - `--yes` / `--no-wait` support, and whether the command is deprecated or in preview;
  - a risk class from the verb;
  - the help examples (seeds for training, as psai used help examples).
  It covers every core module plus installed extensions, and is re-extracted when `az version` changes. zshai ships a baseline catalog for the latest az; your own extract overrides it.
- **"Most modules":** all core groups are validated. Training covers the ~30 most-used groups in depth:
  - account, group, resource, deployment/bicep, vm/vmss, network, storage, keyvault;
  - webapp/appservice/functionapp, containerapp, aks, acr;
  - sql, postgres/mysql flexible-server, cosmosdb, redis, servicebus/eventhub;
  - monitor/log-analytics, role, ad, identity, policy, lock, tag, dns, cdn/afd, consumption, backup.
  Other groups work through catalog validation and the help examples. A command from an extension that isn't installed leads to an install suggestion, `az extension add --name …` (§2.5).
- **Context, read from files** (no `az` call, because az takes ~1–2 s to start):
  - from `~/.azure/azureProfile.json` (UTF-8 with a BOM): logged in or not, the current subscription's name, the tenant and the cloud;
  - from `~/.azure/config`: default group and location.
- **Validation:**
  - the command path exists;
  - every option exists for that command;
  - required options are present, or a default is configured;
  - choice values are valid;
  - deprecated commands give a warning.
  JMESPath in `--query` is parsed for syntax.
- **Risk** (from the catalog's verb class, plus rules):
  - **read:** `show`, `list`, `get-*`, `check-*`, `download`, `tail`;
  - **change:** `create`, `update`, `set`, `add`, `start`, `scale`, `account set`, `login`;
  - **move/delete:** `delete`, `remove`, `stop`/`deallocate`, `regenerate-key`, `revoke`;
  - **dangerous:** `group delete`, any `purge`, `lock delete`, `role assignment create/delete` at subscription or management-group scope, `ad app credential reset`, `ad sp create-for-rbac`, `vm run-command`, `keyvault secret show` (prints a secret), and any delete in a subscription listed in `"protectedSubscriptions"`.
  The model never adds `--yes`. zshai adds it after its own question, so az doesn't ask a second time.
- **Dry runs for deletes** use read-only az calls:
  - `az group delete -n rg-web` → `az resource list -g rg-web` → *"Skal jeg slette ressursgruppen rg-web (14 ressurser: 3 web-apper, 1 SQL-server, …) i abonnementet «Min sub»? Dette kan ikke angres."*
  - Deployments (`deployment group create`) run `what-if` first and show its summary.
- **Answers:** for questions the model uses `--query` and `-o tsv`/`table` ("hvor mange VM-er kjører?" → `az vm list -d --query "length([?powerState=='VM running'])" -o tsv`).
- **Eval without a cloud:** the sandbox has no Azure, so az tasks are checked **statically**. The generated command is normalised through the catalog (command path, set of options, values) and compared with 1–3 reference answers. There is also an opt-in **live read-only** smoke set against your subscription (only `show`/`list`).

---

## 3. Knowledge: the tools
### 3.1 Shipped tools
Generated by `catalog/extract.sh` on **clean** systems and saved as JSON in `catalog/`:
- **Systems:**
  - stock macOS (current release, in a Tart VM on llmhost, no Homebrew, Command Line Tools *not* installed by default; recorded both ways because `git`/`python3` depend on them);
  - stock Ubuntu 26.04 (server and desktop, which differ: desktop has `xdg-open`, `gio`, `wl-copy`/`xclip` are not default);
  - stock Arch Linux (`base` + `base-devel`), which ships much less than Ubuntu (no `wget`, `rsync` or `unzip` in `base`, for example). These gaps are filled through §2.5;
  - Debian 13 and Fedora as a best-effort check.
- **For each system:**
  1. Every executable in `/bin`, `/usr/bin`, `/sbin` and `/usr/sbin`, plus zsh builtins (`whence -w`).
  2. For each tool: its `man` page rendered to text, parsed into a flag list (short, long, and whether it takes an argument), plus `--help`/`--version` where available. This is zshai's equivalent of psai's parameter metadata, used by the validator and for constrained decoding later.
  3. The examples from each man page's EXAMPLES section become datagen seeds, as psai seeded from help examples.
- **Tiers** (decided from the data, not by hand):

  | Tier | Meaning | Examples (to confirm by catalog) |
  |---|---|---|
  | A | On both, compatible flags for common use | `ls cp mv rm mkdir cat head tail wc sort uniq cut paste tr grep awk find xargs tar gzip zip unzip curl ps kill diff tee basename dirname chmod ln touch du df uname env` |
  | B | On both, different flags or behaviour | `sed -i`, `date`, `stat`, `find -printf`, `du`, `readlink -f`, `base64 -d/-D`, `md5sum` vs `md5`, `sha256sum` vs `shasum -a 256`, `ls --time-style` vs `-D`, `xargs -r`, `split`, `seq` |
  | C | One OS only, with an equivalent on the other | `open`/`xdg-open`, `pbcopy`/`pbpaste` vs `wl-copy`/`xclip` (often not installed!), `mdfind`, `sips` (images on Mac), `say`, `defaults`, `diskutil`, `launchctl`/`systemctl`, `osascript`, `ip`/`ss` vs `ifconfig`/`netstat` |
  | D | Not shipped | The common tools of §3.2, used if installed and suggested for install if not; anything else on `$PATH` |

- A **coverage matrix** (tool × common use × OS) drives datagen and eval, as in psai §3.
- At runtime, the small model gets no retrieval; the catalog is only used by the validator, and by the big model when escalating.

### 3.2 Common tools (proposed list, ~45)
These are the tools zshai is trained on, validates, and knows how to install. The list is chosen by how often tasks need them and by covering what the shipped tools can't do. For each tool, the catalog records the **binary name and package name per manager** (the differences cause real mistakes), the version, flags (from `--help`/man, extracted in sandboxes with each tool installed) and risk rules.

| Area | Tools (binary) | Package-name gotchas |
|---|---|---|
| JSON/YAML/CSV/data | `jq`, `yq` (mikefarah), `mlr` (Miller, also covers CSV), `sqlite3` | apt `yq` must be the mikefarah one (check 26.04); `sqlite3` = pacman `sqlite`; macOS ships `sqlite3` and newer macOS ships `jq` (check) |
| Search & files | `rg`, `fd`, `fzf`, `bat`, `eza`, `tree`, `ncdu` | apt: `fd-find` → binary **`fdfind`**, `bat` → binary **`batcat`**; `rg` = package `ripgrep` everywhere |
| Archives & sync | `7z`, `unzip`/`zip` (missing on Arch base), `xz`, `zstd`, `rsync`, `rclone` | 7z: apt `7zip`, pacman `7zip`, brew `sevenzip` (binary `7zz`); newer macOS ships `openrsync` as `rsync` |
| Download & network | `wget`, `http` (HTTPie), `dig`, `whois`, `mtr`, `nc`, `yt-dlp` | `dig`: apt `bind9-dnsutils`, pacman `bind`; macOS ships `dig` |
| Images | `magick` (ImageMagick 7; `convert` on IM6), `exiftool`, `optipng`, `jpegoptim`, `cwebp`, `sips` (shipped on macOS) | exiftool: apt **`libimage-exiftool-perl`**, pacman `perl-image-exiftool`; apt `cwebp` = `webp` |
| Audio/video | `ffmpeg`, `ffprobe`, `sox`, `mediainfo` | `ffprobe` comes with `ffmpeg` |
| Documents & PDF | `pandoc`, `pdftotext`/`pdfinfo`/`pdfunite` (poppler), `qpdf`, `gs`, `soffice` (LibreOffice, headless convert), `tesseract` (OCR) | apt `poppler-utils`; brew LibreOffice is a **cask**; pacman `libreoffice-fresh`; tesseract language data: apt `tesseract-ocr-nor`, pacman `tesseract-data-nor` |
| Dev & system | `git`, `gh`, `az` (§2.8), `tmux`, `htop`, `btop`, `watch` (not on macOS), `tldr` (tealdeer), `docker`/`podman`, `code` (open in VS Code), `fastfetch` (system summary, if installed) | macOS `git` comes with the Command Line Tools (`xcode-select --install`) or brew |
| Remote (shipped) | `ssh`, `scp`, `rsync`, `ping`, `nc`: hosts come from `~/.ssh/config` (§3.3) | `ping -c N` on both; `ping -t` means different things on macOS (timeout) and Linux (TTL) |
| Project tools | `cargo`, `make`, `go`, `npm`/`pnpm`, `python3`/`uv`: used when the cwd is that kind of project (Context v2 sees `Cargo.toml`, `Makefile`, `package.json`, …) | `cargo` is in brew and pacman; apt's is old, so rustup is suggested instead |
| GNU on macOS | `gdate`, `gsed`, `gawk`, `gfind` (coreutils, gnu-sed, gawk, findutils) | used only when installed; the model prefers BSD forms |

Rules:
- The model picks the **best installed** tool (`rg` over `grep -r` when present), but **never needs** a common tool when shipped tools do the job well.
- A tool that is installed but missing from the catalog gets its flags from `--help`/man the first time it's used, cached per version. The validator then checks flags best-effort (warn rather than block).
- `scripts/rank-history.zsh` prints only command names, never arguments.

### 3.3 What your Mac's history says (ranked 2026-11-14, `zsh-history-rank.txt`)
550 entries over two months (2026-09-15 → 2026-11-14).
- **The top commands are about getting somewhere, not processing files:** `ssh` 13%, `ls` 11%, `brew` 11%, `cargo` 8% (`cargo run` 52×), `cd` 7%, `claude` 6%, `ping` 4%, `scp` 2%. Text tools (`grep`, `sed`, `awk`, `find`, `jq`) hardly appear; of the common tools, only `git` (6×) shows up.
  - **Reading:** the Unix-tool work (finding, counting, converting, cleaning up) isn't something you do by hand today. That is exactly what zshai should take over, so **the list is not cut on usage alone.** A short history from a launcher-style Mac says little about which tools tasks need.
- **What changes in the plan:**
  1. **Remote work is first-class.**
     - **Hosts in the context:** host *names* (no addresses or keys) from `~/.ssh/config` and `known_hosts` go into the context, so "er llmhost oppe?", "kopier rapport.pdf til llmhost" and "hvor mye disk er ledig på llmhost?" become `ping -c 3 llmhost`, `scp rapport.pdf llmhost:` and `ssh llmhost df -h`.
     - **Remote commands are risk-checked:** the command inside `ssh host '…'` goes through the same risk rules as a local one. A remote move or delete can't be dry-run locally, so the question shows the command and an orange **[llmhost]** tag, and needs `j`.
     - zshai is also meant to run on those Linux servers itself (apt/pacman), which is already a target.
  2. **Homebrew is a main use:** `brew update` 23×, `upgrade` 22×, `install` 13×, `search` 5×, `remove` 2×.
     - Package tasks (§2.6) are a priority for the MVP.
     - **Casks are first-class:** most of your installs are apps (ghostty, kitty, visual-studio-code, wireshark-app). zshai checks formula vs cask with `brew info --json=v2` and builds `brew install --cask …` itself.
     - **Typos are caught:** `brew upadte` is fixed by the validator from the catalog's subcommand list before it runs.
  3. **Project tools added** (`cargo` etc., the row above) and `code`/`fastfetch` as "use if installed".
  4. **Dropped from the list:**
     - `xsv`/`qsv`: `mlr` covers CSV;
     - `dust`: `du`/`ncdu` cover it;
     - `entr`: niche.
  5. **Not added** (personal or interactive, nothing a task would write): `claude`, `psai`, `pwsh`, `cli-mon`, `ops-term`.
- **Router test material:**
  - Your aliases in use are `ls→ls -G`, the dir-stack aliases (`2→cd -2`, from oh-my-zsh's `autopushd`) and `grep --color=auto`. The router must treat bare numbers `1`–`9` as commands, not prose.
  - Some history lines are only numbers or codes (`69`, `70`, `10M35`, `16M35`, …), probably answers typed into another program's prompt. The router must leave lines like these alone; they go into its test set as "shell".
- **Re-run in Phase 0** on the Linux servers you ssh into. That is where the file work probably happens, and their histories will rank the text tools better than the Mac's.

---

## 4. Training data
Target for v1: **~30k verified examples per language.** That is fewer than psai's 50k, because the base model already knows Unix; psai's real-usage data will show whether that's enough.

### 4.1 Sources
| Source | Notes |
|---|---|
| Man-page examples | Seeds, paraphrased 5–10× into requests (en/no) |
| Port of psai's scenarios | The same files/photos/CSV/JSON/logs/archives/dates/system-info scenarios, solved with Unix tools. psai's 38 bench tasks also become the first zshai bench tasks (§6). |
| **Both dialects** | Every tier-B/C task is generated and verified **on both OSes**. The context tells the model which one to write. |
| Ask/refuse/escalate | Ported from psai (`ask_examples.py`, `cannot_examples.py`), plus **contrast examples** so doable tasks aren't refused (a psai v5 lesson) |
| **With/without tools** | The same request generated with different `have:` lines in the context. Tool installed → use it. Not installed, but shipped tools work → shipped answer. Not installed and needed → `n: <tool>`. This teaches the model to read the context instead of assuming. |
| Common tools | Per tool in §3.2: man/`--help`/tldr-page examples as seeds; scenarios that need them (video, images, PDFs, JSON APIs, OCR) |
| Package tasks | §2.6 requests × apt/pacman/brew, verified in the Ubuntu and Arch images and the macOS VM (with network to a local package mirror/cache) |
| Remote | ping/scp/ssh/rsync to hosts from the context (`hosts: llmhost, …`); remote read-only vs remote change; the remote OS is unknown, so remote commands stay portable or ask first |
| Session/history | "what did I just run", "rerun the last one with sudo" (→ dangerous), "what failed": answered from `fc -l`, `$?` and the context |
| Notes | Examples with a `notes:` line (psai Next #4, built in from the start here) |
| Norwegian | Reuse psai's hand-written Norwegian seed set (the requests are the same, only the answers change) |
| Real usage | Accepted, edited and rejected turns from the SQLite log |

### 4.2 Execute-verify
- **Linux:** podman images with zsh + oh-my-zsh (robbyrussell, `git`), no network, modelled on psai's `sandbox/Containerfile`:
  - `ubuntu-stock` and `ubuntu-tools` (all §3.2 tools installed);
  - `arch-stock` and `arch-tools`.
  - Package tasks run in a variant with network access only to a local apt/pacman cache (apt-cacher-ng / a pacman mirror on this server).
- **macOS:** a Tart VM on llmhost with two snapshots: `stock` (clean macOS + oh-my-zsh) and `brew` (Homebrew + the §3.2 tools); snapshot reset between batches. It is slower than podman, so tier-A examples are verified on Linux only, and tier B/C on both.
- **Steps:**
  1. Parse (`zsh -n` + mvdan) and validate against the catalog.
  2. Run in the fixture with a timeout.
  3. Run the checker (a Python assertion on the final file-system state or stdout).
  4. Check the risk label against §2.2.
- **Two independent teacher solutions must agree** before an example is kept (a psai lesson).
- Dedupe (AST hash plus near-duplicate request text), balance across coverage cells, and keep repair examples.

### 4.3 Output format: no JSON
Shell commands are full of `"`, `'`, `\` and `$`. psai's 3B already broke JSON on a single quote, and shell would be much worse. The target is a **line-based format** that needs no escaping:
```
r: move
m: run
a:
n:
x: 0
e:
---
mkdir -p ~/Pictures/2026-11-10 && mv -- ~/Pictures/IMG_41*.jpg ~/Pictures/2026-11-10/
```
- The header lines are fixed.
- Everything after `---` is the command, taken verbatim.
- `a` holds a clarifying question; `x` means escalate.
- `m: show` means print and explain, don't run; `e:` lines hold the explanation (only with `m: show`).
- Only the first command counts: parsing stops at a second `---` or header line (small models sometimes append alternatives).
- `n` lists missing tools the task needs (`n: ffmpeg`). The script after `---` is then empty, or it is a shipped-tools fallback that zshai offers next to the install question.
- Parsing is trivial, and an unfinished answer is easy to detect.

Prefer the short, idiomatic form (`tail -n +2 f.csv | wc -l`, not a loop).

---

## 5. Models
- **Own model:** `zshai-mini` is trained, versioned and served on its own, separately from psai-mini. It has its own weights, its own MLX/llama server on port 8093, and its own release rule (§6). The training *pipeline* is reused from psai, but no data or weights are shared.
- **Base:** Qwen2.5-Coder (psai's lesson: stay with it over Qwen3-1.7B). Phase 0 benchmarks **1.5B vs 3B, untuned and after a quick LoRA**. Expect the 1.5B to do much better here than on PowerShell. If it reaches ≥ 0.9× the 3B, ship the 1.5B (≈ 0.27 s vs 0.48 s p50 on the M5 Pro).
- **Training:**
  - MLX LoRA on llmhost with psai's `train/` pipeline (`llmhost-run.sh`, thermal guard, resumable `pipeline.sh`);
  - quantize to 4-bit with **group 32**;
  - later, DPO/GRPO with the sandbox checker as the reward (psai §5.3).
- **One zshai model for both languages** unless Phase 0 shows otherwise (psai's 3B handled both).
- **Serving:** a local MLX server on Macs (installed like psai's `scripts/mac/install.sh`), llama-server on Linux, and llmhost as optional escalation. Keep the fixed prompt prefix cached. Latency budget is as in psai §5.4, plus ~10 ms for the socket round trip.
- **Later:** constrained decoding to the catalog's real flags for the chosen tool (llama.cpp grammar / MLX logits processor), the shell version of psai's planned parameter constraint.

### 5.1 Log and feedback
SQLite `~/.config/zshai/zshai.db` with tables `turns`, `responses` and `effects`, in psai's schema.
- **Effects** come from FSEvents (macOS) or inotify (Linux) during the run, merged with the trash/move journal.
- **Querying:** `zshai log`, `zshai log --changed`, `zshai log --id 42`, plus the `zshai:` meta commands.

---

## 6. Evaluation
- **zshai-bench:**
  - **Bench v0:** psai's 38 tasks, ported to Unix tools, in en + no.
  - **Mac-specific tasks** (open, pbcopy, sips, mdfind) and **Linux-specific ones** (xdg-open, ip, systemctl status).
  - **Tool tasks:** each §3.2 tool, run in `*-tools` images (expect the tool used) and in `*-stock` images (expect a shipped fallback or a correct `n:`).
  - **Package tasks:** §2.6 on apt, pacman and brew; checked on the resulting package state.
  - **Held-out set:** fixture types never seen in training, as psai did.
  - The bench is **run on both OSes**; the score is the lower of the two.
- **Router test set** (§2.1): shell lines wrongly sent to the AI = 0 is a release rule.
- **Metrics:** accuracy, safety (100% of moves and deletes asked with correct numbers), wrong-dialect rate (GNU flags on macOS and the reverse), **wrong-binary rate** (`fd` vs `fdfind`, `7z` vs `7zz`), **needless install suggestions** (should be 0 when shipped tools suffice), clarification precision, escalation rate, and p50/p95 latency on M1, M4 and M5 Pro Macs and on this Linux box.
- **Release rule:** accuracy ≥ previous release on both OSes, safety = 100%, router false positives = 0, p95 < 1 s.

---

## 7. Phases

| Phase | Deliverable | Done when |
|---|---|---|
| **0. Baseline** (1 wk) | zsh + oh-my-zsh on this server (Linux dev box); set up oh-my-zsh + robbyrussell + `git` in the sandbox and Tart VM; catalog on clean macOS + Ubuntu + Arch; common-tools catalog (§3.2: binaries, packages per manager, flags) and the final tool list ranked from your history; bench v0 (38 ported tasks × en/no) with Linux sandbox + macOS Tart VM; untuned 1.5B/3B/teacher scores and latency | We know the base model, and the tool list is fixed from data |
| **1. MVP** (2–3 wk; started 2026-11-14) | Plugin (router, widget, one-key confirm, history), Go helper (context, validator, risk, dry run, trash, undo, SQLite log, `zshai:` commands, tool detection, install suggestions, package-manager rules for apt/pacman/brew, git context/dry runs/backup refs, az catalog validation and delete dry runs), using the untuned model or llmhost's teacher | Both examples at the top work on macOS and Linux inside your theme; move/delete never runs without asking; "mov → mp4" without ffmpeg ends in an install question and then a finished task on apt, pacman and brew; router tests pass |
| **2. Data** (2–3 wk) | Port psai datagen; man-page seeds; both-dialect generation; execute-verify on both OSes | ≥ 30k verified per language; ≥ 90% of coverage cells have ≥ 5 examples |
| **3. zshai-mini v1** (1–2 wk) | LoRA model, 4-bit g32; Mac installer (helper + model + MLX server + plugin); Linux tarball | Bench ≥ teacher on common tasks; p95 < 1 s on the M1 |
| **4. Reward tuning + feedback** (ongoing) | RL with the checker, real usage, constrained flag decoding | Better each release |
| **5. Ship** | Homebrew tap (formula installs the helper; plugin in `$ZSH_CUSTOM`), .deb, AUR `PKGBUILD`, tar; notarized macOS binary | Installs cleanly on a fresh Mac, a fresh Ubuntu and a fresh Arch (AUR package) |

## 8. Repo layout
```
plugin/zshai.plugin.zsh   ZLE widget, router, display, one-key confirm, socket client
cmd/zshai/                Go helper: daemon, CLI (zshai --lang, --devices, log)
internal/                 context, model client, validator, risk, dryrun, trash, journal, store, standards, messages,
                          tools (detection + install), pkg (apt, pacman, brew, dnf)
internal/router_test/     router test set (shell lines vs requests, en/no)
catalog/                  extract.sh + per-OS catalog JSON (tools, flags, man examples) + tier table,
                          common-tools.json (binary + package per manager, flags, risk rules)
datagen/                  ported from psai + man-page seeds + dialect generation
sandbox/                  Containerfiles: ubuntu-{stock,tools}, arch-{stock,tools} (zsh + oh-my-zsh, robbyrussell, plugins=(git)),
                          package-cache setup, Tart VM setup for macOS (stock + brew snapshots)
train/                    reuse psai's MLX pipeline (symlink or copy), zshai dataset builder
eval/                     zshai-bench, runner (podman + Tart), summarizer
scripts/mac/              installer, model server start/stop (as psai)
```

## 9. Risks
- **Router false positives**: a real command sent to the AI would make zshai unbearable as a daily shell. Mitigations: the conservative "looks like prose" rule, the `,` override, and the release rule of zero false positives.
- **GNU/BSD dialect mistakes**: mitigated by per-OS flag validation, both-dialect training data, and the bench scored on both OSes.
- **Taking over `accept-line`** can clash with other plugins that wrap it. Today you use only `git`, which wraps nothing. If you add zsh-autosuggestions or zsh-syntax-highlighting later, zshai is tested against them before it supports them.
- **zsh-specific behaviour** (`nomatch`, no word-splitting) differs from the bash that models know best. Mitigation: the sandbox runs real zsh with oh-my-zsh options, and failures turn into repair examples.
- **macOS VM throughput** for execute-verify: only tier-B/C examples run there.
- **"Shipped" changes between OS releases** (for example, newer macOS adding or removing tools). Mitigation: re-run the catalog for each OS release.
- **Package names and binaries drift** between distros and releases (`fdfind`, `batcat`, IM6 vs IM7, the `yq` name clash). Mitigation: the model never writes package names, the catalog is tested by actually installing each tool in each image, and the catalog is re-run each release.
- **Arch moves fast** (rolling release). Mitigation: the `arch-*` images are rebuilt weekly and the tool catalog re-extracted; the bench on Arch is a release gate.
- **`sudo` in an AI flow:** a password prompt in the middle of a turn is fine because the command runs in your own shell, but zshai must never see or store the password. Allowed `sudo` shapes are fixed (§2.6).
- **Nagging:** install suggestions where a shipped tool would do erode trust. Mitigation: the needless-suggestion metric must be 0, and declined suggestions are remembered.

## 10. Open questions
- None right now. Answered 2026-11-14: Fedora/dnf stays best-effort; no Snap/Flatpak; the common-tools list is settled in Phase 0 from `scripts/rank-history.zsh` output from the Mac.
