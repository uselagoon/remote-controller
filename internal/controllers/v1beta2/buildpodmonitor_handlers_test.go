package v1beta2

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestShouldMarkCancelled(t *testing.T) {
	tests := []struct {
		name   string
		status string
		phase  corev1.PodPhase
		want   bool
	}{
		{"running build, running pod", "Running", corev1.PodRunning, true},
		{"pending build, pending pod", "Pending", corev1.PodPending, true},
		{"running build, pod already succeeded", "Running", corev1.PodSucceeded, false},
		{"running build, pod already failed", "Running", corev1.PodFailed, false},
		{"completed build", "Complete", corev1.PodRunning, false},
		{"failed build", "Failed", corev1.PodRunning, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldMarkCancelled(tt.status, tt.phase); got != tt.want {
				t.Errorf("shouldMarkCancelled(%q, %q) = %v, want %v", tt.status, tt.phase, got, tt.want)
			}
		})
	}
}
