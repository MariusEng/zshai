package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/MariusEng/zshai/internal/psfmt"
)

const fmtUsage = `zshai fmt — show JSON, JSON Lines or CSV the way PowerShell shows objects

  zshai fmt [options] [file ...]        (no file or - = stdin)

  -t, --table          Format-Table: aligned columns under a dashed header
  -l, --list           Format-List: one "name : value" block per record
                       (default: table for up to 4 properties, list for more)
  -p, --props a,b      only these properties, in this order (Select-Object)
  -s, --sort KEY       sort by KEY; -KEY sorts largest first (Sort-Object)
  -n, --first N        only the first N records (after sorting)
      --csv            stdin is CSV (files ending in .csv/.tsv are read as CSV)
  -d, --delim C        CSV delimiter (default: guessed from the header line)
  -w, --width N        terminal width (default: the terminal's)

  jq '.[] | select(.age > 30)' people.json | zshai fmt -t -p name,age
  zshai fmt -t --sort -total -n 5 orders.json
`

// cmdFmt: exit 0 on success, 1 on unreadable input, 2 on bad usage.
func cmdFmt(args []string) int {
	var o psfmt.Options
	var files []string
	csvIn := false
	var delim rune
	need := func(i int, name string) (string, bool) {
		if i+1 >= len(args) {
			fmt.Fprintf(os.Stderr, "zshai fmt: %s needs a value\n", name)
			return "", false
		}
		return args[i+1], true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(a, "=")
		if !strings.HasPrefix(a, "--") {
			name, hasVal = a, false
		}
		value := func() (string, bool) {
			if hasVal {
				return val, true
			}
			v, ok := need(i, name)
			if ok {
				i++
			}
			return v, ok
		}
		switch name {
		case "-t", "--table":
			o.Mode = psfmt.Table
		case "-l", "--list":
			o.Mode = psfmt.List
		case "--csv":
			csvIn = true
		case "-p", "--props", "--property":
			v, ok := value()
			if !ok {
				return 2
			}
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					o.Props = append(o.Props, p)
				}
			}
		case "-s", "--sort":
			v, ok := value()
			if !ok {
				return 2
			}
			o.Sort = v
		case "-n", "--first":
			v, ok := value()
			if !ok {
				return 2
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "zshai fmt: --first needs a number")
				return 2
			}
			o.First = n
		case "-d", "--delim":
			v, ok := value()
			if !ok {
				return 2
			}
			if v == `\t` || v == "tab" {
				v = "\t"
			}
			if r := []rune(v); len(r) == 1 {
				delim = r[0]
			} else {
				fmt.Fprintln(os.Stderr, "zshai fmt: --delim needs one character")
				return 2
			}
			csvIn = true
		case "-w", "--width":
			v, ok := value()
			if !ok {
				return 2
			}
			o.Width, _ = strconv.Atoi(v)
		case "-h", "--help":
			fmt.Print(fmtUsage)
			return 0
		case "--":
			files = append(files, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(a, "-") && a != "-" {
				fmt.Fprintf(os.Stderr, "zshai fmt: unknown option %s\n", a)
				return 2
			}
			files = append(files, a)
		}
	}
	if o.Width == 0 {
		o.Width = termWidth()
	}
	if len(files) == 0 {
		files = []string{"-"}
	}
	var recs []any
	var parts []part
	single := len(files) == 1
	for _, f := range files {
		var b []byte
		var err error
		name := f
		if f == "-" {
			b, err = io.ReadAll(os.Stdin)
			name = "stdin"
		} else {
			b, err = os.ReadFile(f)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "zshai fmt:", err)
			return 1
		}
		ext := strings.ToLower(filepath.Ext(f))
		var v any
		if csvIn || ext == ".csv" || ext == ".tsv" {
			d := delim
			if d == 0 && ext == ".tsv" {
				d = '\t'
			}
			var rows []any
			rows, err = psfmt.ParseCSV(b, d)
			v = rows
			if err == nil && len(rows) == 0 {
				// like Import-Csv: a header without rows prints nothing
				fmt.Fprintf(os.Stderr, "zshai fmt: %s has a header line but no rows\n", name)
			}
		} else {
			v, err = psfmt.ParseJSON(b)
			if err != nil && err != psfmt.ErrEmpty {
				err = fmt.Errorf("%s is not JSON (%v); for CSV use --csv", name, err)
			}
		}
		if err == psfmt.ErrEmpty {
			err = fmt.Errorf("%s is empty (%d bytes)", name, len(b))
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "zshai fmt:", err)
			return 1
		}
		if single {
			fmt.Print(psfmt.Format(v, o))
			return 0
		}
		parts = append(parts, part{name, v})
	}
	// files with the same columns are one table (parts of one export); different files
	// each get their own, under their name (zshai fmt -t *.csv)
	same := true
	for _, p := range parts[1:] {
		if shape(p.v) != shape(parts[0].v) {
			same = false
		}
	}
	if !same {
		for i, p := range parts {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("%s:\n", p.name)
			fmt.Print(psfmt.Format(p.v, o))
		}
		return 0
	}
	for _, p := range parts {
		if arr, ok := p.v.([]any); ok {
			recs = append(recs, arr...)
		} else {
			recs = append(recs, p.v)
		}
	}
	fmt.Print(psfmt.Format(recs, o))
	return 0
}

type part struct {
	name string
	v    any
}

// shape: the property names of a file's first record ("" for anything else).
func shape(v any) string {
	if arr, ok := v.([]any); ok && len(arr) > 0 {
		v = arr[0]
	}
	if obj, ok := v.(*psfmt.Object); ok {
		return strings.Join(obj.Keys, "\x00")
	}
	return ""
}

// termWidth: stdout's terminal, else $COLUMNS, else wide enough to never cut (a file).
func termWidth() int {
	if ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ); err == nil && ws.Col > 0 {
		return int(ws.Col)
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	return 100000
}
