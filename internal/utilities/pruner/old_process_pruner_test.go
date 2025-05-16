package pruner

import (
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_calculateRemoveBeforeTimes(t *testing.T) {
	type args struct {
		p         *Pruner
		ns        v1.Namespace
		startTime time.Time
	}
	tests := []struct {
		name            string
		args            args
		buildBeforeTime time.Time
		taskBeforeTime  time.Time
		wantErr         bool
	}{
		{
			name: "Kill immediately (empty test)",
			args: args{
				p: &Pruner{
					TimeoutForBuildPods: 0,
					TimeoutForTaskPods:  0,
				},
				ns: v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{},
					},
				},
				startTime: time.Date(2000, 1, 1, 1, 1, 1, 1, time.Local),
			},
			buildBeforeTime: time.Date(2000, 1, 1, 1, 1, 1, 1, time.Local),
			taskBeforeTime:  time.Date(2000, 1, 1, 1, 1, 1, 1, time.Local),
		},
		{
			name: "Kill 6 hours earlier - given by pruner settings",
			args: args{
				p: &Pruner{
					TimeoutForBuildPods: 6,
					TimeoutForTaskPods:  6,
				},
				ns: v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{},
					},
				},
				startTime: time.Date(2000, 1, 1, 7, 1, 1, 1, time.Local),
			},
			buildBeforeTime: time.Date(2000, 1, 1, 1, 1, 1, 1, time.Local),
			taskBeforeTime:  time.Date(2000, 1, 1, 1, 1, 1, 1, time.Local),
		},
		{
			name: "Kill 1 hours earlier - given by ns override",
			args: args{
				p: &Pruner{
					TimeoutForBuildPods: 6,
					TimeoutForTaskPods:  6,
				},
				ns: v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"lagoon.sh/buildPodTimeout": "2",
							"lagoon.sh/taskPodTimeout":  "1",
						},
					},
				},
				startTime: time.Date(2000, 1, 1, 3, 1, 1, 1, time.Local),
			},
			buildBeforeTime: time.Date(2000, 1, 1, 1, 1, 1, 1, time.Local),
			taskBeforeTime:  time.Date(2000, 1, 1, 2, 1, 1, 1, time.Local),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, err := calculateRemoveBeforeTimes(tt.args.p, tt.args.ns, tt.args.startTime)
			if (err != nil) != tt.wantErr {
				t.Errorf("calculateRemoveBeforeTimes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.buildBeforeTime) {
				t.Errorf("calculateRemoveBeforeTimes() got = %v, buildBeforeTime %v", got, tt.buildBeforeTime)
			}
			if !reflect.DeepEqual(got1, tt.taskBeforeTime) {
				t.Errorf("calculateRemoveBeforeTimes() got1 = %v, buildBeforeTime %v", got1, tt.taskBeforeTime)
			}
		})
	}
}
