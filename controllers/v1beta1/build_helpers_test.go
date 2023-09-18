package v1beta1

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func timeFromString(s string) time.Time {
	time, _ := time.Parse("2006-01-02T15:04:05.000Z", s)
	return time
}

func Test_sortBuilds(t *testing.T) {
	type args struct {
		defaultPriority int
		pendingBuilds   *lagoonv1beta1.LagoonBuildList
	}
	tests := []struct {
		name       string
		args       args
		wantBuilds *lagoonv1beta1.LagoonBuildList
	}{
		{
			name: "test1 - 5 and 6 same time order by priority",
			args: args{
				defaultPriority: 5,
				pendingBuilds: &lagoonv1beta1.LagoonBuildList{
					Items: []lagoonv1beta1.LagoonBuild{
						{
							ObjectMeta: v1.ObjectMeta{
								Name:              "lagoon-build-abcdefg",
								CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")),
							},
							Spec: lagoonv1beta1.LagoonBuildSpec{
								Build: lagoonv1beta1.Build{
									Priority: helpers.IntPtr(5),
								},
							},
						},
						{
							ObjectMeta: v1.ObjectMeta{
								Name:              "lagoon-build-1234567",
								CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")),
							},
							Spec: lagoonv1beta1.LagoonBuildSpec{
								Build: lagoonv1beta1.Build{
									Priority: helpers.IntPtr(6),
								},
							},
						},
					},
				},
			},
			wantBuilds: &lagoonv1beta1.LagoonBuildList{
				Items: []lagoonv1beta1.LagoonBuild{
					{
						ObjectMeta: v1.ObjectMeta{
							Name:              "lagoon-build-abcdefg",
							CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")),
						},
						Spec: lagoonv1beta1.LagoonBuildSpec{
							Build: lagoonv1beta1.Build{
								Priority: helpers.IntPtr(5),
							},
						},
					},
					{
						ObjectMeta: v1.ObjectMeta{
							Name:              "lagoon-build-1234567",
							CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")),
						},
						Spec: lagoonv1beta1.LagoonBuildSpec{
							Build: lagoonv1beta1.Build{
								Priority: helpers.IntPtr(6),
							},
						},
					},
				},
			},
		},
		{
			name: "test2 - 2x5 sorted by time",
			args: args{
				defaultPriority: 5,
				pendingBuilds: &lagoonv1beta1.LagoonBuildList{
					Items: []lagoonv1beta1.LagoonBuild{
						{
							ObjectMeta: v1.ObjectMeta{
								Name:              "lagoon-build-abcdefg",
								CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:50:00.000Z")),
							},
							Spec: lagoonv1beta1.LagoonBuildSpec{
								Build: lagoonv1beta1.Build{
									Priority: helpers.IntPtr(5),
								},
							},
						},
						{
							ObjectMeta: v1.ObjectMeta{
								Name:              "lagoon-build-1234567",
								CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")),
							},
							Spec: lagoonv1beta1.LagoonBuildSpec{
								Build: lagoonv1beta1.Build{
									Priority: helpers.IntPtr(5),
								},
							},
						},
					},
				},
			},
			wantBuilds: &lagoonv1beta1.LagoonBuildList{
				Items: []lagoonv1beta1.LagoonBuild{
					{
						ObjectMeta: v1.ObjectMeta{
							Name:              "lagoon-build-1234567",
							CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")),
						},
						Spec: lagoonv1beta1.LagoonBuildSpec{
							Build: lagoonv1beta1.Build{
								Priority: helpers.IntPtr(5),
							},
						},
					},
					{
						ObjectMeta: v1.ObjectMeta{
							Name:              "lagoon-build-abcdefg",
							CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:50:00.000Z")),
						},
						Spec: lagoonv1beta1.LagoonBuildSpec{
							Build: lagoonv1beta1.Build{
								Priority: helpers.IntPtr(5),
							},
						},
					},
				},
			},
		},
		{
			name: "test3 - 2x5 and 1x6 sorted by priority then time",
			args: args{
				defaultPriority: 5,
				pendingBuilds: &lagoonv1beta1.LagoonBuildList{
					Items: []lagoonv1beta1.LagoonBuild{
						{
							ObjectMeta: v1.ObjectMeta{
								Name:              "lagoon-build-abcdefg",
								CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:50:00.000Z")),
							},
							Spec: lagoonv1beta1.LagoonBuildSpec{
								Build: lagoonv1beta1.Build{
									Priority: helpers.IntPtr(5),
								},
							},
						},
						{
							ObjectMeta: v1.ObjectMeta{
								Name:              "lagoon-build-abc1234",
								CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:46:00.000Z")),
							},
							Spec: lagoonv1beta1.LagoonBuildSpec{
								Build: lagoonv1beta1.Build{
									Priority: helpers.IntPtr(6),
								},
							},
						},
						{
							ObjectMeta: v1.ObjectMeta{
								Name:              "lagoon-build-1234567",
								CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")),
							},
							Spec: lagoonv1beta1.LagoonBuildSpec{
								Build: lagoonv1beta1.Build{
									Priority: helpers.IntPtr(5),
								},
							},
						},
					},
				},
			},
			wantBuilds: &lagoonv1beta1.LagoonBuildList{
				Items: []lagoonv1beta1.LagoonBuild{
					{
						ObjectMeta: v1.ObjectMeta{
							Name:              "lagoon-build-1234567",
							CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")),
						},
						Spec: lagoonv1beta1.LagoonBuildSpec{
							Build: lagoonv1beta1.Build{
								Priority: helpers.IntPtr(5),
							},
						},
					},
					{
						ObjectMeta: v1.ObjectMeta{
							Name:              "lagoon-build-abcdefg",
							CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:50:00.000Z")),
						},
						Spec: lagoonv1beta1.LagoonBuildSpec{
							Build: lagoonv1beta1.Build{
								Priority: helpers.IntPtr(5),
							},
						},
					},
					{
						ObjectMeta: v1.ObjectMeta{
							Name:              "lagoon-build-abc1234",
							CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:46:00.000Z")),
						},
						Spec: lagoonv1beta1.LagoonBuildSpec{
							Build: lagoonv1beta1.Build{
								Priority: helpers.IntPtr(6),
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sortBuilds(tt.args.defaultPriority, tt.args.pendingBuilds)
			if !cmp.Equal(tt.args.pendingBuilds, tt.wantBuilds) {
				t.Errorf("sortBuilds() = %v, want %v", tt.args.pendingBuilds, tt.wantBuilds)
			}
		})
	}
}
