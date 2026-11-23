package v1beta2

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// slice of the different failure states of pods that we care about
// if we observe these on a pending pod, fail the build and get the logs
var failureStates = []string{
	"CrashLoopBackOff",
	"ImagePullBackOff",
}

const failureReasonAnnotation = "lagoon.sh/failure-reason"

// failureReason returns a bounded label describing why a build or task pod failed.
func failureReason(pod *corev1.Pod) string {
	if reason, ok := pod.Annotations[failureReasonAnnotation]; ok {
		return reason
	}
	switch pod.Status.Reason {
	case "Evicted":
		return "pod_evicted"
	case "DeadlineExceeded":
		return "deadline_exceeded"
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.State.Waiting != nil {
			if reason := waitingFailureReason(container.State.Waiting.Reason); reason != "" {
				return reason
			}
		}
		if terminated := container.State.Terminated; terminated != nil {
			if terminated.Reason == "OOMKilled" {
				return "oom_killed"
			}
			if terminated.ExitCode != 0 {
				return "step_failed"
			}
		}
	}
	return "unknown"
}

func waitingFailureReason(reason string) string {
	switch reason {
	case "ImagePullBackOff", "ErrImagePull":
		return "image_pull_error"
	case "CrashLoopBackOff":
		return "crash_loop"
	}
	return ""
}

// markFailureReason records the reason on the in-memory pod before its container status is overwritten.
func markFailureReason(pod *corev1.Pod, waitingReason string) {
	reason := waitingFailureReason(waitingReason)
	if reason == "" {
		reason = "unknown"
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[failureReasonAnnotation] = reason
}

// podStartDelay returns how long a pod took from creation until its container started.
func podStartDelay(pod *corev1.Pod) (time.Duration, bool) {
	for _, container := range pod.Status.ContainerStatuses {
		var started time.Time
		switch {
		case container.State.Running != nil:
			started = container.State.Running.StartedAt.Time
		case container.State.Terminated != nil:
			started = container.State.Terminated.StartedAt.Time
		}
		if !started.IsZero() && started.After(pod.CreationTimestamp.Time) {
			return started.Sub(pod.CreationTimestamp.Time), true
		}
	}
	return 0, false
}

// getContainerLogs grabs the logs from a given container
func getContainerLogs(ctx context.Context, containerName string, request ctrl.Request) ([]byte, error) {
	restCfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("unable to create client: %v", err)
	}
	req := clientset.CoreV1().Pods(request.Namespace).GetLogs(request.Name, &corev1.PodLogOptions{Container: containerName})
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("error in opening stream: %v", err)
	}
	defer podLogs.Close()
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return nil, fmt.Errorf("error in copy information from podLogs to buffer: %v", err)
	}
	return buf.Bytes(), nil
}
