package dockerhost

import "testing"

func TestDesiredReplicas(t *testing.T) {
	max := int64(3)
	tests := []struct {
		name                 string
		current, min         int64
		max                  *int64
		qos, queued, running int
		want                 int64
	}{
		{"below threshold", 1, 1, &max, 10, 29, 10, 1},
		{"triple qos scales up", 1, 1, &max, 10, 30, 10, 2},
		{"clamped to max", 1, 1, &max, 10, 100, 10, 3},
		{"holds while running", 2, 1, &max, 10, 0, 4, 2},
		{"holds while queued below threshold", 2, 1, &max, 10, 5, 10, 2},
		{"scales down when idle", 3, 1, &max, 10, 0, 0, 1},
		{"unbounded max", 1, 1, nil, 10, 60, 10, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := desiredReplicas(tt.current, tt.min, tt.max, tt.qos, 3, tt.queued, tt.running); got != tt.want {
				t.Errorf("got %d want %d", got, tt.want)
			}
		})
	}
}

func TestScaleLimit(t *testing.T) {
	if got := scaleLimit(10, 2, 1); got != 20 {
		t.Errorf("got %d", got)
	}
	if got := scaleLimit(10, 0, 1); got != 10 {
		t.Errorf("got %d", got)
	}
	if got := scaleLimit(10, 3, 0); got != 30 {
		t.Errorf("got %d", got)
	}
}

func TestDesiredReplicasMultiplier(t *testing.T) {
	max := int64(5)
	if got := desiredReplicas(1, 1, &max, 10, 2, 20, 10); got != 2 {
		t.Errorf("got %d want 2", got)
	}
	if got := desiredReplicas(1, 1, &max, 10, 0, 100, 10); got != 1 {
		t.Errorf("got %d want 1", got)
	}
}
