package items

import (
	"strings"
	"testing"
)

func f(v float64) *float64 { return &v }

func TestParseAutoDetects(t *testing.T) {
	cases := []struct {
		in   string
		want Format
		n    int
	}{
		{`["a", {"b": 1}, 3]`, JSON, 3},
		{`{"results": [{"t": "x"}, {"t": "y"}], "total": 2}`, JSON, 2},
		{"{\"a\":1}\n{\"a\":2}\n\n{\"a\":3}\n", JSONL, 3},
		{"one\ntwo\n\nthree\n", Lines, 3},
		{"[not json\nsecond line", Lines, 2},
	}
	for _, c := range cases {
		l, err := Parse([]byte(c.in), Auto, "")
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if l.Format != c.want || len(l.Raw) != c.n {
			t.Errorf("%q: got %s with %d items, want %s with %d", c.in, l.Format, len(l.Raw), c.want, c.n)
		}
	}
}

func TestParseObjectNeedsKeyWhenAmbiguous(t *testing.T) {
	in := []byte(`{"a": [1], "b": [2, 3]}`)
	if _, err := Parse(in, Auto, ""); err == nil || !strings.Contains(err.Error(), "--key") {
		t.Fatalf("want --key hint, got %v", err)
	}
	l, err := Parse(in, JSON, "b")
	if err != nil || len(l.Raw) != 2 || l.Key != "b" {
		t.Fatalf("got %+v, %v", l, err)
	}
}

func TestTextFieldsAndTruncation(t *testing.T) {
	l, _ := Parse([]byte(`[{"title": "Bloom", "body": "long", "n": 3}, "plain string"]`), JSON, "")
	if got := l.Text(0, []string{"title", "n"}, 0); got != "title: Bloom\nn: 3" {
		t.Errorf("fields text = %q", got)
	}
	if got := l.Text(1, nil, 0); got != "plain string" {
		t.Errorf("string item = %q", got)
	}
	if got := l.Text(1, nil, 5); got != "plain…" {
		t.Errorf("truncated = %q", got)
	}
}

func TestEncodeRoundTrips(t *testing.T) {
	l, _ := Parse([]byte(`[{"a": 1}, {"a": 2}, {"a": 3}]`), Auto, "")
	out, err := l.Encode([]int{0, 2}, []*float64{f(.9), f(.1), nil}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(strings.Fields(string(out)), ""); got != `[{"a":1},{"a":3}]` {
		t.Errorf("json out = %s", out)
	}
	out, _ = l.Encode([]int{2}, []*float64{f(.9), f(.1), nil}, true)
	if !strings.Contains(string(out), `"p": null`) && !strings.Contains(string(out), `"p":null`) {
		t.Errorf("unjudged score should be null: %s", out)
	}

	lines, _ := Parse([]byte("x\ny\n"), Lines, "")
	out, _ = lines.Encode([]int{1}, []*float64{f(.2), f(.8)}, true)
	if string(out) != "0.80\ty\n" {
		t.Errorf("lines out = %q", out)
	}
}
