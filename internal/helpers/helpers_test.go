package helpers

import (
	"testing"
)

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
			if output := ShortName(tc.input); output != tc.expect {
				tt.Fatalf("expected: %v, got: %v", tc.expect, output)
			}
		})
	}
}

func TestStringToUint(t *testing.T) {
	var testCases = map[string]struct {
		input  string
		expect *uint
	}{
		"uint 0":     {input: "1", expect: UintPtr(1)},
		"uint 1":     {input: "1234", expect: UintPtr(1234)},
		"uint 2":     {input: "6789", expect: UintPtr(6789)},
		"nil uint 0": {input: "", expect: nil},
		"nil uint 1": {input: "a2", expect: nil},
	}
	for name, tc := range testCases {
		t.Run(name, func(tt *testing.T) {
			output := StringToUintPtr(tc.input)
			if tc.expect == nil {
				if output != tc.expect {
					tt.Fatalf("expected: %d, got: %d", tc.expect, output)
				}
			} else {
				if *output != *tc.expect {
					tt.Fatalf("expected: %d, got: %d", *tc.expect, *output)
				}
			}
		})
	}
}
