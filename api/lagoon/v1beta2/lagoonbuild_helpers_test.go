package v1beta2

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func timeFromString(s string) time.Time {
	time, _ := time.Parse("2006-01-02T15:04:05.000Z", s)
	return time
}

func TestSortQueuedBuilds(t *testing.T) {
	type args struct {
		pendingBuilds []string
	}
	tests := []struct {
		name    string
		args    args
		want    []CachedBuildQueueItem
		wantErr bool
	}{
		{
			name: "test1 - 5 and 6 same time order by priority",
			args: args{
				pendingBuilds: []string{
					fmt.Sprintf(`{"name":"lagoon-build-abcdefg","namespace":"namespace1","priority":5,"creationTimestamp":%d}`, v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix()),
					fmt.Sprintf(`{"name":"lagoon-build-1234567","namespace":"namespace2","priority":6,"creationTimestamp":%d}`, v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix()),
				},
			},
			want: []CachedBuildQueueItem{
				{
					Name:              "lagoon-build-1234567",
					Priority:          6,
					Namespace:         "namespace2",
					CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix(),
				},
				{
					Name:              "lagoon-build-abcdefg",
					Priority:          5,
					Namespace:         "namespace1",
					CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix(),
				},
			},
		},
		{
			name: "test2 - 2x5 sorted by time",
			args: args{
				pendingBuilds: []string{
					fmt.Sprintf(`{"name":"lagoon-build-abcdefg","namespace":"namespace1","priority":5,"creationTimestamp":%d}`, v1.NewTime(timeFromString("2023-09-18T11:50:00.000Z")).Unix()),
					fmt.Sprintf(`{"name":"lagoon-build-1234567","namespace":"namespace2","priority":5,"creationTimestamp":%d}`, v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix()),
				},
			},
			want: []CachedBuildQueueItem{
				{
					Name:              "lagoon-build-1234567",
					Priority:          5,
					Namespace:         "namespace2",
					CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix(),
				},
				{
					Name:              "lagoon-build-abcdefg",
					Priority:          5,
					Namespace:         "namespace1",
					CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:50:00.000Z")).Unix(),
				},
			},
		},
		{
			name: "test2 - 2x5 sorted by time",
			args: args{
				pendingBuilds: []string{
					fmt.Sprintf(`{"name":"lagoon-build-abcdefg","namespace":"namespace1","priority":5,"creationTimestamp":%d}`, v1.NewTime(timeFromString("2023-09-18T11:50:00.000Z")).Unix()),
					fmt.Sprintf(`{"name":"lagoon-build-abc1234","namespace":"namespace3","priority":6,"creationTimestamp":%d}`, v1.NewTime(timeFromString("2023-09-18T11:46:00.000Z")).Unix()),
					fmt.Sprintf(`{"name":"lagoon-build-1234567","namespace":"namespace2","priority":5,"creationTimestamp":%d}`, v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix()),
				},
			},
			want: []CachedBuildQueueItem{
				{
					Name:              "lagoon-build-abc1234",
					Priority:          6,
					Namespace:         "namespace3",
					CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:46:00.000Z")).Unix(),
				},
				{
					Name:              "lagoon-build-1234567",
					Priority:          5,
					Namespace:         "namespace2",
					CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix(),
				},
				{
					Name:              "lagoon-build-abcdefg",
					Priority:          5,
					Namespace:         "namespace1",
					CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:50:00.000Z")).Unix(),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SortQueuedBuilds(tt.args.pendingBuilds)
			if (err != nil) != tt.wantErr {
				t.Errorf("SortQueuedBuilds() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SortQueuedBuilds() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSortQueuedNamespaceBuilds(t *testing.T) {
	type args struct {
		namespace     string
		pendingBuilds []string
	}
	tests := []struct {
		name    string
		args    args
		want    []CachedBuildQueueItem
		wantErr bool
	}{
		{
			name: "test1 - namespace1 builds only sorted by creation",
			args: args{
				namespace: "namespace1",
				pendingBuilds: []string{
					fmt.Sprintf(`{"name":"lagoon-build-abcdefg","namespace":"namespace1","priority":5,"creationTimestamp":%d}`, v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix()),
					fmt.Sprintf(`{"name":"lagoon-build-1234567","namespace":"namespace2","priority":6,"creationTimestamp":%d}`, v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix()),
					fmt.Sprintf(`{"name":"lagoon-build-abc1234","namespace":"namespace1","priority":6,"creationTimestamp":%d}`, v1.NewTime(timeFromString("2023-09-18T11:46:00.000Z")).Unix()),
				},
			},
			want: []CachedBuildQueueItem{
				{
					Name:              "lagoon-build-abc1234",
					Priority:          6,
					Namespace:         "namespace1",
					CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:46:00.000Z")).Unix(),
				},
				{
					Name:              "lagoon-build-abcdefg",
					Priority:          5,
					Namespace:         "namespace1",
					CreationTimestamp: v1.NewTime(timeFromString("2023-09-18T11:45:00.000Z")).Unix(),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SortQueuedNamespaceBuilds(tt.args.namespace, tt.args.pendingBuilds)
			if (err != nil) != tt.wantErr {
				t.Errorf("SortQueuedNamespaceBuilds() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SortQueuedNamespaceBuilds() = %v, want %v", got, tt.want)
			}
		})
	}
}
