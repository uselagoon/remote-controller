package harbor

import (
	"testing"
)

func TestHarbor_matchRobotAccount(t *testing.T) {
	type args struct {
		harbor          Harbor
		robotName       string
		projectName     string
		environmentName string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "test1",
			args: args{
				robotName:       "robot$main-954f2d24",
				projectName:     "example-com",
				environmentName: "main",
				harbor: Harbor{
					RobotPrefix:      "robot$",
					LagoonTargetName: "ci-local-controller-kubernetes",
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &tt.args.harbor
			if got := h.matchRobotAccount(tt.args.robotName, tt.args.projectName, tt.args.environmentName); got != tt.want {
				t.Errorf("Harbor.matchRobotAccount() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHarbor_matchRobotAccountV2(t *testing.T) {
	type args struct {
		harbor          Harbor
		robotName       string
		projectName     string
		environmentName string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "test1",
			args: args{
				robotName:       "robot$example-com+main-954f2d24",
				projectName:     "example-com",
				environmentName: "main",
				harbor: Harbor{
					RobotPrefix:      "robot$",
					LagoonTargetName: "ci-local-controller-kubernetes",
				},
			},
			want: true,
		},
		{
			name: "test2",
			args: args{
				robotName:       "robot$example-com+dbb0b39699dab6abc69f-954f2d24",
				projectName:     "example-com",
				environmentName: "dash--main",
				harbor: Harbor{
					RobotPrefix:      "robot$",
					LagoonTargetName: "ci-local-controller-kubernetes",
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &tt.args.harbor
			if got := h.matchRobotAccountV2(tt.args.robotName, tt.args.projectName, tt.args.environmentName); got != tt.want {
				t.Errorf("Harbor.matchRobotAccountV2() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_harborRobotV2Regex(t *testing.T) {
	type args struct {
		name string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test1",
			args: args{
				name: "my-silly--double-dash-environment",
			},
			want: "688a0543ea9ccc77827e",
		},
		{
			name: "test2",
			args: args{
				name: "my-valid-single-dash-environment",
			},
			want: "my-valid-single-dash-environment",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := harborRobotV2Regex(tt.args.name); got != tt.want {
				t.Errorf("harborRobotV2Regex() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHarbor_generateRobotWithPrefixV2(t *testing.T) {
	type args struct {
		harbor          Harbor
		projectName     string
		environmentName string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test1",
			args: args{
				projectName:     "my-project",
				environmentName: "the-environment",
				harbor: Harbor{
					LagoonTargetName: "lagoon",
					RobotPrefix:      "robot$",
				},
			},
			want: "robot$my-project+the-environment-a30fb066",
		},
		{
			name: "test1",
			args: args{
				projectName:     "my-project",
				environmentName: "the-double--environment",
				harbor: Harbor{
					LagoonTargetName: "lagoon",
					RobotPrefix:      "robot$",
				},
			},
			want: "robot$my-project+734de25907e26b092014-a30fb066",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &tt.args.harbor
			if got := h.generateRobotWithPrefixV2(tt.args.projectName, tt.args.environmentName); got != tt.want {
				t.Errorf("Harbor.generateRobotWithPrefixV2() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHarbor_generateRobotName(t *testing.T) {
	type args struct {
		harbor          Harbor
		environmentName string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test1",
			args: args{
				environmentName: "the-environment",
				harbor: Harbor{
					LagoonTargetName: "lagoon",
				},
			},
			want: "the-environment-a30fb066",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &tt.args.harbor
			if got := h.generateRobotName(tt.args.environmentName); got != tt.want {
				t.Errorf("Harbor.generateRobotName() = %v, want %v", got, tt.want)
			}
		})
	}
}
