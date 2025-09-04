package dockerhost

import (
	"testing"

	lru "github.com/hashicorp/golang-lru/v2"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_pickHost(t *testing.T) {
	type args struct {
		reuseType string
		chosen    string
		available []string
		inUse     map[string]int
		qosMax    int
	}
	tests := []struct {
		name        string
		description string
		args        args
		want        string
	}{
		{
			name:        "test1",
			description: "qos, choose lowest in use dockerhost",
			args: args{
				chosen: "dockerhost1",
				available: []string{
					"dockerhost1",
					"dockerhost2",
				},
				inUse: map[string]int{
					"dockerhost1": 2,
				},
				qosMax: 3,
			},
			want: "dockerhost2",
		},
		{
			name:        "test2",
			description: "qos, choose lowest in use dockerhost",
			args: args{
				chosen: "dockerhost2",
				available: []string{
					"dockerhost1",
					"dockerhost2",
				},
				inUse: map[string]int{
					"dockerhost1": 2,
				},
				qosMax: 3,
			},
			want: "dockerhost2",
		},
		{
			name:        "test3",
			description: "qos, in use dockerhost no longer exists choose from available",
			args: args{
				chosen: "dockerhost2",
				available: []string{
					"dockerhost2",
				},
				inUse: map[string]int{
					"dockerhost1": 2,
				},
				qosMax: 3,
			},
			want: "dockerhost2",
		},
		{
			name:        "test4",
			description: "qos, choose lowest in use dockerhost",
			args: args{
				chosen: "dockerhost2",
				available: []string{
					"dockerhost1",
					"dockerhost2",
					"dockerhost3",
				},
				inUse: map[string]int{
					"dockerhost1": 2,
					"dockerhost2": 1,
					"dockerhost3": 2,
				},
				qosMax: 3,
			},
			want: "dockerhost2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sScheme := runtime.NewScheme()
			clientBuilder := ctrlfake.NewClientBuilder()
			clientBuilder = clientBuilder.WithScheme(k8sScheme)

			fakeClient := clientBuilder.Build()
			logs := ctrl.Log.WithName("dockerhost")
			d := New(fakeClient, logs, "lagoon", tt.args.reuseType, &lru.Cache[string, string]{}, &lru.Cache[string, string]{})
			if got := d.pickHost(tt.args.chosen, tt.args.available, tt.args.inUse, tt.args.qosMax); got != tt.want {
				t.Errorf("pickHost() = %v, want %v", got, tt.want)
			}
		})
	}
}
