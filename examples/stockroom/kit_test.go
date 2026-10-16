package main

import (
	"os"
	"strings"
	"testing"
)

// The aisle colours are the gallery's five-series ramp, copied into a Go map
// because a map cannot read a stylesheet. This is the test that keeps the two
// from drifting: change --lp-c3 in the kit and this fails rather than an aisle
// quietly becoming a colour no other example in the gallery uses.
func TestAisleColoursMatchTheKit(t *testing.T) {
	css, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}
	text := string(css)

	for _, want := range []struct{ token, hex string }{
		{"--lp-c1", "#4338CA"},
		{"--lp-c2", "#0E9AA7"},
		{"--lp-c3", "#2E9E63"},
		{"--lp-c4", "#8B5CF6"},
		{"--lp-c5", "#C2703B"},
	} {
		if !strings.Contains(text, want.token+": "+want.hex) {
			t.Errorf("%s is no longer %s in toft-kit.css; the aisle colours in "+
				"render.go were copied from it and have to move with it", want.token, want.hex)
		}
		found := false
		for _, c := range catColors {
			if c == want.hex {
				found = true
			}
		}
		if !found {
			t.Errorf("no aisle uses %s (%s); the five aisles are the five series",
				want.token, want.hex)
		}
	}

	if len(catColors) != 5 {
		t.Errorf("five aisles and five series: got %d aisles", len(catColors))
	}
}
