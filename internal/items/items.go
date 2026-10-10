// Package items parses and re-emits the arbitrary lists `skimmer filter`
// judges: a JSON array, JSON Lines, or plain text lines.
package items

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Format is an input/output encoding.
type Format string

const (
	Auto  Format = "auto"
	JSON  Format = "json"
	JSONL Format = "jsonl"
	Lines Format = "lines"
)

// ParseFormat validates a --format value.
func ParseFormat(s string) (Format, error) {
	switch f := Format(strings.ToLower(strings.TrimSpace(s))); f {
	case "", Auto:
		return Auto, nil
	case JSON, JSONL, Lines:
		return f, nil
	case "ndjson":
		return JSONL, nil
	case "text", "txt":
		return Lines, nil
	}
	return "", fmt.Errorf("unknown format %q (expected auto, json, jsonl, or lines)", s)
}

// List is a parsed input.
type List struct {
	Format Format
	// Raw holds each item exactly as given: JSON for json/jsonl, the line
	// for lines.
	Raw []string
	// Key is the object field the array was found under, if any.
	Key string
}

// Parse reads data in the given format. With Auto it tries a JSON array (or
// an object holding exactly one array), then JSON Lines, then lines. key,
// when set, names the field of a top-level JSON object that holds the array.
func Parse(data []byte, f Format, key string) (*List, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("input is empty")
	}
	switch f {
	case JSON:
		return parseJSON(trimmed, key)
	case JSONL:
		return parseJSONL(trimmed)
	case Lines:
		return parseLines(data), nil
	}
	if trimmed[0] == '[' || trimmed[0] == '{' {
		l, err := parseJSON(trimmed, key)
		if err == nil {
			return l, nil
		}
		// One whole JSON value that isn't a usable list (e.g. an object
		// with several arrays) is a real error, not one JSON Lines item.
		if json.Valid(trimmed) {
			return nil, err
		}
		if l, err := parseJSONL(trimmed); err == nil {
			return l, nil
		}
	}
	return parseLines(data), nil
}

func parseJSON(data []byte, key string) (*List, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err == nil {
		return &List{Format: JSON, Raw: raws(arr)}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("input is not a JSON array or object: %w", err)
	}
	if key != "" {
		v, ok := obj[key]
		if !ok {
			return nil, fmt.Errorf("JSON object has no field %q", key)
		}
		if err := json.Unmarshal(v, &arr); err != nil {
			return nil, fmt.Errorf("field %q is not an array", key)
		}
		return &List{Format: JSON, Raw: raws(arr), Key: key}, nil
	}
	var found []string
	for k, v := range obj {
		var a []json.RawMessage
		if json.Unmarshal(v, &a) == nil {
			found = append(found, k)
			arr = a
		}
	}
	switch len(found) {
	case 1:
		return &List{Format: JSON, Raw: raws(arr), Key: found[0]}, nil
	case 0:
		return nil, errors.New("JSON object holds no array to filter")
	}
	sort.Strings(found)
	return nil, fmt.Errorf("JSON object holds several arrays (%s): pick one with --key", strings.Join(found, ", "))
}

func parseJSONL(data []byte) (*List, error) {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			return nil, fmt.Errorf("line %d is not valid JSON", n)
		}
		out = append(out, string(line))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return &List{Format: JSONL, Raw: out}, nil
}

func parseLines(data []byte) *List {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return &List{Format: Lines, Raw: out}
}

func raws(arr []json.RawMessage) []string {
	out := make([]string, len(arr))
	for i, r := range arr {
		out[i] = string(r)
	}
	return out
}

// Text renders item i as the text Jev reads. A JSON string becomes its
// value; other JSON is compacted, or reduced to the named fields when
// fields is non-empty. The result is cut to maxChars runes (0 = no cap).
func (l *List) Text(i int, fields []string, maxChars int) string {
	raw := l.Raw[i]
	s := raw
	if l.Format != Lines {
		s = jsonText(raw, fields)
	}
	if maxChars > 0 && utf8.RuneCountInString(s) > maxChars {
		s = string([]rune(s)[:maxChars]) + "…"
	}
	return s
}

func jsonText(raw string, fields []string) string {
	var str string
	if json.Unmarshal([]byte(raw), &str) == nil {
		return str
	}
	if len(fields) > 0 {
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &obj) == nil {
			var parts []string
			for _, f := range fields {
				v, ok := obj[f]
				if !ok {
					continue
				}
				var s string
				if json.Unmarshal(v, &s) != nil {
					s = compact(v)
				}
				parts = append(parts, f+": "+s)
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
	}
	return compact([]byte(raw))
}

func compact(b []byte) string {
	var buf bytes.Buffer
	if json.Compact(&buf, b) != nil {
		return string(b)
	}
	return buf.String()
}

// Encode writes the kept items (indices into l.Raw) in l's format. With
// scores, JSON items are wrapped as {"index","p","item"} and lines are
// prefixed with the probability and a tab.
func (l *List) Encode(keep []int, ps []*float64, scores bool) ([]byte, error) {
	var buf bytes.Buffer
	switch l.Format {
	case Lines:
		for _, i := range keep {
			if scores {
				buf.WriteString(fmtP(ps[i]) + "\t")
			}
			buf.WriteString(l.Raw[i])
			buf.WriteByte('\n')
		}
		return buf.Bytes(), nil
	case JSONL:
		for _, i := range keep {
			b, err := l.item(i, ps, scores)
			if err != nil {
				return nil, err
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
		return buf.Bytes(), nil
	}
	out := make([]json.RawMessage, 0, len(keep))
	for _, i := range keep {
		b, err := l.item(i, ps, scores)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func (l *List) item(i int, ps []*float64, scores bool) ([]byte, error) {
	raw := []byte(compact([]byte(l.Raw[i])))
	if !scores {
		return raw, nil
	}
	return json.Marshal(struct {
		Index int             `json:"index"`
		P     *float64        `json:"p"`
		Item  json.RawMessage `json:"item"`
	}{i, ps[i], raw})
}

func fmtP(p *float64) string {
	if p == nil {
		return "?"
	}
	return fmt.Sprintf("%.2f", *p)
}
