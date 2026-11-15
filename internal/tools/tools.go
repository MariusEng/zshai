// Package tools is the common-tools catalog (PLAN.md §3.2): which binary a tool
// installs, its package name per package manager, and how to install it.
package tools

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Tool is one common tool. Pkg maps manager -> package ("" = not available there).
// Bin maps manager -> binary name when it differs (apt: fd -> fdfind).
type Tool struct {
	Name string
	Area string
	Pkg  map[string]string
	Bin  map[string]string
	Cask bool   // brew: install with --cask
	URL  string // project page when no package exists
}

func t(name, area string, pkg ...string) Tool {
	m := map[string]string{}
	for i := 0; i+1 < len(pkg); i += 2 {
		m[pkg[i]] = pkg[i+1]
	}
	return Tool{Name: name, Area: area, Pkg: m}
}

func same(name, area string) Tool {
	return t(name, area, "apt", name, "pacman", name, "brew", name, "dnf", name)
}

// All is the catalog. Shipped tools (ls, grep, ...) are not listed.
var All = func() map[string]Tool {
	list := []Tool{
		same("jq", "data"),
		t("yq", "data", "apt", "yq", "pacman", "go-yq", "brew", "yq", "dnf", "yq"),
		t("mlr", "data", "apt", "miller", "pacman", "miller", "brew", "miller", "dnf", "miller"),
		t("sqlite3", "data", "apt", "sqlite3", "pacman", "sqlite", "brew", "sqlite", "dnf", "sqlite"),
		t("rg", "search", "apt", "ripgrep", "pacman", "ripgrep", "brew", "ripgrep", "dnf", "ripgrep"),
		withBin(t("fd", "search", "apt", "fd-find", "pacman", "fd", "brew", "fd", "dnf", "fd-find"), "apt", "fdfind"),
		same("fzf", "search"),
		withBin(same("bat", "search"), "apt", "batcat"),
		same("eza", "search"),
		same("tree", "search"),
		same("ncdu", "search"),
		withBin(t("7z", "archive", "apt", "7zip", "pacman", "7zip", "brew", "sevenzip", "dnf", "7zip"), "brew", "7zz"),
		same("zip", "archive"), same("unzip", "archive"),
		t("xz", "archive", "apt", "xz-utils", "pacman", "xz", "brew", "xz", "dnf", "xz"),
		same("zstd", "archive"), same("rsync", "archive"), same("rclone", "archive"),
		same("wget", "network"),
		t("http", "network", "apt", "httpie", "pacman", "httpie", "brew", "httpie", "dnf", "httpie"),
		t("dig", "network", "apt", "bind9-dnsutils", "pacman", "bind", "brew", "bind", "dnf", "bind-utils"),
		same("whois", "network"), same("mtr", "network"),
		t("nc", "network", "apt", "netcat-openbsd", "pacman", "openbsd-netcat", "brew", "netcat", "dnf", "nmap-ncat"),
		same("yt-dlp", "network"),
		t("magick", "images", "apt", "imagemagick", "pacman", "imagemagick", "brew", "imagemagick", "dnf", "ImageMagick"),
		t("exiftool", "images", "apt", "libimage-exiftool-perl", "pacman", "perl-image-exiftool", "brew", "exiftool", "dnf", "perl-Image-ExifTool"),
		same("optipng", "images"), same("jpegoptim", "images"),
		t("cwebp", "images", "apt", "webp", "pacman", "libwebp", "brew", "webp", "dnf", "libwebp-tools"),
		same("ffmpeg", "media"),
		t("ffprobe", "media", "apt", "ffmpeg", "pacman", "ffmpeg", "brew", "ffmpeg", "dnf", "ffmpeg"),
		same("sox", "media"), same("mediainfo", "media"),
		same("pandoc", "docs"),
		t("pdftotext", "docs", "apt", "poppler-utils", "pacman", "poppler", "brew", "poppler", "dnf", "poppler-utils"),
		t("pdfinfo", "docs", "apt", "poppler-utils", "pacman", "poppler", "brew", "poppler", "dnf", "poppler-utils"),
		t("pdfunite", "docs", "apt", "poppler-utils", "pacman", "poppler", "brew", "poppler", "dnf", "poppler-utils"),
		same("qpdf", "docs"),
		t("gs", "docs", "apt", "ghostscript", "pacman", "ghostscript", "brew", "ghostscript", "dnf", "ghostscript"),
		cask(t("soffice", "docs", "apt", "libreoffice", "pacman", "libreoffice-fresh", "brew", "libreoffice", "dnf", "libreoffice")),
		t("tesseract", "docs", "apt", "tesseract-ocr", "pacman", "tesseract", "brew", "tesseract", "dnf", "tesseract"),
		same("git", "dev"),
		t("gh", "dev", "apt", "gh", "pacman", "github-cli", "brew", "gh", "dnf", "gh"),
		t("az", "dev", "apt", "azure-cli", "pacman", "azure-cli", "brew", "azure-cli", "dnf", "azure-cli"),
		same("tmux", "dev"), same("htop", "dev"), same("btop", "dev"),
		t("watch", "dev", "apt", "procps", "pacman", "procps-ng", "brew", "watch", "dnf", "procps-ng"),
		t("tldr", "dev", "apt", "tealdeer", "pacman", "tealdeer", "brew", "tealdeer", "dnf", "tealdeer"),
		t("docker", "dev", "apt", "docker.io", "pacman", "docker", "brew", "docker", "dnf", "moby-engine"),
		same("podman", "dev"),
		cask(t("code", "dev", "brew", "visual-studio-code")),
		same("fastfetch", "dev"),
		t("cargo", "project", "pacman", "rust", "brew", "rust", "dnf", "cargo"),
		t("go", "project", "apt", "golang-go", "pacman", "go", "brew", "go", "dnf", "golang"),
		t("npm", "project", "apt", "npm", "pacman", "npm", "brew", "node", "dnf", "npm"),
		same("pnpm", "project"), same("uv", "project"),
		t("gdate", "gnu", "brew", "coreutils"), t("gsed", "gnu", "brew", "gnu-sed"),
		t("gawk", "gnu", "apt", "gawk", "pacman", "gawk", "brew", "gawk"), t("gfind", "gnu", "brew", "findutils"),
	}
	m := map[string]Tool{}
	for _, x := range list {
		m[x.Name] = x
	}
	// alternate binary names point at the same tool
	m["fdfind"] = m["fd"]
	m["batcat"] = m["bat"]
	m["7zz"] = m["7z"]
	m["convert"] = m["magick"]
	m["mogrify"] = m["magick"]
	m["libreoffice"] = m["soffice"]
	return m
}()

func withBin(x Tool, mgr, bin string) Tool {
	if x.Bin == nil {
		x.Bin = map[string]string{}
	}
	x.Bin[mgr] = bin
	return x
}

func cask(x Tool) Tool { x.Cask = true; return x }

// Lookup returns the tool for a binary name.
func Lookup(bin string) (Tool, bool) { x, ok := All[bin]; return x, ok }

// BinFor is the binary the tool installs with this manager.
func (x Tool) BinFor(mgr string) string {
	if b, ok := x.Bin[mgr]; ok {
		return b
	}
	return x.Name
}

// Installed lists the catalog tools found on PATH, using their real binary names.
func Installed() []string {
	seen := map[string]bool{}
	var out []string
	for bin := range All {
		if seen[bin] {
			continue
		}
		if _, err := exec.LookPath(bin); err == nil {
			seen[bin] = true
			out = append(out, bin)
		}
	}
	return out
}

// Manager detects the package manager (PLAN.md §2.6).
func Manager(pref string) string {
	if pref != "" && pref != "auto" {
		return pref
	}
	if runtime.GOOS == "darwin" {
		if BrewPrefix() != "" {
			return "brew"
		}
		return ""
	}
	id, like := osRelease()
	all := id + " " + like
	switch {
	case strings.Contains(all, "arch") || strings.Contains(all, "manjaro") || strings.Contains(all, "endeavouros"):
		return "pacman"
	case strings.Contains(all, "debian") || strings.Contains(all, "ubuntu"):
		return "apt"
	case strings.Contains(all, "fedora") || strings.Contains(all, "rhel"):
		return "dnf"
	}
	for _, m := range []string{"apt-get", "pacman", "dnf"} {
		if _, err := exec.LookPath(m); err == nil {
			return strings.TrimSuffix(m, "-get")
		}
	}
	return ""
}

func BrewPrefix() string {
	for _, p := range []string{os.Getenv("HOMEBREW_PREFIX"), "/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"} {
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p + "/bin/brew"); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

func osRelease() (id, like string) {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		switch k {
		case "ID":
			id = strings.ToLower(v)
		case "ID_LIKE":
			like = strings.ToLower(v)
		}
	}
	return
}

// InstallCommand builds the install command for a tool, or "" if the manager has no package.
func InstallCommand(x Tool, mgr string) string {
	pkg := x.Pkg[mgr]
	if pkg == "" {
		return ""
	}
	switch mgr {
	case "apt":
		return "sudo apt install -y " + pkg
	case "pacman":
		return "sudo pacman -S --needed --noconfirm " + pkg
	case "brew":
		if x.Cask {
			return "brew install --cask " + pkg
		}
		return "brew install " + pkg
	case "dnf":
		return "sudo dnf install -y " + pkg
	}
	return ""
}
