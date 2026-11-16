// Package risk classifies a parsed command line by deterministic rules
// (PLAN.md §2.2): read-only, change, install, move/delete, dangerous.
// The model's own label is never trusted on its own.
package risk

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/MariusEng/zshai/internal/azcat"
	"github.com/MariusEng/zshai/internal/shell"
)

type Level int

const (
	Read Level = iota
	Change
	Install
	Delete // move or delete: gets the dry-run question
	Danger
)

func (l Level) String() string {
	return [...]string{"read", "change", "install", "delete", "danger"}[l]
}

// Finding is one reason for the level.
type Finding struct {
	Level Level
	Why   string
	Cmd   *shell.Cmd
	Kind  string // for the dry run: rm, mv, find-delete, xargs-rm, git-clean, git-reset, git-branch-D, git-push-force, az-delete, pkg-remove, truncate, rsync-delete
}

type Result struct {
	Level    Level
	Findings []Finding
	Remote   []string // hosts commands run on
}

// Env is what the rules need to know about the machine.
type Env struct {
	Home string
	OS   string // "darwin" | "linux"
	Az   *azcat.Catalog
	// ProtectedSubs are subscription names/ids where any az delete is dangerous.
	ProtectedSubs []string
	CurrentSub    string
}

func (r *Result) add(l Level, kind string, c *shell.Cmd, why string, a ...any) {
	if len(a) > 0 {
		why = fmt.Sprintf(why, a...)
	}
	r.Findings = append(r.Findings, Finding{Level: l, Why: why, Cmd: c, Kind: kind})
	if l > r.Level {
		r.Level = l
	}
}

// Classify applies the rules to every simple command.
func Classify(s *shell.Script, env Env) Result {
	var r Result
	for _, c := range s.Cmds {
		if c.Remote != "" && !slices.Contains(r.Remote, c.Remote) {
			r.Remote = append(r.Remote, c.Remote)
		}
		classifyRedirs(&r, c, env)
		classifyCmd(&r, c, env)
		if slices.Contains(c.Wrappers, "sudo") || slices.Contains(c.Wrappers, "doas") {
			if !pkgSudoOK(c) {
				r.add(Danger, "", c, "runs as root (sudo)")
			}
		}
	}
	return r
}

var readOnly = set(`ls cat head tail less more wc sort uniq cut paste tr grep egrep fgrep zgrep stat file du df
	date cal ps top htop btop uptime who w whoami id groups uname hostname env printenv which whence where type
	diff cmp comm join basename dirname realpath readlink md5 md5sum shasum sha1sum sha256sum sha512sum cksum
	echo printf print pwd true false test [ [[ seq yes column fold fmt nl od xxd hexdump strings expand unexpand
	rev tac look jot man apropos whatis tldr history fc dirs jobs wait sleep cd pushd popd export alias unalias
	set setopt unsetopt source . hash rehash ping ping6 dig nslookup host traceroute traceroute6 mtr whois
	netstat ss ip ifconfig lsof vm_stat sw_vers system_profiler mdfind mdls sysctl free vmstat iostat nproc
	lscpu lsblk lsusb lspci findmnt mount locale tty stty clear reset sum base64 bc dc expr awk gawk sed gsed jq
	rg fd fdfind bat batcat eza tree ncdu fzf ffprobe mediainfo pdfinfo identify exiftool mlr less zcat bzcat
	xzcat zstdcat gdate gfind fastfetch neofetch screenfetch nvidia-smi sensors last lastlog finger getent
	dscl scutil networksetup pmset ioreg kextstat log csrutil spctl codesign security pbpaste wl-paste xclip
	xsel gh-dash tmux-ls k9s journalctl dmesg uptime arch getconf`)

func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Fields(s) {
		m[f] = true
	}
	return m
}

var systemDirs = []string{"/etc", "/usr", "/bin", "/sbin", "/System", "/Library", "/boot", "/var/lib", "/opt/homebrew/Cellar", "/private/etc"}

func isSystemPath(p string) bool {
	for _, d := range systemDirs {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// isHugeTarget: deleting these is never a normal request.
func isHugeTarget(p string, home string) bool {
	clean := strings.TrimSuffix(p, "/")
	switch clean {
	case "", "/", "~", "$HOME", "${HOME}", "/*", "~/*", "$HOME/*", "*", ".", "..", "/home", "/Users", "/Volumes", "/mnt", "/media":
		return true
	}
	if home != "" && (clean == home || clean == home+"/*") {
		return true
	}
	if isSystemPath(clean) {
		return true
	}
	return false
}

func classifyRedirs(r *Result, c *shell.Cmd, env Env) {
	for _, rd := range c.Redirs {
		op := strings.TrimLeft(rd.Op, "0123456789&")
		switch op {
		case ">", ">|", ">!", ">>", "&>", "&>>":
		default:
			continue
		}
		t := rd.Target
		if rd.Lit && (t == "/dev/null" || t == "/dev/stderr" || t == "/dev/stdout" || strings.HasPrefix(t, "&")) {
			continue
		}
		if rd.Lit && isSystemPath(t) {
			r.add(Danger, "", c, "writes to system file %s", t)
			continue
		}
		if op == ">>" || op == "&>>" {
			r.add(Change, "", c, "appends to %s", show(t))
		} else {
			r.add(Change, "truncate", c, "writes %s", show(t))
		}
	}
}

func show(s string) string {
	if s == "" {
		return "a file"
	}
	return s
}

func classifyCmd(r *Result, c *shell.Cmd, env Env) {
	n := c.Name
	args := c.Args
	f := shell.Flags(args)
	ops := shell.Operands(args)
	switch {
	case n == "" && len(c.Words) >= 0 && c.Path == "":
		r.add(Change, "", c, "runs a command whose name is computed")
		return
	case n == ":" || readOnly[n] && !special[n]:
		return
	}
	switch n {
	case "zshai":
		// zshai fmt only formats what it reads (PowerShell-style tables); the rest of
		// zshai's own commands are not for the model to run
		if len(args) > 0 && (args[0] == "fmt" || args[0] == "format") {
			return
		}
		r.add(Change, "", c, "runs zshai %s", strings.Join(args, " "))
	// ---- delete / move
	case "rm", "unlink", "rmdir", "trash", "srm":
		for _, o := range ops {
			if isHugeTarget(o, env.Home) {
				r.add(Danger, "", c, "deletes %s", o)
				return
			}
		}
		r.add(Delete, "rm", c, "deletes files")
	case "shred", "wipe":
		r.add(Danger, "", c, "destroys file contents permanently")
	case "mv":
		for _, o := range ops {
			if isHugeTarget(o, env.Home) && o != "." && o != ".." {
				r.add(Danger, "", c, "moves %s", o)
				return
			}
		}
		r.add(Delete, "mv", c, "moves files")
	case "find", "gfind":
		classifyFind(r, c, env)
	case "rsync":
		if f["--delete"] || f["--delete-after"] || f["--delete-before"] || f["--delete-during"] || f["--remove-source-files"] {
			r.add(Delete, "rsync-delete", c, "rsync deletes or moves files")
		} else {
			r.add(Change, "", c, "copies files")
		}
	case "sed", "gsed", "perl":
		if f["-i"] || anyPrefix(args, "-i") || f["--in-place"] {
			r.add(Change, "", c, "edits files in place")
		} else if n == "perl" {
			r.add(Change, "", c, "runs a perl program")
		}
	case "awk", "gawk":
		if slices.Contains(args, "inplace") {
			r.add(Change, "", c, "edits files in place")
		}
	case "cp", "mkdir", "touch", "ln", "install", "chgrp", "split", "csplit", "patch", "tee", "truncate", "mkfifo", "mktemp":
		if n == "truncate" {
			r.add(Delete, "", c, "truncates files")
			return
		}
		for _, o := range ops {
			if isSystemPath(o) {
				r.add(Danger, "", c, "writes into %s", o)
				return
			}
		}
		r.add(Change, "", c, "%s changes files", n)
	case "chmod", "chown":
		if f["-R"] {
			for _, o := range ops {
				if isHugeTarget(o, env.Home) {
					r.add(Danger, "", c, "%s -R on %s", n, o)
					return
				}
			}
		}
		r.add(Change, "", c, "changes permissions")
	case "tar", "gtar", "bsdtar":
		if f["-t"] || len(args) > 0 && !strings.HasPrefix(args[0], "-") && strings.Contains(args[0], "t") && !strings.ContainsAny(args[0], "cxru") {
			return
		}
		r.add(Change, "", c, "creates or extracts an archive")
	case "zip", "gzip", "gunzip", "bzip2", "bunzip2", "xz", "unxz", "zstd", "unzstd", "7z", "7zz", "unar", "ditto":
		if n == "7z" || n == "7zz" {
			if len(ops) > 0 && (ops[0] == "l" || ops[0] == "t") {
				return
			}
			if len(ops) > 0 && ops[0] == "d" {
				r.add(Delete, "", c, "deletes files from an archive")
				return
			}
		}
		if (n == "gzip" || n == "xz" || n == "zstd" || n == "bzip2") && (f["-c"] || f["-l"] || f["-t"] || f["--stdout"]) {
			return
		}
		r.add(Change, "", c, "creates or extracts an archive")
	case "unzip":
		if f["-l"] || f["-v"] || f["-t"] || f["-Z"] || f["-p"] {
			return
		}
		r.add(Change, "", c, "extracts an archive")
	case "curl":
		if f["-o"] || f["-O"] || f["--output"] || f["--remote-name"] || f["-T"] || f["--upload-file"] ||
			f["-d"] || f["--data"] || f["-F"] || f["--form"] || f["--json"] || writeMethod(args) {
			r.add(Change, "", c, "downloads to a file or sends data")
		}
	case "wget":
		if !(slices.Contains(args, "-O") && slices.Contains(args, "-")) && !slices.Contains(args, "-qO-") && !slices.Contains(args, "-O-") {
			r.add(Change, "", c, "downloads a file")
		}
	case "yt-dlp", "scp", "sftp", "rclone":
		if n == "rclone" {
			classifyRclone(r, c, ops)
			return
		}
		r.add(Change, "", c, "transfers files")
	case "ssh":
		if c.Remote == "" { // interactive login: as typed, fine
			return
		}
		// remote commands are classified on their own (they are in s.Cmds with Remote set)
	case "kill", "pkill", "killall":
		if slices.Contains(ops, "1") || slices.Contains(ops, "-1") {
			r.add(Danger, "", c, "kills init / all processes")
			return
		}
		r.add(Change, "", c, "stops processes")
	case "open", "xdg-open", "code", "say", "pbcopy", "wl-copy", "osascript", "notify-send", "afplay":
		if n == "osascript" {
			r.add(Change, "", c, "runs AppleScript")
			return
		}
		r.add(Change, "", c, "opens or changes something outside the shell")
	case "crontab":
		switch {
		case f["-l"]:
		case f["-r"]:
			r.add(Danger, "", c, "removes the whole crontab")
		default:
			r.add(Change, "", c, "edits the crontab")
		}
	case "defaults":
		if len(ops) > 0 && (ops[0] == "read" || ops[0] == "domains" || ops[0] == "read-type" || ops[0] == "find") {
			return
		}
		r.add(Change, "", c, "changes macOS settings")
	case "launchctl":
		if len(ops) > 0 && (ops[0] == "list" || ops[0] == "print" || ops[0] == "blame") {
			return
		}
		r.add(Danger, "", c, "changes system services")
	case "systemctl", "service":
		if len(ops) > 0 && (strings.HasPrefix(ops[0], "list") || ops[0] == "status" || strings.HasPrefix(ops[0], "is-") || ops[0] == "show" || ops[0] == "cat") {
			return
		}
		r.add(Danger, "", c, "changes system services")
	case "dd", "mkfs", "fdisk", "sfdisk", "gdisk", "parted", "wipefs", "mkswap", "shutdown", "reboot", "halt", "poweroff", "init", "nvram", "csrutil-disable":
		r.add(Danger, "", c, "%s can destroy data or stop the machine", n)
	case "diskutil":
		if len(ops) > 0 && (ops[0] == "list" || ops[0] == "info" || ops[0] == "apfs" && len(ops) > 1 && ops[1] == "list") {
			return
		}
		r.add(Danger, "", c, "changes disks")
	case "eval", "exec":
		r.add(Danger, "", c, "evaluates generated code")
	case "sh", "bash", "zsh", "dash", "fish", "python", "python3", "node", "ruby":
		if c.PipeFrom != nil && pipesDownload(c.PipeFrom) {
			r.add(Danger, "", c, "runs code downloaded from the internet")
			return
		}
		if (n == "python3" || n == "python") && len(args) >= 2 && args[0] == "-m" && (args[1] == "json.tool" || args[1] == "http.server") {
			if args[1] == "json.tool" {
				return
			}
		}
		r.add(Change, "", c, "runs a program")
	case "xargs":
		r.add(Change, "", c, "xargs runs echo")
	// ---- package managers
	case "apt", "apt-get", "aptitude", "dpkg", "pacman", "yay", "paru", "brew", "dnf", "port", "mas", "pip", "pip3", "npm", "pnpm", "cargo", "go", "uv", "gem":
		classifyPkg(r, c, ops, f)
	case "add-apt-repository", "apt-key":
		r.add(Danger, "", c, "adds a third-party package source")
	// ---- git, gh, az, containers
	case "git":
		classifyGit(r, c, env)
	case "gh":
		classifyGh(r, c, ops)
	case "az":
		classifyAz(r, c, env)
	case "docker", "podman":
		classifyDocker(r, c, ops)
	case "kubectl":
		if len(ops) > 0 && slices.Contains([]string{"get", "describe", "logs", "top", "explain", "version", "api-resources", "config"}, ops[0]) {
			return
		}
		if len(ops) > 0 && ops[0] == "delete" {
			r.add(Delete, "", c, "deletes Kubernetes resources")
			return
		}
		r.add(Change, "", c, "changes a Kubernetes cluster")
	// ---- common tools that write
	case "ffmpeg":
		if f["-y"] {
			r.add(Delete, "", c, "ffmpeg -y overwrites files")
		} else {
			r.add(Change, "", c, "writes a new media file")
		}
	case "magick", "convert":
		if len(ops) > 0 && (ops[0] == "identify") {
			return
		}
		if n == "magick" && len(ops) > 0 && ops[0] == "mogrify" {
			r.add(Delete, "", c, "changes images in place")
			return
		}
		r.add(Change, "", c, "writes an image")
	case "mogrify":
		r.add(Delete, "", c, "changes images in place")
	case "sips":
		if f["-g"] || f["--getProperty"] {
			return
		}
		if f["--out"] || f["-o"] {
			r.add(Change, "", c, "writes an image")
		} else {
			r.add(Delete, "", c, "sips changes images in place")
		}
	case "exiftool":
		if f["-overwrite_original"] || f["-overwrite_original_in_place"] {
			r.add(Delete, "", c, "rewrites files without a backup")
		} else if anyContains(args, "=") || f["-o"] {
			r.add(Change, "", c, "writes metadata")
		}
	case "pandoc", "qpdf", "gs", "soffice", "libreoffice", "tesseract", "sox", "cwebp", "optipng", "jpegoptim", "pdfunite", "pdftotext", "pdftoppm":
		if n == "pdftotext" && len(ops) >= 2 && ops[1] == "-" || n == "pdftotext" && len(ops) == 1 && false {
			return
		}
		if n == "pandoc" && !f["-o"] && !f["--output"] {
			return
		}
		if n == "tesseract" && len(ops) >= 2 && ops[1] == "stdout" {
			return
		}
		r.add(Change, "", c, "writes output files")
	case "yq":
		if f["-i"] || f["--inplace"] {
			r.add(Change, "", c, "edits files in place")
		}
	case "mlr":
		if f["-I"] {
			r.add(Change, "", c, "edits files in place")
		}
	case "sqlite3":
		if sqlWrites(args) {
			r.add(Change, "", c, "changes a database")
		}
	case "tmux":
		if len(ops) > 0 && (ops[0] == "ls" || ops[0] == "list-sessions" || ops[0] == "has-session") {
			return
		}
		r.add(Change, "", c, "changes tmux sessions")
	case "make", "cmake", "ninja", "just", "task":
		r.add(Change, "", c, "runs a build")
	default:
		if readOnly[n] {
			return
		}
		r.add(Change, "", c, "runs %s", n)
	}
}

// commands in readOnly that still need a closer look
var special = set(`sed gsed awk gawk exiftool jq yq tmux ssh`)

func anyPrefix(args []string, p string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, p) {
			return true
		}
	}
	return false
}

func anyContains(args []string, s string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && strings.Contains(a, s) {
			return true
		}
	}
	return false
}

func writeMethod(args []string) bool {
	for i, a := range args {
		if (a == "-X" || a == "--request") && i+1 < len(args) {
			m := strings.ToUpper(args[i+1])
			return m != "GET" && m != "HEAD" && m != "OPTIONS"
		}
		if strings.HasPrefix(a, "-X") && len(a) > 2 {
			m := strings.ToUpper(a[2:])
			return m != "GET" && m != "HEAD" && m != "OPTIONS"
		}
	}
	return false
}

var sqlWrite = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|create|alter|replace|attach|vacuum|pragma\s+\w+\s*=)\b`)

func sqlWrites(args []string) bool {
	for _, a := range args {
		if sqlWrite.MatchString(a) {
			return true
		}
	}
	return false
}

func pipesDownload(st *syntax.Stmt) bool {
	s := shell.Print(st)
	return strings.Contains(s, "curl") || strings.Contains(s, "wget")
}

func classifyFind(r *Result, c *shell.Cmd, env Env) {
	for i, a := range c.Args {
		switch a {
		case "-delete":
			r.add(Delete, "find-delete", c, "find deletes the files it finds")
			return
		case "-exec", "-execdir", "-ok", "-okdir":
			if i+1 < len(c.Args) {
				x := c.Args[i+1]
				switch filepath.Base(x) {
				case "rm", "rmdir", "unlink", "trash":
					r.add(Delete, "find-delete", c, "find deletes the files it finds")
					return
				case "mv":
					r.add(Delete, "", c, "find moves the files it finds")
					return
				}
				if !readOnly[filepath.Base(x)] {
					r.add(Change, "", c, "find runs %s on each file", x)
				}
			}
		}
	}
	for _, o := range shell.Operands(c.Args) {
		_ = o
	}
}

func classifyRclone(r *Result, c *shell.Cmd, ops []string) {
	if len(ops) == 0 {
		return
	}
	switch ops[0] {
	case "ls", "lsd", "lsl", "lsf", "lsjson", "size", "about", "listremotes", "cat", "check", "tree", "version", "config":
	case "sync", "move", "moveto", "delete", "deletefile", "purge", "rmdir", "rmdirs", "cleanup", "dedupe":
		r.add(Delete, "", c, "rclone %s deletes or moves files", ops[0])
	default:
		r.add(Change, "", c, "rclone %s writes files", ops[0])
	}
}

func classifyDocker(r *Result, c *shell.Cmd, ops []string) {
	if len(ops) == 0 {
		return
	}
	sub := ops[0]
	if (sub == "container" || sub == "image" || sub == "volume" || sub == "network" || sub == "system" || sub == "compose") && len(ops) > 1 {
		sub = sub + " " + ops[1]
	}
	switch sub {
	case "ps", "images", "logs", "inspect", "stats", "version", "info", "top", "port", "diff", "history", "search",
		"container ls", "container list", "image ls", "image list", "volume ls", "network ls", "system df", "compose ps", "compose logs", "compose config":
		return
	case "rm", "rmi", "container rm", "image rm", "volume rm", "network rm", "container prune", "image prune", "volume prune", "network prune", "compose down":
		r.add(Delete, "", c, "removes containers, images or volumes")
	case "system prune":
		r.add(Danger, "", c, "removes all unused containers, images and networks")
	default:
		r.add(Change, "", c, "%s %s changes containers", c.Name, sub)
	}
}

func classifyGh(r *Result, c *shell.Cmd, ops []string) {
	if len(ops) < 2 {
		if len(ops) == 1 && (ops[0] == "status" || ops[0] == "version") {
			return
		}
		r.add(Change, "", c, "runs gh %s", strings.Join(ops, " "))
		return
	}
	verb := ops[1]
	switch verb {
	case "list", "view", "status", "diff", "checks", "show", "ls", "download", "browse":
		return
	case "delete":
		if ops[0] == "repo" {
			r.add(Danger, "", c, "deletes a GitHub repository")
			return
		}
		r.add(Delete, "", c, "deletes a GitHub %s", ops[0])
	default:
		r.add(Change, "", c, "gh %s %s changes GitHub", ops[0], verb)
	}
}

// ---------------------------------------------------------------- packages

func pkgSudoOK(c *shell.Cmd) bool {
	ops := shell.Operands(c.Args)
	switch c.Name {
	case "apt", "apt-get":
		return len(ops) > 0 && slices.Contains([]string{"install", "update", "upgrade", "full-upgrade", "dist-upgrade", "remove", "purge", "autoremove"}, ops[0])
	case "pacman":
		return true // the pacman rules below decide the level
	case "dnf":
		return len(ops) > 0 && slices.Contains([]string{"install", "upgrade", "remove", "autoremove"}, ops[0])
	case "dpkg":
		return false
	}
	return false
}

func classifyPkg(r *Result, c *shell.Cmd, ops []string, f map[string]bool) {
	n := c.Name
	sub := ""
	if len(ops) > 0 {
		sub = ops[0]
	}
	switch n {
	case "apt", "apt-get", "aptitude", "dnf":
		switch sub {
		case "search", "show", "list", "policy", "depends", "rdepends", "info", "changelog", "madison", "check-update":
		case "install", "reinstall":
			r.add(Install, "", c, "installs packages")
		case "update", "upgrade", "full-upgrade", "dist-upgrade":
			r.add(Change, "", c, "updates packages")
		case "remove", "purge", "autoremove", "autopurge", "erase":
			r.add(Delete, "pkg-remove", c, "removes packages")
		default:
			r.add(Change, "", c, "%s %s", n, sub)
		}
	case "dpkg":
		switch {
		case f["-l"] || f["-L"] || f["-S"] || f["-s"] || f["--get-selections"] || f["--print-architecture"]:
		case f["-i"]:
			r.add(Install, "", c, "installs a .deb")
		case f["-r"] || f["-P"]:
			r.add(Delete, "pkg-remove", c, "removes packages")
		default:
			r.add(Change, "", c, "dpkg changes packages")
		}
	case "pacman", "yay", "paru":
		op := ""
		for _, a := range c.Args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1 && a[1] >= 'A' && a[1] <= 'Z' {
				op = a
				break
			}
		}
		switch {
		case op == "" && n != "pacman" && len(ops) == 0:
			r.add(Change, "", c, "%s updates the system", n)
		case strings.HasPrefix(op, "-Q") || strings.HasPrefix(op, "-F") || op == "-Ss" || op == "-Si" || op == "-Sl" || op == "-Sg" || op == "-T":
		case strings.HasPrefix(op, "-R"):
			r.add(Delete, "pkg-remove", c, "removes packages")
		case strings.HasPrefix(op, "-S"):
			if strings.Contains(op, "y") && !strings.Contains(op, "u") && len(ops) > 0 {
				r.add(Danger, "", c, "pacman -Sy without -u is a partial upgrade (unsupported on Arch)")
				return
			}
			if strings.Contains(op, "u") {
				r.add(Change, "", c, "upgrades the system")
			} else {
				r.add(Install, "", c, "installs packages")
			}
			if n != "pacman" {
				r.add(Change, "", c, "AUR packages are maintained by users, not Arch")
			}
		case strings.HasPrefix(op, "-U"):
			r.add(Install, "", c, "installs a package file")
		default:
			r.add(Change, "", c, "%s %s", n, op)
		}
	case "brew":
		switch sub {
		case "list", "ls", "info", "search", "outdated", "deps", "uses", "leaves", "config", "doctor", "--prefix", "--cellar", "--version", "desc", "home", "which-formula", "services-list", "log", "cat", "shellenv", "commands":
		case "install", "reinstall":
			r.add(Install, "", c, "installs packages")
		case "update", "upgrade":
			r.add(Change, "", c, "updates Homebrew packages")
		case "uninstall", "remove", "rm", "autoremove", "cleanup", "untap":
			r.add(Delete, "pkg-remove", c, "removes packages")
		case "tap":
			if len(ops) > 1 {
				r.add(Change, "", c, "adds a third-party tap")
			}
		case "services":
			if len(ops) > 1 && (ops[1] == "list" || ops[1] == "info") {
				return
			}
			r.add(Change, "", c, "changes background services")
		default:
			r.add(Change, "", c, "brew %s", sub)
		}
	case "mas":
		switch sub {
		case "list", "search", "info", "outdated":
		case "install":
			r.add(Install, "", c, "installs App Store apps")
		case "uninstall":
			r.add(Delete, "", c, "removes App Store apps")
		default:
			r.add(Change, "", c, "mas %s", sub)
		}
	case "pip", "pip3", "uv", "gem", "npm", "pnpm", "cargo", "go", "port":
		switch sub {
		case "list", "show", "search", "freeze", "info", "outdated", "version", "env", "ls", "view", "why", "tree", "check", "doc", "vet", "fmt-check":
		case "install", "add", "i":
			if n == "cargo" || n == "go" || n == "npm" && len(ops) == 1 || n == "pnpm" && len(ops) == 1 {
				r.add(Change, "", c, "%s %s", n, sub)
				return
			}
			r.add(Install, "", c, "installs packages")
		case "uninstall", "remove", "rm":
			r.add(Delete, "pkg-remove", c, "removes packages")
		default:
			r.add(Change, "", c, "%s %s", n, sub)
		}
	}
}

// ---------------------------------------------------------------- git

var gitGlobalValue = set(`-C -c --git-dir --work-tree --namespace --exec-path`)

// GitSub returns the git subcommand and its args.
func GitSub(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if gitGlobalValue[a] {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a, args[i+1:]
	}
	return "", nil
}

func classifyGit(r *Result, c *shell.Cmd, env Env) {
	sub, args := GitSub(c.Args)
	f := shell.Flags(args)
	ops := shell.Operands(args)
	switch sub {
	case "", "status", "log", "diff", "show", "blame", "shortlog", "describe", "rev-parse", "rev-list", "ls-files",
		"ls-tree", "ls-remote", "grep", "reflog", "whatchanged", "cat-file", "for-each-ref", "show-ref", "help",
		"version", "count-objects", "merge-base", "name-rev", "cherry", "range-diff", "var", "check-ignore", "fsck":
		if sub == "reflog" && len(ops) > 0 && (ops[0] == "expire" || ops[0] == "delete") {
			r.add(Danger, "", c, "removes reflog entries (recovery data)")
		}
		return
	case "fetch":
		if f["--prune"] || f["-p"] {
			r.add(Change, "", c, "fetches and prunes remote branches")
			return
		}
		r.add(Change, "", c, "fetches from the remote")
	case "branch":
		switch {
		case f["-D"] || f["-d"] && f["-f"] || f["--delete"] && f["--force"]:
			r.add(Delete, "git-branch-D", c, "deletes a branch even if it is not merged")
		case f["-d"] || f["--delete"]:
			r.add(Change, "", c, "deletes a merged branch")
		case f["-m"] || f["-M"] || f["-c"] || f["-C"] || f["-u"] || f["--set-upstream-to"] || len(ops) > 0:
			r.add(Change, "", c, "changes branches")
		}
	case "tag":
		switch {
		case f["-d"] || f["--delete"]:
			r.add(Delete, "", c, "deletes a tag")
		case f["-l"] || f["--list"] || len(ops) == 0:
		default:
			r.add(Change, "", c, "creates a tag")
		}
	case "remote":
		if len(ops) == 0 || ops[0] == "show" || ops[0] == "get-url" || f["-v"] {
			return
		}
		r.add(Change, "", c, "changes remotes")
	case "stash":
		if len(ops) > 0 {
			switch ops[0] {
			case "list", "show":
				return
			case "drop", "clear":
				r.add(Delete, "", c, "drops stashed changes")
				return
			case "pop", "apply":
				r.add(Change, "", c, "applies stashed changes")
				return
			}
		}
		r.add(Change, "", c, "stashes changes")
	case "clean":
		if f["-n"] || f["--dry-run"] {
			return
		}
		if f["-x"] || f["-X"] {
			r.add(Danger, "git-clean", c, "git clean -x also deletes ignored files (build output, .env, …)")
			return
		}
		r.add(Delete, "git-clean", c, "deletes untracked files")
	case "reset":
		if f["--hard"] || f["--merge"] {
			r.add(Danger, "git-reset", c, "git reset --hard throws away uncommitted changes")
			return
		}
		r.add(Change, "", c, "moves the branch / unstages")
	case "checkout", "restore", "switch":
		if f["-f"] || f["--force"] || f["--discard-changes"] || sub == "checkout" && slices.Contains(args, "--") || sub == "restore" && !f["--staged"] && !f["-S"] || sub == "checkout" && slices.Contains(ops, ".") {
			r.add(Danger, "git-reset", c, "throws away uncommitted changes in files")
			return
		}
		if (sub == "checkout" || sub == "switch") && (f["-B"] || f["-C"] || f["--force-create"]) {
			r.add(Danger, "", c, "resets an existing branch")
			return
		}
		r.add(Change, "", c, "switches branch")
	case "push":
		switch {
		case f["--force"] || f["-f"] || anyPlusRefspec(ops):
			r.add(Danger, "git-push-force", c, "force-push overwrites commits on the remote")
		case f["--force-with-lease"] || f["--force-if-includes"]:
			r.add(Delete, "git-push-force", c, "force-push (with lease) replaces commits on the remote")
		case f["--delete"] || f["-d"] || anyColonDelete(ops):
			r.add(Delete, "", c, "deletes a remote branch")
		case f["--mirror"] || f["--prune"]:
			r.add(Danger, "", c, "push --mirror/--prune deletes remote refs")
		default:
			r.add(Change, "", c, "pushes commits")
		}
	case "rebase":
		if f["--abort"] || f["--continue"] || f["--skip"] && false {
			r.add(Change, "", c, "rebase %s", strings.Join(args, " "))
			return
		}
		r.add(Delete, "", c, "rebase rewrites commits")
	case "commit":
		if f["--amend"] {
			r.add(Change, "", c, "rewrites the last commit")
			return
		}
		r.add(Change, "", c, "creates a commit")
	case "filter-branch", "filter-repo", "replace", "update-ref", "prune", "gc", "worktree":
		if sub == "worktree" && len(ops) > 0 && ops[0] == "list" {
			return
		}
		if sub == "gc" {
			r.add(Change, "", c, "garbage-collects the repository")
			return
		}
		r.add(Danger, "", c, "git %s rewrites or removes history", sub)
	case "config":
		if f["--get"] || f["--list"] || f["-l"] || f["--get-all"] || f["--show-origin"] || len(ops) <= 1 && !f["--unset"] {
			return
		}
		if f["--global"] || f["--system"] {
			r.add(Change, "", c, "changes your global git config")
			return
		}
		r.add(Change, "", c, "changes the repo's git config")
	case "rm", "mv":
		if f["--cached"] {
			r.add(Change, "", c, "untracks files")
			return
		}
		r.add(Delete, map[string]string{"rm": "rm", "mv": "mv"}[sub], c, "git %s deletes or moves files", sub)
	case "bisect", "cherry-pick", "revert", "merge", "pull", "am", "apply", "add", "init", "clone", "submodule", "notes", "sparse-checkout", "lfs":
		r.add(Change, "", c, "git %s changes the repository", sub)
	default:
		r.add(Change, "", c, "git %s", sub)
	}
}

func anyPlusRefspec(ops []string) bool {
	for _, o := range ops[min(1, len(ops)):] {
		if strings.HasPrefix(o, "+") {
			return true
		}
	}
	return false
}

func anyColonDelete(ops []string) bool {
	for _, o := range ops {
		if strings.HasPrefix(o, ":") && len(o) > 1 {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- az

var azDanger = map[string]string{
	"group delete":                    "deletes a whole resource group and everything in it",
	"lock delete":                     "removes a resource lock",
	"ad app credential reset":         "replaces an app's credentials",
	"ad sp credential reset":          "replaces a service principal's credentials",
	"ad sp create-for-rbac":           "creates a service principal with a role",
	"vm run-command invoke":           "runs commands inside a VM",
	"vm run-command create":           "runs commands inside a VM",
	"vmss run-command invoke":         "runs commands inside VMs",
	"keyvault secret show":            "prints a secret",
	"keyvault secret download":        "writes a secret to a file",
	"storage account keys list":       "prints storage account keys",
	"storage account delete":          "deletes a storage account and all its data",
	"sql server delete":               "deletes a SQL server and its databases",
	"sql db delete":                   "deletes a database",
	"cosmosdb delete":                 "deletes a Cosmos DB account",
	"aks delete":                      "deletes a Kubernetes cluster",
	"keyvault delete":                 "deletes a key vault",
	"account management-group delete": "deletes a management group",
	"policy assignment delete":        "removes a policy assignment",
	"deployment sub create":           "deploys at subscription scope",
	"deployment mg create":            "deploys at management-group scope",
	"deployment tenant create":        "deploys at tenant scope",
	"role definition delete":          "deletes a role definition",
	"logout":                          "logs out of Azure",
}

func classifyAz(r *Result, c *shell.Cmd, env Env) {
	args := c.Args
	if env.Az == nil {
		r.add(Change, "", c, "az command (no catalog to check it)")
		return
	}
	name, cmd, rest := env.Az.Resolve(args)
	if cmd == nil {
		lw := strings.Join(shell.Operands(args), " ")
		if lw == "" || strings.HasPrefix(lw, "version") || env.Az.Group(lw) {
			return
		}
		r.add(Change, "", c, "unknown az command")
		return
	}
	if why, ok := azDanger[name]; ok {
		r.add(Danger, kindAz(name), c, "az %s %s", name, why)
		return
	}
	if strings.HasSuffix(name, " purge") || strings.Contains(name, " purge-") {
		r.add(Danger, "", c, "az %s permanently purges deleted resources", name)
		return
	}
	if strings.HasPrefix(name, "role assignment ") && (strings.HasSuffix(name, " create") || strings.HasSuffix(name, " delete")) {
		scope := optValue(rest, "--scope")
		role := strings.ToLower(optValue(rest, "--role"))
		if scope == "" || strings.Count(strings.Trim(scope, "/"), "/") <= 1 || strings.Contains(scope, "managementGroups") ||
			role == "owner" || role == "contributor" || role == "user access administrator" {
			r.add(Danger, "", c, "az %s with a broad scope or a powerful role", name)
			return
		}
	}
	switch cmd.Risk {
	case "read":
		return
	case "del":
		for _, p := range env.ProtectedSubs {
			if p != "" && (strings.EqualFold(p, env.CurrentSub) || strings.EqualFold(p, optValue(rest, "--subscription"))) {
				r.add(Danger, "az-delete", c, "az %s in protected subscription %s", name, p)
				return
			}
		}
		r.add(Delete, "az-delete", c, "az %s", name)
	default:
		r.add(Change, "", c, "az %s", name)
	}
}

func kindAz(name string) string {
	if strings.HasSuffix(name, " delete") {
		return "az-delete"
	}
	return ""
}

func optValue(args []string, opt string) string {
	for i, a := range args {
		if a == opt && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, opt+"=") {
			return a[len(opt)+1:]
		}
	}
	return ""
}

// AzName returns the resolved az command name ("vm list") for a Cmd.
func AzName(c *shell.Cmd, cat *azcat.Catalog) string {
	if cat == nil {
		return ""
	}
	n, _, _ := cat.Resolve(c.Args)
	return n
}

// OptValue is exported for the dry run.
func OptValue(args []string, opt ...string) string {
	for _, o := range opt {
		if v := optValue(args, o); v != "" {
			return v
		}
	}
	return ""
}
