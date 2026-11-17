package localmodel

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/MariusEng/zshai/internal/config"
)

// The zshai-mini sizes users can choose from. The files live in one GitHub release
// (ModelsTag), separate from the program, so a program update doesn't mean another
// multi-GB download. A model over GitHub's 2 GiB per-file limit is split with llama.cpp's
// gguf-split (…-00001-of-00003.gguf); llama-server loads the first part and finds the rest.
const (
	ModelsTag  = "models-v3"
	ModelsBase = "https://github.com/MariusEng/zshai/releases/download/" + ModelsTag + "/"
)

type Spec struct {
	Name  string // "1.5b"
	File  string // the file, or the first part of a split model
	Parts int
	Size  string // download size, for the list
	Note  string
}

var Catalog = []Spec{
	{Name: "1.5b", File: "zshai-mini-1.5b-v3-Q4_K_M.gguf", Parts: 1, Size: "1.0 GB", Note: "fast on any machine"},
	{Name: "3b", File: "zshai-mini-3b-v4-Q4_K_M.gguf", Parts: 1, Size: "1.9 GB", Note: "best; fast on Apple Silicon"},
}

// Preference when the config doesn't name a model: the best one that is quick here.
func preference() []string {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return []string{"3b", "1.5b", "0.5b"}
	}
	return []string{"1.5b", "3b", "0.5b"}
}

func Lookup(name string) (Spec, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, s := range Catalog {
		if s.Name == name || s.Name == strings.TrimSuffix(name, "-") || "zshai-mini-"+s.Name == name {
			return s, true
		}
	}
	return Spec{}, false
}

var nameRe = regexp.MustCompile(`^zshai-mini-(\d+(?:\.\d+)?b)-.*?(?:-(\d{5})-of-(\d{5}))?\.gguf$`)

// Installed maps a size ("1.5b") to the newest file of it in the data folders (the first
// part, for a split model). Round-1 files (zshai-mini-0.5b-v2-…) count as "0.5b".
func Installed() map[string]string {
	out := map[string]string{}
	for _, d := range DataDirs() {
		ms, _ := filepath.Glob(filepath.Join(d, "models", "zshai-mini*.gguf"))
		sort.Strings(ms)
		for _, m := range ms {
			sub := nameRe.FindStringSubmatch(filepath.Base(m))
			if sub == nil || (sub[2] != "" && sub[2] != "00001") {
				continue
			}
			if _, ok := out[sub[1]]; !ok || filepath.Dir(out[sub[1]]) == filepath.Dir(m) {
				out[sub[1]] = m // sorted: the newest version in a folder wins
			}
		}
	}
	return out
}

// Pick is the model file to run: the config's "localModel", else the preferred installed one.
func Pick() (name, path string, err error) {
	inst := Installed()
	if want := strings.ToLower(config.Load().LocalModel); want != "" {
		if p, ok := inst[want]; ok {
			return want, p, nil
		}
		return "", "", fmt.Errorf("the %s model is not installed (zshai model install %s)", want, want)
	}
	for _, n := range preference() {
		if p, ok := inst[n]; ok {
			return n, p, nil
		}
	}
	for n, p := range inst {
		return n, p, nil
	}
	return "", "", errors.New("no zshai-mini model is installed (zshai model install 1.5b)")
}

// List is `zshai model list` / `zai: model`.
func List() string {
	inst := Installed()
	active, _, _ := Pick()
	var b strings.Builder
	b.WriteString("zshai-mini models (zshai model install <size> · zshai model use <size>):\n")
	seen := map[string]bool{}
	for _, s := range Catalog {
		seen[s.Name] = true
		mark, state := "  ", "not installed"
		if p, ok := inst[s.Name]; ok {
			state = "installed: " + filepath.Base(p)
		}
		if s.Name == active {
			mark = "▸ "
		}
		fmt.Fprintf(&b, "%s%-5s %-7s %-44s %s\n", mark, s.Name, s.Size, s.Note, state)
	}
	for n, p := range inst {
		if !seen[n] {
			mark := "  "
			if n == active {
				mark = "▸ "
			}
			fmt.Fprintf(&b, "%s%-5s %-7s %-44s installed: %s\n", mark, n, "", "older model", filepath.Base(p))
		}
	}
	return b.String()
}

// installDir: where downloaded models go (~/.local/share/zshai/models).
func installDir() string {
	ds := DataDirs()
	return filepath.Join(ds[len(ds)-1], "models")
}

func partName(s Spec, i int) string {
	if s.Parts <= 1 {
		return s.File
	}
	return strings.Replace(s.File, "-00001-of-", fmt.Sprintf("-%05d-of-", i), 1)
}

// Install downloads a model from the models release, checks it against the release's
// SHA256SUMS, and moves it into place. Progress goes to w.
func Install(name string, w io.Writer) error {
	s, ok := Lookup(name)
	if !ok {
		return fmt.Errorf("unknown model %q (choose 1.5b or 3b)", name)
	}
	sums, err := fetchSums()
	if err != nil {
		return err
	}
	dir := installDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i := 1; i <= s.Parts; i++ {
		f := partName(s, i)
		want, ok := sums[f]
		if !ok {
			return fmt.Errorf("%s is not in the %s release", f, ModelsTag)
		}
		dst := filepath.Join(dir, f)
		if got, err := fileSum(dst); err == nil && got == want {
			fmt.Fprintf(w, "%s: already installed\n", f)
			continue
		}
		if err := download(ModelsBase+f, dst, want, w); err != nil {
			return err
		}
	}
	return nil
}

func fetchSums() (map[string]string, error) {
	c := http.Client{Timeout: 30 * time.Second}
	r, err := c.Get(ModelsBase + "SHA256SUMS")
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("can't get the model list (%s): %s", ModelsBase+"SHA256SUMS", r.Status)
	}
	out := map[string]string{}
	sc := bufio.NewScanner(r.Body)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 {
			out[strings.TrimPrefix(f[1], "*")] = f[0]
		}
	}
	return out, sc.Err()
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type progress struct {
	w           io.Writer
	name        string
	done, total int64
	last        time.Time
}

func (p *progress) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if time.Since(p.last) > 300*time.Millisecond || p.done == p.total {
		p.last = time.Now()
		if p.total > 0 {
			fmt.Fprintf(p.w, "\r%s: %5.1f%% of %.1f GB", p.name, 100*float64(p.done)/float64(p.total), float64(p.total)/1e9)
		} else {
			fmt.Fprintf(p.w, "\r%s: %.1f GB", p.name, float64(p.done)/1e9)
		}
	}
	return len(b), nil
}

func download(url, dst, want string, w io.Writer) error {
	r, err := http.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("%s: %s", url, r.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	p := &progress{w: w, name: filepath.Base(dst), total: r.ContentLength}
	_, err = io.Copy(io.MultiWriter(f, h, p), r.Body)
	f.Close()
	fmt.Fprintln(w)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(tmp)
		return fmt.Errorf("%s: checksum mismatch (download damaged?)", filepath.Base(dst))
	}
	return os.Rename(tmp, dst)
}

// Remove deletes an installed model's files.
func Remove(name string) error {
	inst := Installed()
	p, ok := inst[strings.ToLower(name)]
	if !ok {
		return fmt.Errorf("the %s model is not installed", name)
	}
	if sub := nameRe.FindStringSubmatch(filepath.Base(p)); sub != nil && sub[3] != "" {
		ms, _ := filepath.Glob(strings.Replace(p, "-00001-of-", "-*-of-", 1))
		for _, m := range ms {
			os.Remove(m)
		}
		return nil
	}
	return os.Remove(p)
}
