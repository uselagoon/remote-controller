package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/uselagoon/remote-controller/test/utils"
)

// testDeletedBuildPod deletes a running build pod directly (as a node drain or eviction would)
// and validates the final status is shipped before the pod is removed
func testDeletedBuildPod(timeout string, duration, interval time.Duration) {
	namespace := "nginx-example-main"
	buildName := "lagoon-build-4m5apbc"

	ginkgo.By("creating a LagoonBuild resource via rabbitmq")
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
		"@test/e2e/testdata/cancellation/lagoon-build-4m5apbc.json",
		"http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish",
	)
	_, err := utils.Run(cmd)
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())

	ginkgo.By("validating that the build pod is created")
	verifyBuildPodCreates := func() error {
		cmd := exec.Command(
			utils.Kubectl(), "-n", namespace, "wait", "--for=create", "pod", buildName,
			fmt.Sprintf("--timeout=%s", timeout),
		)
		_, err := utils.Run(cmd)
		return err
	}
	gomega.EventuallyWithOffset(1, verifyBuildPodCreates, duration, interval).Should(gomega.Succeed())

	ginkgo.By("validating that the build pod is running and holds the build pod finalizer")
	verifyRunningWithFinalizer := func() error {
		cmd := exec.Command(utils.Kubectl(), "get", "pod", buildName, "-n", namespace,
			"-o", "jsonpath={.status.phase}/{.metadata.finalizers}")
		out, err := utils.Run(cmd)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(out), "Running/") {
			return fmt.Errorf("build pod not running yet: %s", out)
		}
		if !strings.Contains(string(out), "finalizer.lagoonbuildpod.crd.lagoon.sh/v1beta2") {
			return fmt.Errorf("build pod is missing the finalizer: %s", out)
		}
		return nil
	}
	gomega.EventuallyWithOffset(1, verifyRunningWithFinalizer, duration, interval).Should(gomega.Succeed())

	ginkgo.By("deleting the running build pod without a cancellation request")
	cmd = exec.Command(utils.Kubectl(), "delete", "pod", buildName, "-n", namespace, "--wait=false")
	_, err = utils.Run(cmd)
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())

	ginkgo.By("validating that the LagoonBuild reaches a terminal status")
	verifyBuildStatus := func() error {
		cmd := exec.Command(utils.Kubectl(), "get", "lagoonbuilds", buildName, "-n", namespace,
			"-o", "jsonpath={.status.phase}")
		out, err := utils.Run(cmd)
		if err != nil {
			return err
		}
		if string(out) != "Cancelled" {
			return fmt.Errorf("lagoonbuild in %s status", out)
		}
		return nil
	}
	gomega.EventuallyWithOffset(1, verifyBuildStatus, duration, interval).Should(gomega.Succeed())

	ginkgo.By("validating that the finalizer is released and the pod is removed")
	verifyPodGone := func() error {
		cmd := exec.Command(utils.Kubectl(), "get", "pod", buildName, "-n", namespace, "--ignore-not-found", "-o", "name")
		out, err := utils.Run(cmd)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(out)) != "" {
			return fmt.Errorf("build pod %s still exists", buildName)
		}
		return nil
	}
	gomega.EventuallyWithOffset(1, verifyPodGone, duration, interval).Should(gomega.Succeed())

	ginkgo.By("validating that the build status label was set to the terminal status")
	cmd = exec.Command(utils.Kubectl(), "get", "lagoonbuilds", buildName, "-n", namespace,
		"-o", "jsonpath={.metadata.labels.lagoon\\.sh/buildStatus}")
	out, err := utils.Run(cmd)
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
	gomega.ExpectWithOffset(1, string(out)).To(gomega.Equal("Cancelled"))
}
