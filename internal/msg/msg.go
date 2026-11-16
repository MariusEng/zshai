// Package msg builds every user-facing text (Norwegian Bokmål / English) from
// facts, never from the model's words (PLAN.md §2.2–2.3).
package msg

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MariusEng/zshai/internal/dryrun"
	"github.com/MariusEng/zshai/internal/risk"
)

type L struct{ No bool }

func New(lang string) L { return L{No: lang == "no"} }

func (l L) t(no, en string) string {
	if l.No {
		return no
	}
	return en
}

// Size formats bytes in the user's language: 3,4 MB / 3.4 MB.
func (l L) Size(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	s := strconv.FormatFloat(float64(b)/float64(div), 'f', 1, 64)
	if l.No {
		s = strings.Replace(s, ".", ",", 1)
	}
	return fmt.Sprintf("%s %cB", s, "KMGTPE"[exp])
}

var monthsNo = []string{"jan.", "feb.", "mars", "april", "mai", "juni", "juli", "aug.", "sep.", "okt.", "nov.", "des."}

// Dates renders a modification-date range: "endret 3.–10. nov." / "changed Nov 3–10".
func (l L) Dates(a, b time.Time) string {
	if a.IsZero() {
		return ""
	}
	sameDay := a.YearDay() == b.YearDay() && a.Year() == b.Year()
	sameMonth := a.Month() == b.Month() && a.Year() == b.Year()
	if l.No {
		m := func(t time.Time) string { return monthsNo[t.Month()-1] }
		switch {
		case sameDay:
			return fmt.Sprintf("endret %d. %s", a.Day(), m(a))
		case sameMonth:
			return fmt.Sprintf("endret %d.–%d. %s", a.Day(), b.Day(), m(b))
		default:
			return fmt.Sprintf("endret %d. %s–%d. %s", a.Day(), m(a), b.Day(), m(b))
		}
	}
	switch {
	case sameDay:
		return "changed " + a.Format("Jan 2")
	case sameMonth:
		return fmt.Sprintf("changed %s–%d", a.Format("Jan 2"), b.Day())
	default:
		return fmt.Sprintf("changed %s–%s", a.Format("Jan 2"), b.Format("Jan 2"))
	}
}

func Tilde(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func (l L) files(n int) string {
	if l.No {
		if n == 1 {
			return "1 fil"
		}
		return fmt.Sprintf("%d filer", n)
	}
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// commonDir is the folder shared by the items (for "i ~/Downloads").
func commonDir(items []dryrun.Item, cwd string) string {
	if len(items) == 0 {
		return ""
	}
	abs := func(p string) string {
		if strings.HasPrefix(p, "~/") {
			home, _ := os.UserHomeDir()
			return filepath.Join(home, p[2:])
		}
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(cwd, p)
	}
	d := filepath.Dir(abs(items[0].Path))
	for _, it := range items[1:] {
		for !strings.HasPrefix(filepath.Dir(abs(it.Path))+"/", d+"/") && d != "/" {
			d = filepath.Dir(d)
		}
	}
	return d
}

// place shows a folder relative to the cwd: "her"/"here", "logs", or ~/x.
func (l L) place(d, cwd string) string {
	switch {
	case d == "" || d == cwd:
		return l.t("her", "here")
	case strings.HasPrefix(d, cwd+"/"):
		return strings.TrimPrefix(d, cwd+"/")
	}
	return Tilde(d)
}

func (l L) summary(p *dryrun.Plan) string {
	parts := []string{l.Size(p.Bytes)}
	if d := l.Dates(p.Oldest, p.Newest); d != "" {
		parts = append(parts, d)
	}
	what := l.files(p.Files)
	if p.Dirs > 0 && p.Files != p.Dirs {
		what += l.t(fmt.Sprintf(" i %d mapper", p.Dirs), fmt.Sprintf(" in %d folders", p.Dirs))
		if p.Dirs == 1 {
			what = strings.Replace(what, "1 mapper", "1 mappe", 1)
			what = strings.Replace(what, "1 folders", "1 folder", 1)
		}
	}
	return fmt.Sprintf("%s (%s)", what, strings.Join(parts, ", "))
}

// Question is the confirm question for a dry-run plan.
func (l L) Question(p *dryrun.Plan, lvl risk.Level, cwd, sub string) string {
	if p == nil {
		return ""
	}
	if p.Unknown {
		why := p.Why
		if p.Remote != "" {
			return l.t(fmt.Sprintf("Dette sletter eller flytter filer på %s, og zshai kan ikke se hvilke på forhånd.", p.Remote),
				fmt.Sprintf("This deletes or moves files on %s, and zshai can't see which ones beforehand.", p.Remote))
		}
		return l.t("zshai kan ikke se på forhånd hva dette sletter eller flytter ("+why+").",
			"zshai can't tell beforehand what this deletes or moves ("+why+").")
	}
	dir := commonDir(p.Items, cwd)
	switch p.Kind {
	case "rm", "find-delete", "xargs-rm", "git-clean":
		if p.Files == 0 && len(p.Items) == 0 {
			return l.t("Ingen filer passer, så ingenting blir slettet.", "No files match, so nothing would be deleted.")
		}
		where := " " + l.place(dir, cwd)
		if where != " "+l.t("her", "here") {
			where = l.t(" i ", " in ") + l.place(dir, cwd)
		}
		tail := l.t(" De legges i papirkurven.", " They go to the trash.")
		if !p.Trashable {
			tail = l.t(" De slettes permanent (ikke papirkurv).", " They are deleted permanently (not to the trash).")
		}
		q := l.t("Skal jeg slette ", "Shall I delete ") + l.summary(p) + where + "?" + tail
		if len(p.Missing) > 0 {
			q += l.t(fmt.Sprintf(" (%d finnes ikke)", len(p.Missing)), fmt.Sprintf(" (%d don't exist)", len(p.Missing)))
		}
		return q
	case "mv":
		if p.Facts["rename"] == "1" && len(p.Items) == 1 {
			return l.t(fmt.Sprintf("Skal jeg gi %s nytt navn: %s?", p.Items[0].Path, p.Dest),
				fmt.Sprintf("Shall I rename %s to %s?", p.Items[0].Path, p.Dest))
		}
		var q string
		if len(p.Items) == 1 && !p.Items[0].IsDir {
			q = l.t("Skal jeg flytte ", "Shall I move ") + p.Items[0].Path + " (" + l.Size(p.Bytes) + ")" + l.t(" til ", " to ") + Tilde(p.Dest) + "?"
		} else {
			from := l.t(" fra ", " from ") + l.place(dir, cwd)
			if l.place(dir, cwd) == l.t("her", "here") {
				from = l.t(" herfra", " from here")
			}
			q = l.t("Skal jeg flytte ", "Shall I move ") + l.summary(p) + from + l.t(" til ", " to ") + Tilde(p.Dest) + "?"
		}
		if len(p.Conflicts) > 0 {
			q += l.t(fmt.Sprintf(" %d finnes allerede i målmappen og blir erstattet.", len(p.Conflicts)),
				fmt.Sprintf(" %d already exist at the destination and would be replaced.", len(p.Conflicts)))
		}
		return q
	case "truncate":
		return l.t(fmt.Sprintf("Dette overskriver %s (%s).", p.Items[0].Path, l.Size(p.Bytes)),
			fmt.Sprintf("This overwrites %s (%s).", p.Items[0].Path, l.Size(p.Bytes)))
	case "git-reset":
		var parts []string
		if len(p.Lines) > 0 {
			list := strings.Join(p.Lines[:min(4, len(p.Lines))], ", ")
			if len(p.Lines) > 4 {
				list += ", …"
			}
			parts = append(parts, l.t(fmt.Sprintf("forkaster endringene i %s (%s)", l.files(len(p.Lines)), list),
				fmt.Sprintf("throws away the changes in %s (%s)", l.files(len(p.Lines)), list)))
		}
		if n, _ := strconv.Atoi(p.Facts["commits"]); n > 0 {
			un, _ := strconv.Atoi(p.Facts["unpushed"])
			parts = append(parts, l.t(fmt.Sprintf("fjerner %d commits fra branchen (%d av dem er ikke pushet)", n, un),
				fmt.Sprintf("removes %d commits from the branch (%d of them not pushed)", n, un)))
		}
		if len(parts) == 0 {
			return l.t("Ingen ulagrede endringer går tapt.", "No uncommitted changes would be lost.")
		}
		return l.t("Dette ", "This ") + strings.Join(parts, l.t(" og ", " and ")) + l.t(". zshai tar en backup først, så «angre» virker.", ". zshai saves a backup first, so undo works.")
	case "git-branch-D":
		var bs []string
		for _, ln := range p.Lines {
			br, n, _ := strings.Cut(ln, "\t")
			bs = append(bs, l.t(fmt.Sprintf("%s (%s commits som ikke finnes andre steder)", br, n), fmt.Sprintf("%s (%s commits not on any other branch)", br, n)))
		}
		return l.t("Skal jeg slette ", "Shall I delete ") + strings.Join(bs, ", ") + l.t("? zshai lagrer tuppen som backup.", "? zshai keeps the tip as a backup.")
	case "git-push-force":
		return l.t(fmt.Sprintf("Force-push erstatter %s commits på remote som du ikke har lokalt, og sender %s.", p.Facts["overwritten"], p.Facts["pushing"]),
			fmt.Sprintf("Force-push replaces %s commits on the remote that you don't have locally, and sends %s.", p.Facts["overwritten"], p.Facts["pushing"]))
	case "az-delete":
		if p.Facts["cmd"] == "group delete" {
			list := strings.Join(p.Lines[:min(5, len(p.Lines))], ", ")
			if len(p.Lines) > 5 {
				list += ", …"
			}
			if p.Files == 0 {
				list = l.t("tom", "empty")
			}
			return l.t(fmt.Sprintf("Skal jeg slette ressursgruppen %s (%d ressurser: %s) i abonnementet «%s»? Dette kan ikke angres.", p.Facts["group"], p.Files, list, sub),
				fmt.Sprintf("Shall I delete resource group %s (%d resources: %s) in subscription %q? This can't be undone.", p.Facts["group"], p.Files, list, sub))
		}
		target := p.Facts["target"]
		if p.Facts["group"] != "" {
			target += l.t(" i ", " in ") + p.Facts["group"]
		}
		return l.t(fmt.Sprintf("Skal jeg kjøre az %s på %s i abonnementet «%s»? Dette kan ikke angres med zshai.", p.Facts["cmd"], target, sub),
			fmt.Sprintf("Shall I run az %s on %s in subscription %q? zshai can't undo this.", p.Facts["cmd"], target, sub))
	case "pkg-remove":
		list := strings.Join(p.Lines[:min(8, len(p.Lines))], ", ")
		if len(p.Lines) > 8 {
			list += fmt.Sprintf(" … (+%d)", len(p.Lines)-8)
		}
		return l.t(fmt.Sprintf("Skal jeg fjerne %d pakker: %s?", len(p.Lines), list), fmt.Sprintf("Shall I remove %d packages: %s?", len(p.Lines), list))
	case "rsync-delete":
		return l.t(fmt.Sprintf("rsync sletter %d filer i målet.", p.Files), fmt.Sprintf("rsync deletes %d files at the destination.", p.Files))
	}
	return ""
}

// Label is the tag after a command: [lese] / [read].
func (l L) Label(lvl risk.Level) string {
	no := map[risk.Level]string{risk.Read: "lese", risk.Change: "endring", risk.Install: "installering", risk.Delete: "flytt/slett", risk.Danger: "farlig"}
	en := map[risk.Level]string{risk.Read: "read", risk.Change: "change", risk.Install: "install", risk.Delete: "move/delete", risk.Danger: "dangerous"}
	if l.No {
		return no[lvl]
	}
	return en[lvl]
}

func (l L) Keys(lvl risk.Level) string {
	if lvl == risk.Danger {
		return l.t("Skriv JA for å kjøre, Enter for å avbryte", "Type YES to run, Enter to cancel")
	}
	return l.t("ja / nei / vis / endre", "yes / no / show / edit")
}

func (l L) Run() string { return l.t("Kjøre?", "Run?") }

func (l L) RunFixed() string {
	return l.t("Kjøre den rettede kommandoen?", "Run the corrected command?")
}

func (l L) Failed(code int) string {
	if code == 0 {
		return l.t("Kommandoen skrev feilmeldinger, så svaret er nok feil. Forslag til retting:",
			"The command printed errors, so its answer is probably wrong. Suggested fix:")
	}
	return l.t(fmt.Sprintf("Kommandoen feilet (kode %d). Forslag til retting:", code),
		fmt.Sprintf("The command failed (exit %d). Suggested fix:", code))
}

func (l L) Cancelled() string { return l.t("Avbrutt.", "Cancelled.") }

func (l L) Needs(tool, install string, sizeNote string) string {
	if install == "" {
		return l.t(fmt.Sprintf("Dette trenger %s, som ikke er installert, og som pakkesystemet ditt ikke har.", tool),
			fmt.Sprintf("This needs %s, which isn't installed and isn't in your package manager.", tool))
	}
	return l.t(fmt.Sprintf("Dette trenger %s, som ikke er installert. Installer med `%s`%s?", tool, install, sizeNote),
		fmt.Sprintf("This needs %s, which isn't installed. Install with `%s`%s?", tool, install, sizeNote))
}

func (l L) Trashed(n int, size int64) string {
	return l.t(fmt.Sprintf("La %s (%s) i papirkurven. «zshai: angre» legger dem tilbake.", l.files(n), l.Size(size)),
		fmt.Sprintf("Moved %s (%s) to the trash. \"zshai: undo\" puts them back.", l.files(n), l.Size(size)))
}

func (l L) Unreachable(err error) string {
	return l.t("Får ikke kontakt med modellen ("+err.Error()+"). Skallet virker som vanlig.",
		"Can't reach the model ("+err.Error()+"). The shell works as usual.")
}

func (l L) CwdGone(dir string) string {
	return l.t("Mappa du står i finnes ikke lenger ("+dir+"). Gå til en annen mappe først, f.eks. cd ~",
		"The folder you are in no longer exists ("+dir+"). Change to another folder first, e.g. cd ~")
}

func (l L) NeedsShow(tool, install string) string {
	if install == "" {
		return l.t(tool+" er ikke installert", tool+" isn't installed")
	}
	return l.t(tool+" er ikke installert (installer med `"+install+"`)", tool+" isn't installed (install with `"+install+"`)")
}

func (l L) ShowHint() string {
	return l.t("(ikke kjørt; ↑ henter kommandoen)", "(not run; ↑ brings it back)")
}

func (l L) Unchecked(problems []string) string {
	return l.t("Obs, kommandoen besto ikke kontrollen: ", "Note, the command failed zshai's checks: ") + strings.Join(problems, "; ")
}

func (l L) Gave(problems []string) string {
	return l.t("Fant ingen kommando som besto kontrollen: ", "Couldn't find a command that passed the checks: ") + strings.Join(problems, "; ")
}

func (l L) Remote(hosts []string) string {
	return l.t("kjører på ", "runs on ") + strings.Join(hosts, ", ")
}

// Spinner labels (internal/spinner): what zshai is waiting for.
func (l L) Thinking() string       { return l.t("tenker", "thinking") }
func (l L) Retrying() string       { return l.t("prøver igjen", "retrying") }
func (l L) ThinkingHarder() string { return l.t("tenker hardere", "thinking harder") }
func (l L) Fixing() string         { return l.t("retter feilen", "fixing it") }
func (l L) StartingModel() string  { return l.t("starter modellen", "starting the model") }
