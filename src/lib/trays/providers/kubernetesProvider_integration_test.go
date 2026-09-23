//go:build integration_k8s

package providers

import (
	"cattery/lib/config"
	"cattery/lib/kube"
	"cattery/lib/trays"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// These tests run against a real cluster reachable through the ambient
// kubeconfig (CI: the kind cluster from helm/kind-action; a dev box: the
// current kubectl context) in the context's namespace. They pull two public
// images: registry.k8s.io/pause (the "runner") and a published cattery image
// (the agent carrier; override with CATTERY_TEST_AGENT_IMAGE, e.g. after
// `kind load docker-image`).
//
// The copied agent binary runs inside the pause image and tries to register
// with a black-hole advertiseUrl: its HTTP client has no timeout, so the pod
// stays Running long enough for the assertions.

const (
	itRunnerImage     = "registry.k8s.io/pause:3.10"
	itBlackHoleServer = "http://10.255.255.1:5137"
)

func itAgentImage() string {
	if v := os.Getenv("CATTERY_TEST_AGENT_IMAGE"); v != "" {
		return v
	}
	return "docker.io/paritytech/cattery:latest"
}

func itProvider(t *testing.T, timeout time.Duration) (*KubernetesProvider, kubernetes.Interface, string) {
	t.Helper()
	restCfg, kubeNs, err := kube.NewRestConfig(kube.Options{})
	require.NoError(t, err, "load kubeconfig")
	client, err := kubernetes.NewForConfig(restCfg)
	require.NoError(t, err)
	ns := os.Getenv("CATTERY_TEST_NAMESPACE")
	if ns == "" {
		ns = kubeNs
	}
	return newKubernetesProvider("it-k8s", client, ns, timeout), client, ns
}

// itTray installs a tray type built from raw and returns a tray with a
// unique id. The Job is deleted on cleanup whatever the test did.
func itTray(t *testing.T, client kubernetes.Interface, ns, advertiseURL string, raw map[string]any) *trays.Tray {
	t.Helper()
	kc := typedK8sConfig(t, raw)
	require.NoError(t, kc.Validate("it-k8s"))
	cfg := &config.CatteryConfig{
		Server: config.ServerConfig{ListenAddress: ":0", AdvertiseUrl: advertiseURL},
		TrayTypes: []*config.TrayType{{
			Name: "it-k8s", Provider: "it-k8s", GitHubOrg: "org", RunnerGroupId: 1, Config: kc,
		}},
	}
	config.SetForTest(t, cfg)

	b := make([]byte, 8)
	_, err := rand.Read(b)
	require.NoError(t, err)
	tray := newTestTray("it-k8s", "it-k8s-"+hex.EncodeToString(b))

	t.Cleanup(func() {
		policy := metav1.DeletePropagationBackground
		err := client.BatchV1().Jobs(ns).Delete(context.Background(), tray.Id, metav1.DeleteOptions{PropagationPolicy: &policy})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Logf("cleanup: delete job %s/%s: %v", ns, tray.Id, err)
		}
	})
	return tray
}

func itPods(t *testing.T, client kubernetes.Interface, ns string, tray *trays.Tray) []string {
	t.Helper()
	list, err := client.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{LabelSelector: labelTrayID + "=" + tray.Id})
	require.NoError(t, err)
	names := make([]string, 0, len(list.Items))
	for _, p := range list.Items {
		names = append(names, p.Name)
	}
	return names
}

func TestKubernetesProvider_EndToEnd(t *testing.T) {
	p, client, ns := itProvider(t, 3*time.Minute)
	tray := itTray(t, client, ns, itBlackHoleServer, map[string]any{
		"image":        itRunnerImage,
		"agentversion": "latest",
		"agentimage":   itAgentImage(),
		// pause is a scratch image: the runner folder must be a directory
		// that exists there, since it becomes the working directory.
		"runnerfolder": "/",
		"resources":    map[string]any{"requests": map[string]any{"cpu": "50m", "memory": "32Mi"}},
	})
	ctx := context.Background()

	require.NoError(t, p.StartDeploy(ctx, tray))
	job, err := client.BatchV1().Jobs(ns).Get(ctx, tray.Id, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, tray.Id, job.Labels[labelTrayID])
	assert.Equal(t, string(job.UID), tray.ProviderData[kubernetesProviderDataJobUID])
	assert.Equal(t, int32(0), *job.Spec.BackoffLimit)

	require.NoError(t, p.WaitDeploy(ctx, tray))

	pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: labelTrayID + "=" + tray.Id})
	require.NoError(t, err)
	require.Len(t, pods.Items, 1)
	pod := pods.Items[0]
	assert.True(t, strings.HasPrefix(pod.Name, tray.Id+"-"), "pod %s is named after the job", pod.Name)
	require.Len(t, pod.Status.InitContainerStatuses, 1)
	initState := pod.Status.InitContainerStatuses[0].State
	require.NotNil(t, initState.Terminated, "agent init container finished")
	assert.Equal(t, int32(0), initState.Terminated.ExitCode)

	require.NoError(t, p.CleanTray(ctx, tray))
	require.Eventually(t, func() bool {
		_, err := client.BatchV1().Jobs(ns).Get(ctx, tray.Id, metav1.GetOptions{})
		return apierrors.IsNotFound(err)
	}, time.Minute, time.Second, "job deleted")
	require.Eventually(t, func() bool {
		return len(itPods(t, client, ns, tray)) == 0
	}, 2*time.Minute, 2*time.Second, "pod deleted through background propagation")

	require.NoError(t, p.CleanTray(ctx, tray), "second cleanup is a no-op")
}

func TestKubernetesProvider_ServerModeDownloadFailureFailsFast(t *testing.T) {
	p, client, ns := itProvider(t, 3*time.Minute)
	// Nothing listens here, so wget fails and the init container exits
	// non-zero after its retries; restartPolicy Never then fails the pod.
	tray := itTray(t, client, ns, "http://127.0.0.1:1", map[string]any{
		"image":        itRunnerImage,
		"agentversion": "server",
		"agentimage":   itAgentImage(),
		"runnerfolder": "/",
	})
	ctx := context.Background()

	require.NoError(t, p.StartDeploy(ctx, tray))

	start := time.Now()
	err := p.WaitDeploy(ctx, tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), agentInitContainerName)
	assert.Less(t, time.Since(start), 3*time.Minute, "failed pod is reported before the timeout")

	require.Eventually(t, func() bool {
		job, err := client.BatchV1().Jobs(ns).Get(ctx, tray.Id, metav1.GetOptions{})
		if err != nil {
			return false
		}
		for _, c := range job.Status.Conditions {
			if c.Type == "Failed" && c.Status == "True" {
				return true
			}
		}
		return false
	}, time.Minute, 2*time.Second, "backoffLimit 0 marks the job Failed instead of re-running it")
	assert.Len(t, itPods(t, client, ns, tray), 1, "no replacement pod")

	require.NoError(t, p.CleanTray(ctx, tray))
}

func TestKubernetesProvider_ImagePullFailureFailsFast(t *testing.T) {
	p, client, ns := itProvider(t, 3*time.Minute)
	tray := itTray(t, client, ns, itBlackHoleServer, map[string]any{
		"image":        "registry.k8s.io/cattery-does-not-exist:1",
		"agentversion": "latest",
		"agentimage":   itAgentImage(),
	})
	ctx := context.Background()

	require.NoError(t, p.StartDeploy(ctx, tray))

	start := time.Now()
	err := p.WaitDeploy(ctx, tray)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "ErrImagePull") || strings.Contains(err.Error(), "ImagePullBackOff"), "%v", err)
	assert.Less(t, time.Since(start), 3*time.Minute)

	require.NoError(t, p.CleanTray(ctx, tray))
}
