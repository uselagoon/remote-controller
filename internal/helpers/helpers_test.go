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

func TestGenerateNamespaceName(t *testing.T) {
	type args struct {
		pattern             string
		environmentName     string
		projectname         string
		prefix              string
		controllerNamespace string
		randomPrefix        bool
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "really long environment name with slash and capitals",
			args: args{
				pattern:             "",
				environmentName:     "Feature/Really-Exceedingly-Long-Environment-Name-For-A-Branch",
				projectname:         "this-is-my-project",
				prefix:              "",
				controllerNamespace: "lagoon",
				randomPrefix:        false,
			},
			want: "this-is-my-project-feature-really-exceedingly-long-env-dc8c",
		},
		{
			name: "really long environment name with slash and no capitals",
			args: args{
				pattern:             "",
				environmentName:     "feature/really-exceedingly-long-environment-name-for-a-branch",
				projectname:         "this-is-my-project",
				prefix:              "",
				controllerNamespace: "lagoon",
				randomPrefix:        false,
			},
			want: "this-is-my-project-feature-really-exceedingly-long-env-dc8c",
		},
		{
			name: "short environment name with slash and capitals",
			args: args{
				pattern:             "",
				environmentName:     "Feature/Branch",
				projectname:         "this-is-my-project",
				prefix:              "",
				controllerNamespace: "lagoon",
				randomPrefix:        false,
			},
			want: "this-is-my-project-feature-branch",
		},
		{
			name: "short environment name with slash and no capitals",
			args: args{
				pattern:             "",
				environmentName:     "feature/branch",
				projectname:         "this-is-my-project",
				prefix:              "",
				controllerNamespace: "lagoon",
				randomPrefix:        false,
			},
			want: "this-is-my-project-feature-branch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GenerateNamespaceName(tt.args.pattern, tt.args.environmentName, tt.args.projectname, tt.args.prefix, tt.args.controllerNamespace, tt.args.randomPrefix); got != tt.want {
				t.Errorf("GenerateNamespaceName() got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMakeSafe(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "slash in name",
			in:   "Feature/Branch",
			want: "feature-branch",
		},
		{
			name: "noslash in name",
			in:   "Feature-Branch",
			want: "feature-branch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MakeSafe(tt.in); got != tt.want {
				t.Errorf("MakeSafe() go %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHashString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "generate hash",
			in:   "feature-branch",
			want: "011122006d017c21d1376add9f7f65b43555a455",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HashString(tt.in); got != tt.want {
				t.Errorf("HashString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShortenEnvironment(t *testing.T) {
	type args struct {
		project     string
		environment string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "really long environment name with slash and capitals",
			args: args{
				environment: MakeSafe("Feature/Really-Exceedingly-Long-Environment-Name-For-A-Branch"),
				project:     "this-is-my-project",
			},
			want: "feature-really-exceedingly-long-env-dc8c",
		},
		{
			name: "short environment name",
			args: args{
				environment: MakeSafe("Feature/Branch"),
				project:     "this-is-my-project",
			},
			want: "feature-branch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShortenEnvironment(tt.args.project, tt.args.environment); got != tt.want {
				t.Errorf("ShortenEnvironment() got %v, want %v", got, tt.want)
			}
		})
	}
}
