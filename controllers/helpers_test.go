package controllers

import "testing"

func TestShortName(t *testing.T) {
	var testCases = map[string]struct {
		input  string
		expect string
	}{
		"small string 0": {input: "foo", expect: "fqtli23i"},
		"small string 1": {input: "bar", expect: "7tpcwlw3"},
		"small string 2": {input: "bard", expect: "sbmpphej"},
		"large string 0": {input: "very-very-very-long-string-here-much-more-than-sixty-three-chars", expect: "iil2toyi"},
		"large string 1": {input: "very-very-very-long-string-here-much-more-than-sixty-three-characters", expect: "54flwlga"},
	}
	for name, tc := range testCases {
		t.Run(name, func(tt *testing.T) {
			if output := shortName(tc.input); output != tc.expect {
				tt.Fatalf("expected: %v, got: %v", tc.expect, output)
			}
		})
	}
}
