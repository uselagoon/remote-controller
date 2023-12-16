package harbor

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestHarbor_retentionOverrides(t *testing.T) {
	type fields struct {
		TagRetention TagRetention
	}
	type args struct {
		namespace corev1.Namespace
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   TagRetention
	}{
		{
			name: "test1",
			fields: fields{
				TagRetention: TagRetention{
					Enabled:              false,
					Schedule:             "M H(22-2) D(5-25) * *",
					PullRequestRetention: 2,
					BranchRetention:      5,
				},
			},
			args: args{
				namespace: corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"harbor.lagoon.sh/retention-policy": "{\"enabled\":true,\"schedule\":\"M H(2-15) D(5-15) * *\", \"pullrequestRetention\": 3, \"branchRetention\": 6}",
						},
					},
				},
			},
			want: TagRetention{
				Enabled:              true,
				Schedule:             "M H(2-15) D(5-15) * *",
				PullRequestRetention: 3,
				BranchRetention:      6,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Harbor{
				TagRetention: tt.fields.TagRetention,
			}
			if got := h.retentionOverrides(tt.args.namespace); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Harbor.retentionOverrides() = %v, want %v", got, tt.want)
			}
		})
	}
}
