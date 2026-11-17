// Package psfmt prints JSON, JSON Lines and CSV records the way PowerShell prints
// objects (ConvertFrom-Json / Import-Csv, then Format-Table or Format-List):
//
//	name  age city          name : Alice
//	----  --- ----          age  : 30
//	Alice  30 Oslo          city : Oslo
//
// Auto mode follows PowerShell's rule for custom objects: a table for up to 4
// properties, a list for more. Nested values look like PowerShell's too:
// objects as @{a=1; b=2}, arrays as {1, 2, 3, 4…} (first 4 items, $FormatEnumerationLimit).
package psfmt

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

type Mode int

const (
	Auto Mode = iota
	Table
	List
)

// EnumLimit is PowerShell's $FormatEnumerationLimit: arrays show this many items.
const EnumLimit = 4

// Object is a JSON object with its keys in file order.
type Object struct {
	Keys []string
	Vals map[string]any
}

// Options for Format.
type Options struct {
	Mode  Mode
	Props []string // only these properties, in this order (Select-Object)
	Sort  string   // property to sort by; "-name" = descending (Sort-Object)
	First int      // only the first N records after sorting (Select-Object -First)
	Width int      // terminal width; 0 = 120
}

// ---------------------------------------------------------------- reading

// ErrEmpty: the input has nothing in it (an empty file, or only blank lines).
var ErrEmpty = errors.New("is empty")

// Text returns b as UTF-8 without a byte order mark. UTF-16 with a BOM (Excel's
// "Unicode text", PowerShell 5's Out-File) is decoded.
func Text(b []byte) []byte {
	if len(b) >= 2 && (b[0] == 0xff && b[1] == 0xfe || b[0] == 0xfe && b[1] == 0xff) {
		be := b[0] == 0xfe
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			if be {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			} else {
				u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
			}
		}
		return []byte(string(utf16.Decode(u)))
	}
	return bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
}

// ParseJSON reads one JSON value, or several (JSON Lines / concatenated JSON), which then
// become an array. Objects keep their key order; numbers stay as written (json.Number).
func ParseJSON(b []byte) (any, error) {
	b = Text(b)
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, ErrEmpty
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var vals []any
	for {
		v, err := readValue(dec)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}
	switch len(vals) {
	case 0:
		return nil, ErrEmpty
	case 1:
		return vals[0], nil
	}
	return vals, nil
}

func readValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &Object{Vals: map[string]any{}}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := readValue(dec)
				if err != nil {
					return nil, err
				}
				if _, dup := o.Vals[k]; !dup {
					o.Keys = append(o.Keys, k)
				}
				o.Vals[k] = v
			}
			_, err := dec.Token() // }
			return o, err
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := readValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token() // ]
			return arr, err
		}
		return nil, fmt.Errorf("unexpected %v", t)
	default:
		return tok, nil // string, json.Number, bool, nil
	}
}

// ParseCSV reads CSV with a header line, like Import-Csv. delim 0 = guess from the
// header line (tab, ; or ,). Values that look like numbers become numbers, so they are
// right-aligned and sort as numbers.
func ParseCSV(b []byte, delim rune) ([]any, error) {
	b = Text(b)
	// old Mac line endings (\r alone) are one long line to encoding/csv
	if bytes.IndexByte(b, '\r') >= 0 {
		b = bytes.ReplaceAll(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, ErrEmpty
	}
	if delim == 0 {
		first, _, _ := bytes.Cut(b, []byte("\n"))
		delim = ','
		best := bytes.Count(first, []byte(","))
		for _, d := range []rune{';', '\t', '|'} {
			if n := bytes.Count(first, []byte(string(d))); n > best {
				delim, best = d, n
			}
		}
	}
	r := csv.NewReader(bytes.NewReader(b))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrEmpty
	}
	head := rows[0]
	for i, h := range head {
		if strings.TrimSpace(h) == "" {
			head[i] = fmt.Sprintf("H%d", i+1) // Import-Csv's name for an empty header
		}
	}
	out := make([]any, 0, len(rows)-1)
	for _, row := range rows[1:] {
		o := &Object{Keys: head, Vals: map[string]any{}}
		for i, h := range head {
			if i < len(row) {
				o.Vals[h] = csvValue(row[i])
			} else {
				o.Vals[h] = nil
			}
		}
		out = append(out, o)
	}
	return out, nil
}

func csvValue(s string) any {
	t := strings.TrimSpace(s)
	if t != "" && t == s {
		if _, err := strconv.ParseFloat(t, 64); err == nil && !strings.ContainsAny(t, "xXeEnN") {
			// leading zeros (zip codes, ids like 007) stay text
			if !(len(t) > 1 && t[0] == '0' && t[1] != '.') {
				return json.Number(t)
			}
		}
	}
	return s
}

// ---------------------------------------------------------------- formatting

// Format renders v.
func Format(v any, o Options) string {
	if o.Width <= 0 {
		o.Width = 120
	}
	recs, isList := v.([]any)
	if !isList {
		recs = []any{v}
	}
	if o.Sort != "" {
		recs = sortRecords(recs, o.Sort)
	}
	if o.First > 0 && len(recs) > o.First {
		recs = recs[:o.First]
	}
	// split into objects and other values; PowerShell prints scalars one per line
	var objs []*Object
	allObjs := len(recs) > 0
	for _, r := range recs {
		ob, ok := r.(*Object)
		if !ok {
			allObjs = false
			break
		}
		objs = append(objs, ob)
	}
	if !allObjs {
		var sb strings.Builder
		for _, r := range recs {
			if ob, ok := r.(*Object); ok {
				sb.WriteString(list([]*Object{ob}, props([]*Object{ob}, o.Props), o.Width))
				continue
			}
			sb.WriteString(Value(r, false))
			sb.WriteByte('\n')
		}
		return sb.String()
	}
	ps := props(objs, o.Props)
	mode := o.Mode
	if mode == Auto {
		mode = Table
		if len(ps) > 4 {
			mode = List
		}
	}
	if mode == List {
		if len(o.Props) == 0 {
			ps = nil // each object shows its own properties, as Format-List does
		}
		return list(objs, ps, o.Width)
	}
	return table(objs, ps, o.Width)
}

// props: the wanted properties, or every property in first-seen order. Names in want are
// matched without case, like PowerShell; unknown ones are kept (they show empty).
func props(objs []*Object, want []string) []string {
	var all []string
	seen := map[string]bool{}
	for _, ob := range objs {
		for _, k := range ob.Keys {
			if !seen[k] {
				seen[k] = true
				all = append(all, k)
			}
		}
	}
	if len(want) == 0 {
		return all
	}
	var out []string
	for _, w := range want {
		name := w
		for _, k := range all {
			if strings.EqualFold(k, w) {
				name = k
				break
			}
		}
		out = append(out, name)
	}
	return out
}

func get(ob *Object, k string) any {
	if v, ok := ob.Vals[k]; ok {
		return v
	}
	for _, kk := range ob.Keys {
		if strings.EqualFold(kk, k) {
			return ob.Vals[kk]
		}
	}
	return nil
}

func sortRecords(recs []any, key string) []any {
	desc := strings.HasPrefix(key, "-")
	key = strings.TrimPrefix(strings.TrimPrefix(key, "-"), "+")
	out := append([]any(nil), recs...)
	val := func(r any) any {
		if ob, ok := r.(*Object); ok {
			return get(ob, key)
		}
		return r
	}
	sort.SliceStable(out, func(i, j int) bool {
		c := compare(val(out[i]), val(out[j]))
		if desc {
			return c > 0
		}
		return c < 0
	})
	return out
}

// compare: nulls first, numbers by value, everything else as text without case.
func compare(a, b any) int {
	if a == nil || b == nil {
		switch {
		case a == nil && b == nil:
			return 0
		case a == nil:
			return -1
		}
		return 1
	}
	na, aok := num(a)
	nb, bok := num(b)
	if aok && bok {
		return na.Cmp(nb)
	}
	return strings.Compare(strings.ToLower(Value(a, true)), strings.ToLower(Value(b, true)))
}

func num(v any) (*big.Float, bool) {
	var s string
	switch t := v.(type) {
	case json.Number:
		s = string(t)
	case string:
		s = strings.TrimSpace(t)
	default:
		return nil, false
	}
	f, _, err := big.ParseFloat(s, 10, 128, big.ToNearestEven)
	return f, err == nil
}

// Value renders one value as PowerShell shows it in a table cell (inline = true) or a
// list. Strings are printed without quotes; null is empty; booleans are True/False.
func Value(v any, inline bool) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		if inline {
			return oneLine(t)
		}
		return t
	case json.Number:
		return string(t)
	case bool:
		if t {
			return "True"
		}
		return "False"
	case []any:
		parts := make([]string, 0, min(len(t), EnumLimit))
		for i, x := range t {
			if i == EnumLimit {
				break
			}
			parts = append(parts, nested(x))
		}
		s := "{" + strings.Join(parts, ", ")
		if len(t) > EnumLimit {
			s += "…"
		}
		return s + "}"
	case *Object:
		return nested(t)
	}
	return fmt.Sprint(v)
}

// nested: a value inside an array or object. Objects are @{k=v; …}, as PowerShell
// prints a PSCustomObject that sits inside another one.
func nested(v any) string {
	ob, ok := v.(*Object)
	if !ok {
		return Value(v, true)
	}
	parts := make([]string, 0, len(ob.Keys))
	for _, k := range ob.Keys {
		parts = append(parts, k+"="+nested(ob.Vals[k]))
	}
	return "@{" + strings.Join(parts, "; ") + "}"
}

func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n\t") {
		return s
	}
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	return s
}

func isNum(v any) bool {
	_, ok := v.(json.Number)
	return ok
}

// width: display columns (wide East Asian characters and emoji take two).
func width(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case r == 0x200d || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || (r >= 0xfe00 && r <= 0xfe0f):
		case wide(r):
			n += 2
		default:
			n++
		}
	}
	return n
}

func wide(r rune) bool {
	return r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) || (r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe30 && r <= 0xfe4f) || (r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) || (r >= 0x1f300 && r <= 0x1f64f) || (r >= 0x1f900 && r <= 0x1f9ff) ||
		(r >= 0x20000 && r <= 0x3fffd))
}

// cut shortens s to w display columns, ending in … like PowerShell's truncated cells.
func cut(s string, w int) string {
	if width(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	var sb strings.Builder
	n := 0
	for _, r := range s {
		rw := width(string(r))
		if n+rw > w-1 {
			break
		}
		sb.WriteRune(r)
		n += rw
	}
	sb.WriteString("…")
	return sb.String()
}

func pad(s string, w int, right bool) string {
	gap := w - width(s)
	if gap <= 0 {
		return s
	}
	if right {
		return strings.Repeat(" ", gap) + s
	}
	return s + strings.Repeat(" ", gap)
}

// table: Format-Table -AutoSize. Columns are as wide as their widest cell; number
// columns are right-aligned (header too); a table wider than the terminal shrinks its
// widest columns, and drops columns from the right when even that isn't enough.
func table(objs []*Object, ps []string, termW int) string {
	if len(ps) == 0 {
		return ""
	}
	cells := make([][]string, len(objs))
	right := make([]bool, len(ps))
	w := make([]int, len(ps))
	for j, p := range ps {
		w[j] = width(p)
		right[j] = true
		seen := false
		for _, ob := range objs {
			v := get(ob, p)
			if v == nil {
				continue
			}
			seen = true
			if !isNum(v) {
				right[j] = false
			}
		}
		if !seen {
			right[j] = false
		}
	}
	for i, ob := range objs {
		cells[i] = make([]string, len(ps))
		for j, p := range ps {
			s := Value(get(ob, p), true)
			cells[i][j] = s
			w[j] = max(w[j], width(s))
		}
	}
	// fit the terminal: the last column may end at termW-1 (no wrap on the last cell)
	total := func(n int) int {
		t := 0
		for j := 0; j < n; j++ {
			t += w[j]
		}
		return t + n - 1
	}
	n := len(ps)
	limit := termW - 1
	for total(n) > limit {
		// shrink the widest column, but not below its header (or 8)
		big, bj := 0, -1
		for j := 0; j < n; j++ {
			floor := max(width(ps[j]), min(8, w[j]))
			if w[j] > floor && w[j] > big {
				big, bj = w[j], j
			}
		}
		if bj < 0 {
			if n == 1 {
				break
			}
			n-- // nothing left to shrink: drop the rightmost column
			continue
		}
		floor := max(width(ps[bj]), min(8, w[bj]))
		w[bj] = max(floor, w[bj]-(total(n)-limit))
	}
	var sb strings.Builder
	sb.WriteByte('\n')
	row := func(vals []string, under bool) {
		var line strings.Builder
		for j := 0; j < n; j++ {
			s := vals[j]
			if under {
				s = strings.Repeat("-", min(width(ps[j]), w[j]))
			}
			s = cut(s, w[j])
			if j == n-1 && !right[j] {
				line.WriteString(s)
			} else {
				line.WriteString(pad(s, w[j], right[j]))
			}
			if j < n-1 {
				line.WriteByte(' ')
			}
		}
		sb.WriteString(strings.TrimRight(line.String(), " "))
		sb.WriteByte('\n')
	}
	row(ps, false)
	row(ps, true)
	for _, c := range cells {
		row(c, false)
	}
	sb.WriteByte('\n')
	return sb.String()
}

// list: Format-List. One block per object, names padded to the longest, " : ", long or
// multi-line values continue under the value column. ps nil = each object's own keys.
func list(objs []*Object, ps []string, termW int) string {
	var sb strings.Builder
	sb.WriteByte('\n')
	for _, ob := range objs {
		keys := ps
		if keys == nil {
			keys = ob.Keys
		}
		nw := 0
		for _, p := range keys {
			nw = max(nw, width(p))
		}
		indent := nw + 3
		valW := max(20, termW-1-indent)
		for _, p := range keys {
			sb.WriteString(pad(p, nw, false))
			sb.WriteString(" :")
			if v := get(ob, p); v != nil && Value(v, false) != "" {
				sb.WriteByte(' ')
			}
			lines := wrap(Value(get(ob, p), false), valW)
			for i, l := range lines {
				if i > 0 {
					sb.WriteString(strings.Repeat(" ", indent))
				}
				sb.WriteString(strings.TrimRight(l, " "))
				sb.WriteByte('\n')
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// wrap breaks s into lines of at most w columns, at spaces where possible.
func wrap(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		for width(para) > w {
			cutAt := 0
			n := 0
			lastSpace := -1
			for i, r := range para {
				rw := width(string(r))
				if n+rw > w {
					break
				}
				n += rw
				cutAt = i + utf8.RuneLen(r)
				if r == ' ' {
					lastSpace = i
				}
			}
			if lastSpace > 0 {
				cutAt = lastSpace
			}
			out = append(out, strings.TrimRight(para[:cutAt], " "))
			para = strings.TrimLeft(para[cutAt:], " ")
		}
		out = append(out, para)
	}
	return out
}
