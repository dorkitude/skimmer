package kit

import (
	"os"
	"testing"
)

func TestRegionHit(t *testing.T) {
	gold := Region{{1000, 3000}}
	cases := []struct {
		name string
		kept []Span
		want bool
	}{
		{"the chunk itself", []Span{{1000, 3000}}, true},
		{"a neighbor sharing the 10% overlap", []Span{{2800, 4800}}, false},
		{"both neighbors", []Span{{-800, 1200}, {2800, 4800}}, false},
		{"a smaller chunk inside", []Span{{1500, 2500}}, true},
		{"a bigger chunk holding it", []Span{{0, 4000}}, true},
		{"two halves together", []Span{{900, 1900}, {1900, 2950}}, true},
		{"nothing", nil, false},
	}
	for _, c := range cases {
		if got := gold.Hit(c.kept); got != c.want {
			t.Errorf("%s: Hit = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMinus(t *testing.T) {
	r := Region{{0, 100}}.Minus(Region{{80, 200}})
	if len(r) != 1 || r[0] != (Span{0, 80}) {
		t.Errorf("got %v", r)
	}
	r = Region{{0, 100}}.Minus(Region{{20, 30}})
	if len(r) != 2 || r.Len() != 90 {
		t.Errorf("got %v", r)
	}
}

// Every catalog chunk must be found in its document, in order.
func TestCatalogLocates(t *testing.T) {
	if err := os.Chdir("../../.."); err != nil {
		t.Fatal(err)
	}
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(cat) < 200 {
		t.Errorf("only %d catalog chunks", len(cat))
	}
}
