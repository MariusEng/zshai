package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/MariusEng/zshai/internal/config"
	"github.com/MariusEng/zshai/internal/msg"
)

// "zshai: config fixAttempts=2", "zshai: config escalate.model qwen/qwen3-coder-30b",
// "zshai: config typos av", "zshai: config fixAttempts" (show one). Several key=value pairs
// in one line are fine. The value's type follows the setting (number, on/off, text, list).

// configArg: what follows "config" when it is a setting: key=value pairs, "key value" or
// a bare key (letters, dots). "config how do I …" stays a request for the model.
var configArg = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.]*(\s*=.*|\s+\S+)?$`)

// choices for settings that only take some values
var configChoices = map[string][]string{
	"language": {"no", "en"}, "tools": {"installed", "shipped"}, "suggestInstall": {"ask", "mention", "never"},
	"packageManager": {"auto", "apt", "pacman", "brew", "dnf"}, "tables": {"powershell", "column"},
	"localModel": {"", "1.5b", "3b"},
}

func configSet(L msg.L, rest string) string {
	base := map[string]any{}
	b, _ := json.Marshal(config.Defaults())
	_ = json.Unmarshal(b, &base)
	// settings left out of the file when empty can be set too
	for k, v := range map[string]any{"apiKey": "", "localModel": "",
		"escalate": map[string]any{"server": "", "model": "", "apiKey": "", "timeoutSeconds": 0.0}} {
		if _, ok := base[k]; !ok {
			base[k] = v
		}
	}
	if raw, err := os.ReadFile(config.Path()); err == nil {
		var file map[string]any
		if err := json.Unmarshal(raw, &file); err != nil {
			return t(L, "Innstillingsfila er ikke gyldig JSON: ", "The settings file is not valid JSON: ") + err.Error() + "\n"
		}
		for k, v := range file {
			base[k] = v
		}
	}
	pairs, err := splitPairs(rest)
	if err != nil {
		return err.Error() + "\n"
	}
	var out strings.Builder
	changed := false
	for _, p := range pairs {
		path, ok := resolveKey(base, p[0])
		if !ok {
			return fmt.Sprintf(t(L, "Ukjent innstilling «%s». Innstillingene: %s\n", "Unknown setting %q. The settings: %s\n"),
				p[0], strings.Join(keyList(base), ", "))
		}
		old := getPath(base, path)
		if p[1] == "\x00" { // no value: show it
			fmt.Fprintf(&out, "%s: %s\n", strings.Join(path, "."), showValue(path, old))
			continue
		}
		v, err := parseValue(old, p[1])
		if err != nil {
			return fmt.Sprintf("%s: %v\n", strings.Join(path, "."), err)
		}
		if ch, ok := configChoices[path[len(path)-1]]; ok && len(path) == 1 {
			s, _ := v.(string)
			if !slices.Contains(ch, s) {
				return fmt.Sprintf(t(L, "%s kan være: %s\n", "%s can be: %s\n"), path[0], strings.Join(nonEmpty(ch), ", "))
			}
		}
		setPath(base, path, v)
		fmt.Fprintf(&out, "%s: %s → %s\n", strings.Join(path, "."), showValue(path, old), showValue(path, v))
		changed = true
	}
	if !changed {
		return out.String()
	}
	// it must still be a valid config: the types are checked by decoding into it
	nb, _ := json.Marshal(base)
	cfg := config.Defaults()
	dec := json.NewDecoder(bytes.NewReader(nb))
	if err := dec.Decode(&cfg); err != nil {
		return t(L, "Ugyldig verdi: ", "Invalid value: ") + err.Error() + "\n"
	}
	if err := config.Save(cfg); err != nil {
		return "zshai: " + err.Error() + "\n"
	}
	return out.String() + t(L, "Lagret i ", "Saved to ") + config.Path() + "\n"
}

// splitPairs: "a=1 b=2", "a 1", "a = 1", "a" (show). Values may be quoted or JSON.
func splitPairs(s string) ([][2]string, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "=") {
		k, v, ok := strings.Cut(s, " ")
		if !ok {
			return [][2]string{{s, "\x00"}}, nil
		}
		return [][2]string{{k, strings.TrimSpace(v)}}, nil
	}
	var out [][2]string
	for s != "" {
		k, rest, ok := strings.Cut(s, "=")
		if !ok {
			return nil, fmt.Errorf("expected key=value, got %q", s)
		}
		k = strings.TrimSpace(k)
		rest = strings.TrimLeft(rest, " ")
		// the value runs to the next " key=" (or the end); JSON and quoted values whole
		end := len(rest)
		if len(rest) > 0 && (rest[0] == '"' || rest[0] == '[' || rest[0] == '{' || rest[0] == '\'') {
			dec := json.NewDecoder(strings.NewReader(rest))
			var x any
			if rest[0] != '\'' && dec.Decode(&x) == nil {
				end = int(dec.InputOffset())
			} else if rest[0] == '\'' {
				if i := strings.IndexByte(rest[1:], '\''); i >= 0 {
					end = i + 2
				}
			}
		} else {
			for i := 0; i < len(rest); i++ {
				if rest[i] == ' ' {
					if j := strings.IndexByte(rest[i:], '='); j > 0 && !strings.Contains(strings.TrimSpace(rest[i:i+j]), " ") {
						end = i
						break
					}
				}
			}
		}
		out = append(out, [2]string{k, strings.TrimSpace(rest[:end])})
		s = strings.TrimSpace(rest[end:])
	}
	return out, nil
}

// resolveKey finds a (dotted) key, ignoring case: fixattempts, escalate.Model.
func resolveKey(m map[string]any, key string) ([]string, bool) {
	var path []string
	cur := m
	parts := strings.Split(key, ".")
	for i, p := range parts {
		found := ""
		for k := range cur {
			if strings.EqualFold(k, p) {
				found = k
			}
		}
		if found == "" {
			return nil, false
		}
		path = append(path, found)
		if i < len(parts)-1 {
			next, ok := cur[found].(map[string]any)
			if !ok {
				return nil, false
			}
			cur = next
		}
	}
	return path, true
}

func getPath(m map[string]any, path []string) any {
	var cur any = m
	for _, p := range path {
		cur = cur.(map[string]any)[p]
	}
	return cur
}

func setPath(m map[string]any, path []string, v any) {
	for _, p := range path[:len(path)-1] {
		m = m[p].(map[string]any)
	}
	m[path[len(path)-1]] = v
}

func parseValue(old any, s string) (any, error) {
	unq := strings.Trim(s, `"'`)
	switch old.(type) {
	case bool:
		switch strings.ToLower(unq) {
		case "true", "on", "på", "ja", "yes", "1":
			return true, nil
		case "false", "off", "av", "nei", "no", "0":
			return false, nil
		}
		return nil, fmt.Errorf("on/off (på/av, true/false), not %q", s)
	case float64:
		n, err := strconv.ParseFloat(unq, 64)
		if err != nil {
			return nil, fmt.Errorf("a number, not %q", s)
		}
		return n, nil
	case []any, nil:
		if strings.HasPrefix(s, "[") {
			var v []any
			if err := json.Unmarshal([]byte(s), &v); err != nil {
				return nil, err
			}
			return v, nil
		}
		if _, isList := old.([]any); isList || old == nil {
			var v []any
			for _, x := range strings.Split(unq, ",") {
				if x = strings.TrimSpace(x); x != "" {
					v = append(v, x)
				}
			}
			return v, nil
		}
	case map[string]any:
		var v map[string]any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return nil, fmt.Errorf("a JSON object, or set one field: key.field=value")
		}
		return v, nil
	}
	return unq, nil
}

func showValue(path []string, v any) string {
	if strings.EqualFold(path[len(path)-1], "apiKey") {
		if s, _ := v.(string); s != "" {
			return "(hidden)"
		}
	}
	if m, ok := v.(map[string]any); ok {
		c := map[string]any{}
		for k, x := range m {
			if s, _ := x.(string); strings.EqualFold(k, "apiKey") && s != "" {
				x = "(hidden)"
			}
			c[k] = x
		}
		v = c
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func keyList(m map[string]any) []string {
	var ks []string
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			for sk := range sub {
				ks = append(ks, k+"."+sk)
			}
			continue
		}
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func nonEmpty(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}
