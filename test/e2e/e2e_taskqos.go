package e2e

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/uselagoon/remote-controller/test/utils"
)

func testTaskQoS(timeout string, duration, interval time.Duration) {
	var tasks = []struct {
		Name      string
		Namespace string
	}{
		{
			Name:      "1xpyz5m",
			Namespace: "nginx-example-main",
		},
		{
			Name:      "2xpyz5m",
			Namespace: "nginx-example-dev1",
		},
		{
			Name:      "3xpyz5m",
			Namespace: "nginx-example-dev2",
		},
		{
			Name:      "4xpyz5m",
			Namespace: "nginx-example-dev3",
		},
	}
	ginkgo.By("validating that lagoontasks are queing")
	// these tasks use the task test image with a sleep to ensure there is enough
	// time for QoS to be tested
	for _, task := range tasks {
		ginkgo.By("creating a LagoonTask resource via rabbitmq")
		cmd := exec.Command(
			"curl",
			"-s",
			"-u",
			"guest:guest",
			"-H",
			"'Accept: application/json'",
			"-H",
			"'Content-Type:application/json'",
			"-X",
			"POST",
			"-d",
			fmt.Sprintf("@test/e2e/testdata/taskqos/lagoon-task-%s.json", task.Name),
			"http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish",
		)
		_, err := utils.Run(cmd)
		gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
		time.Sleep(1 * time.Second)
	}
	time.Sleep(5 * time.Second)
	for _, task := range tasks {
		ginkgo.By("validating that the LagoonTask task pod is created")
		cmd := exec.Command(
			utils.Kubectl(),
			"-n", task.Namespace,
			"wait",
			"--for=condition=Ready",
			"pod",
			fmt.Sprintf("lagoon-task-%s", task.Name),
			fmt.Sprintf("--timeout=%s", timeout),
		)
		_, err := utils.Run(cmd)
		if task.Name == "4xpyz5m" {
			// should fail because it gets queued at 3 tasks max
			gomega.ExpectWithOffset(1, err).To(gomega.HaveOccurred())
			// then wait for the task pod to start
			verifyTaskRuns := func() error {
				cmd = exec.Command(utils.Kubectl(), "get",
					"pods", fmt.Sprintf("lagoon-task-%s", task.Name),
					"-o", "jsonpath={.status.phase}",
					"-n", task.Namespace,
				)
				status, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(status) != "Running" {
					return fmt.Errorf("task pod in %s status", status)
				}
				return nil
			}
			// if this fails, then qos didn't start the pod for some reason in the duration available
			gomega.EventuallyWithOffset(1, verifyTaskRuns, duration, interval).Should(gomega.Succeed())

			ginkgo.By("validating that the lagoon-task pod completes as expected")
			verifyTaskPodCompletes := func() error {
				// Validate pod status
				cmd = exec.Command(utils.Kubectl(), "get",
					"pods", fmt.Sprintf("lagoon-task-%s", task.Name), "-o", "jsonpath={.status.phase}",
					"-n", task.Namespace,
				)
				status, err := utils.Run(cmd)
				gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred())
				if string(status) != "Succeeded" {
					return fmt.Errorf("controller pod in %s status", status)
				}
				return nil
			}
			gomega.EventuallyWithOffset(1, verifyTaskPodCompletes, duration, interval).Should(gomega.Succeed())
		} else {
			// should pass because qos tasks
			gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
		}
	}
}
