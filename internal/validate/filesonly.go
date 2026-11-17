package validate

import (
	"regexp"
	"strings"
)

// FilesOnly: "rm -f zshai*" for "slett alle zshai* filer" also hits the folders zshai*
// matches, and rm stops ("is a directory"). Small models don't fix that when told; the fix
// is mechanical: zsh's (.) qualifier keeps plain files. Only for one plain rm of globs;
// -f and -r go (the user asked for files, and zshai's delete goes to the trash anyway).
// Returns "" when it doesn't apply.
var rmGlobRe = regexp.MustCompile(`^\s*rm((?:\s+-[a-zA-Z]+)*)(?:\s+--)?((?:\s+[^\s;|&<>()'"$` + "`" + `]+)+)\s*$`)

func FilesOnly(src string) string {
	m := rmGlobRe.FindStringSubmatch(src)
	if m == nil {
		return ""
	}
	var flags []string
	for _, f := range strings.Fields(m[1]) {
		f = strings.NewReplacer("f", "", "r", "", "R", "").Replace(strings.TrimPrefix(f, "-"))
		if f != "" {
			flags = append(flags, "-"+f)
		}
	}
	globbed := false
	var args []string
	for _, a := range strings.Fields(m[2]) {
		if strings.ContainsAny(a, "*?[") {
			a += "(.)"
			globbed = true
		}
		args = append(args, a)
	}
	if !globbed {
		return ""
	}
	return strings.Join(append(append(append([]string{"rm"}, flags...), "--"), args...), " ")
}
