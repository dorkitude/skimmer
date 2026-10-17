package kit

import "testing"

func TestMappedLocator(t *testing.T) {
	const original = "alpha beta gamma delta"
	const modified = "alpha beta\n\nPLANTED TEXT\n\ngamma delta"
	cases := []struct {
		name, doc, text string
		want            Span
	}{
		{"unchanged", original, "beta gamma", Span{5, 14}},
		{"before insertion", modified, "alpha beta", Span{0, 9}},
		{"across insertion", modified, "beta PLANTED TEXT gamma", Span{5, 14}},
		{"ends inside insertion", modified, "beta PLANTED", Span{5, 9}},
		{"starts inside insertion", modified, "TEXT gamma", Span{9, 14}},
		{"insertion only", modified, "PLANTED TEXT", Span{9, 9}},
		{"after insertion", modified, "gamma delta", Span{9, 19}},
		{"shifted chunk", modified, "pha beta PLANTED TEXT gamma delta", Span{2, 19}},
		{"whole document", modified, modified, Span{0, 19}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc, err := NewMappedLocator(original, tc.doc)
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := loc.Locate(tc.text); !ok || got != tc.want {
				t.Fatalf("Locate = %v, %v; want %v, true", got, ok, tc.want)
			}
		})
	}
	for _, doc := range []string{"alpha gamma delta", "alpha CHANGED gamma delta", "extra alpha beta middle gamma delta"} {
		if _, err := NewMappedLocator(original, doc); err == nil {
			t.Errorf("accepted unsupported edits: %q", doc)
		}
	}
}

func TestMappedLocatorDocumentOrder(t *testing.T) {
	loc, err := NewMappedLocator("first repeated last repeated end", "first repeated PLANTED last repeated end")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first repeated", "repeated PLANTED last", "last repeated", "repeated end"} {
		if _, ok := loc.Locate(text); !ok {
			t.Fatalf("could not locate %q", text)
		}
	}
	if got, ok := loc.Locate("repeated end"); !ok || got != (Span{17, 28}) {
		t.Fatalf("last chunk = %v, %v", got, ok)
	}
}
