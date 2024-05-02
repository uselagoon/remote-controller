package v1beta2

import (
	"testing"
)

func TestCheckLagoonVersion(t *testing.T) {
	type args struct {
		build        *LagoonBuild
		checkVersion string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "test1",
			args: args{
				build: &LagoonBuild{
					Spec: LagoonBuildSpec{
						Project: Project{
							Variables: LagoonVariables{
								Project: []byte(`[{"name":"LAGOON_SYSTEM_CORE_VERSION","value":"v2.12.0","scope":"internal_system"}]`),
							},
						},
					},
				},
				checkVersion: "2.12.0",
			},
			want: true,
		},
		{
			name: "test2",
			args: args{
				build: &LagoonBuild{
					Spec: LagoonBuildSpec{
						Project: Project{
							Variables: LagoonVariables{
								Project: []byte(`[{"name":"LAGOON_SYSTEM_CORE_VERSION","value":"v2.11.0","scope":"internal_system"}]`),
							},
						},
					},
				},
				checkVersion: "2.12.0",
			},
			want: false,
		},
		{
			name: "test3",
			args: args{
				build: &LagoonBuild{
					Spec: LagoonBuildSpec{
						Project: Project{
							Variables: LagoonVariables{
								Project: []byte(`[]`),
							},
						},
					},
				},
				checkVersion: "2.12.0",
			},
			want: false,
		},
		{
			name: "test4",
			args: args{
				build: &LagoonBuild{
					Spec: LagoonBuildSpec{
						Project: Project{
							Variables: LagoonVariables{
								Project: []byte(`[{"name":"LAGOON_SYSTEM_CORE_VERSION","value":"v2.12.0","scope":"internal_system"}]`),
							},
						},
					},
				},
				checkVersion: "v2.12.0",
			},
			want: true,
		},
		{
			name: "test5",
			args: args{
				build: &LagoonBuild{
					Spec: LagoonBuildSpec{
						Project: Project{
							Variables: LagoonVariables{
								Project: []byte(`[{"name":"LAGOON_SYSTEM_CORE_VERSION","value":"v2.11.0","scope":"internal_system"}]`),
							},
						},
					},
				},
				checkVersion: "v2.12.0",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckLagoonVersion(tt.args.build, tt.args.checkVersion); got != tt.want {
				t.Errorf("CheckLagoonVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}
