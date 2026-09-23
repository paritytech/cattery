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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// These tests run against a real cluster in the namespace of the ambient
// kubeconfig's context (CI: the kind cluster from helm/kind-action; a dev
// box: the current kubectl context). They pull two public images:
// registry.k8s.io/pause (the "runner") and a published cattery image (the
// agent carrier; override with CATTERY_TEST_AGENT_IMAGE, e.g. after
// `kind load docker-image`).
//
// Two clients are involved: an admin client from the ambient kubeconfig for
// fixtures and assertions, and the client the provider under test uses. They
// are the same unless CATTERY_TEST_KUBE_SERVER, CATTERY_TEST_KUBE_TOKEN_FILE
// and CATTERY_TEST_KUBE_CA_FILE are set, in which case the provider talks to
// the API server as that bearer token. CI mints one for the chart's
// ServiceAccount, which proves the chart's RBAC is sufficient for everything
// the provider does and exercises the remote-cluster access mode for real.
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

func randomHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

// itClients returns the admin client, the client for the provider under test
// and the namespace to work in.
func itClients(t *testing.T) (admin, provider kubernetes.Interface, ns string) {
	t.Helper()
	adminCfg, kubeNs, err := kube.NewRestConfig(kube.Options{})
	require.NoError(t, err, "load kubeconfig")
	admin, err = kubernetes.NewForConfig(adminCfg)
	require.NoError(t, err)

	ns = os.Getenv("CATTERY_TEST_NAMESPACE")
	if ns == "" {
		ns = kubeNs
	}

	provider = admin
	if server := os.Getenv("CATTERY_TEST_KUBE_SERVER"); server != "" {
		providerCfg, _, err := kube.NewRestConfig(kube.Options{
			Server:    server,
			TokenFile: os.Getenv("CATTERY_TEST_KUBE_TOKEN_FILE"),
			CAFile:    os.Getenv("CATTERY_TEST_KUBE_CA_FILE"),
		})
		require.NoError(t, err, "build restricted client")
		provider, err = kubernetes.NewForConfig(providerCfg)
		require.NoError(t, err)
		t.Logf("provider under test uses a bearer token against %s", server)
	}
	return admin, provider, ns
}

func itProvider(t *testing.T, timeout time.Duration) (*KubernetesProvider, kubernetes.Interface, string) {
	t.Helper()
	admin, providerClient, ns := itClients(t)
	return newKubernetesProvider("it-k8s", providerClient, ns, timeout), admin, ns
}

// itTray installs a tray type built from raw and returns a tray with a
// unique id. The Job is deleted on cleanup whatever the test did.
func itTray(t *testing.T, admin kubernetes.Interface, ns, advertiseURL string, raw map[string]any) *trays.Tray {
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

	tray := newTestTray("it-k8s", "it-k8s-"+randomHex(t))
	t.Cleanup(func() {
		policy := metav1.DeletePropagationBackground
		err := admin.BatchV1().Jobs(ns).Delete(context.Background(), tray.Id, metav1.DeleteOptions{PropagationPolicy: &policy})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Logf("cleanup: delete job %s/%s: %v", ns, tray.Id, err)
		}
	})
	return tray
}

func itPods(t *testing.T, admin kubernetes.Interface, ns string, tray *trays.Tray) []corev1.Pod {
	t.Helper()
	list, err := admin.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{LabelSelector: labelTrayID + "=" + tray.Id})
	require.NoError(t, err)
	return list.Items
}

func TestKubernetesProvider_EndToEnd(t *testing.T) {
	p, admin, ns := itProvider(t, 3*time.Minute)
	tray := itTray(t, admin, ns, itBlackHoleServer, map[string]any{
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
	job, err := admin.BatchV1().Jobs(ns).Get(ctx, tray.Id, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, tray.Id, job.Labels[labelTrayID])
	assert.Equal(t, string(job.UID), tray.ProviderData[kubernetesProviderDataJobUID])
	assert.Equal(t, int32(0), *job.Spec.BackoffLimit)

	require.NoError(t, p.WaitDeploy(ctx, tray))

	pods := itPods(t, admin, ns, tray)
	require.Len(t, pods, 1)
	pod := pods[0]
	assert.True(t, strings.HasPrefix(pod.Name, tray.Id+"-"), "pod %s is named after the job", pod.Name)
	require.Len(t, pod.Status.InitContainerStatuses, 1)
	initState := pod.Status.InitContainerStatuses[0].State
	require.NotNil(t, initState.Terminated, "agent init container finished")
	assert.Equal(t, int32(0), initState.Terminated.ExitCode)

	require.NoError(t, p.CleanTray(ctx, tray))
	require.Eventually(t, func() bool {
		_, err := admin.BatchV1().Jobs(ns).Get(ctx, tray.Id, metav1.GetOptions{})
		return apierrors.IsNotFound(err)
	}, time.Minute, time.Second, "job deleted")
	require.Eventually(t, func() bool {
		return len(itPods(t, admin, ns, tray)) == 0
	}, 2*time.Minute, 2*time.Second, "pod deleted through background propagation")

	require.NoError(t, p.CleanTray(ctx, tray), "second cleanup is a no-op")
}

func TestKubernetesProvider_PodTemplateRef(t *testing.T) {
	p, admin, ns := itProvider(t, 3*time.Minute)
	ctx := context.Background()

	templateName := "it-cattery-" + randomHex(t)
	_, err := admin.CoreV1().PodTemplates(ns).Create(ctx, &corev1.PodTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: templateName, Namespace: ns},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"Team": "CI"}},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "sidecar", Image: itRunnerImage},
					{Name: "runner", Image: itRunnerImage, WorkingDir: "/"},
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = admin.CoreV1().PodTemplates(ns).Delete(context.Background(), templateName, metav1.DeleteOptions{})
	})

	tray := itTray(t, admin, ns, itBlackHoleServer, map[string]any{
		"podtemplateref": templateName,
		"agentversion":   "latest",
		"agentimage":     itAgentImage(),
		"runnerfolder":   "/",
	})

	require.NoError(t, p.StartDeploy(ctx, tray))
	job, err := admin.BatchV1().Jobs(ns).Get(ctx, tray.Id, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "CI", job.Spec.Template.Labels["Team"], "template labels kept as written")
	assert.Equal(t, tray.Id, job.Spec.Template.Labels[labelTrayID])
	require.Len(t, job.Spec.Template.Spec.Containers, 2)
	assert.Equal(t, "sidecar", job.Spec.Template.Spec.Containers[0].Name)
	assert.Empty(t, job.Spec.Template.Spec.Containers[0].Command, "sidecar untouched")
	assert.Equal(t, []string{agentBinaryPath}, job.Spec.Template.Spec.Containers[1].Command)

	require.NoError(t, p.WaitDeploy(ctx, tray))

	pods := itPods(t, admin, ns, tray)
	require.Len(t, pods, 1)
	assert.Equal(t, corev1.PodRunning, pods[0].Status.Phase)
	assert.Len(t, pods[0].Spec.Containers, 2)

	require.NoError(t, p.CleanTray(ctx, tray))
}

func TestKubernetesProvider_ServerModeDownloadFailureFailsFast(t *testing.T) {
	p, admin, ns := itProvider(t, 3*time.Minute)
	// Nothing listens here, so wget fails and the init container exits
	// non-zero after its retries; restartPolicy Never then fails the pod.
	tray := itTray(t, admin, ns, "http://127.0.0.1:1", map[string]any{
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
		job, err := admin.BatchV1().Jobs(ns).Get(ctx, tray.Id, metav1.GetOptions{})
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
	assert.Len(t, itPods(t, admin, ns, tray), 1, "no replacement pod")

	require.NoError(t, p.CleanTray(ctx, tray))
}

func TestKubernetesProvider_ImagePullFailureFailsFast(t *testing.T) {
	p, admin, ns := itProvider(t, 3*time.Minute)
	tray := itTray(t, admin, ns, itBlackHoleServer, map[string]any{
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
