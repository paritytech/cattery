package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Notes on the fake clientset these tests rely on:
//   - no Job controller runs, so pods are created by hand with the tray label;
//   - Watch ignores selectors (the provider re-checks the label);
//   - Delete ignores preconditions (conflicts are simulated with a reactor);
//   - no UIDs are assigned (a reactor stamps them on create).

const (
	testK8sNamespace = "ns-test"
	testAdvertiseURL = "http://cattery.test:5137"
)

var jobsResource = schema.GroupResource{Group: "batch", Resource: "jobs"}

// newFakeK8sProvider returns a fake clientset (pre-populated with objs) and a
// provider bound to it; Jobs created through it get a deterministic UID.
func newFakeK8sProvider(t *testing.T, timeout time.Duration, objs ...runtime.Object) (*fake.Clientset, *KubernetesProvider) {
	t.Helper()
	client := fake.NewClientset(objs...)
	client.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if job, ok := action.(k8stesting.CreateAction).GetObject().(*batchv1.Job); ok && job.UID == "" {
			job.UID = types.UID("uid-" + job.Name)
		}
		return false, nil, nil // fall through to the tracker with the stamped object
	})
	return client, newKubernetesProvider("test-k8s", client, testK8sNamespace, timeout)
}

// setupK8sTrayConfig installs a CatteryConfig with one kubernetes tray type.
func setupK8sTrayConfig(t *testing.T, trayTypeName string, kc config.TrayConfig) {
	t.Helper()
	cfg := &config.CatteryConfig{
		Server: config.ServerConfig{ListenAddress: ":0", AdvertiseUrl: testAdvertiseURL},
		TrayTypes: []*config.TrayType{{
			Name:          trayTypeName,
			Provider:      "test-k8s",
			GitHubOrg:     "org",
			RunnerGroupId: 1,
			Config:        kc,
		}},
	}
	config.SetForTest(t, cfg)
}

func typedTray(t *testing.T, extra map[string]any) (*trays.Tray, config.KubernetesTrayConfig) {
	t.Helper()
	raw := map[string]any{"image": "runner:1"}
	for k, v := range extra {
		raw[k] = v
	}
	kc := typedK8sConfig(t, raw)
	setupK8sTrayConfig(t, "k8s-small", kc)
	return newTestTray("k8s-small", "k8s-small-0123456789abcdef"), kc
}

func labelledPod(name, trayID string, status corev1.PodStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testK8sNamespace,
			Labels:    map[string]string{labelTrayID: trayID},
		},
		Status: status,
	}
}

func actionVerbs(client *fake.Clientset, resource string) []string {
	var verbs []string
	for _, a := range client.Actions() {
		if a.GetResource().Resource == resource {
			verbs = append(verbs, a.GetVerb())
		}
	}
	return verbs
}

// ---------------------------------------------------------------------------
// StartDeploy
// ---------------------------------------------------------------------------

func TestK8sStartDeploy_TypedMode_CreatesJob(t *testing.T) {
	client, p := newFakeK8sProvider(t, time.Minute)
	tray, _ := typedTray(t, map[string]any{"agentversion": "0.2.0"})

	require.NoError(t, p.StartDeploy(context.Background(), tray))

	job, err := client.BatchV1().Jobs(testK8sNamespace).Get(context.Background(), tray.Id, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, tray.Id, job.Labels[labelTrayID])
	assert.Equal(t, "k8s-small", job.Labels[labelTrayType])
	assert.Equal(t, "test-k8s", job.Labels[labelProvider])
	assert.Equal(t, int32(0), *job.Spec.BackoffLimit)
	assert.Equal(t, tray.Id, job.Spec.Template.Labels[labelTrayID])
	runner := findContainer(job.Spec.Template.Spec.Containers, "runner")
	require.NotNil(t, runner)
	assert.Equal(t, []string{"agent", "-i", tray.Id, "-s", testAdvertiseURL, "--runner-folder", config.DefaultKubernetesRunnerFolder}, runner.Args)
	assert.Equal(t, "docker.io/paritytech/cattery:0.2.0", job.Spec.Template.Spec.InitContainers[0].Image)

	assert.Equal(t, map[string]string{
		kubernetesProviderDataNamespace: testK8sNamespace,
		kubernetesProviderDataJobName:   tray.Id,
		kubernetesProviderDataJobUID:    "uid-" + tray.Id,
	}, tray.ProviderData)
}

func TestK8sStartDeploy_RefMode_UsesPodTemplate(t *testing.T) {
	template := &corev1.PodTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "runner-large", Namespace: testK8sNamespace},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"Team": "CI"}},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "dind", Image: "docker:dind"},
					{Name: "runner", Image: "runner:1"},
				},
			},
		},
	}
	client, p := newFakeK8sProvider(t, time.Minute, template)
	setupK8sTrayConfig(t, "k8s-large", config.KubernetesTrayConfig{PodTemplateRef: "runner-large"})
	tray := newTestTray("k8s-large", "k8s-large-0123456789abcdef")

	require.NoError(t, p.StartDeploy(context.Background(), tray))

	assert.Contains(t, actionVerbs(client, "podtemplates"), "get")
	job, err := client.BatchV1().Jobs(testK8sNamespace).Get(context.Background(), tray.Id, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "CI", job.Spec.Template.Labels["Team"], "template labels are preserved as-is")
	assert.Equal(t, tray.Id, job.Spec.Template.Labels[labelTrayID])
	require.Len(t, job.Spec.Template.Spec.Containers, 2)
	assert.Equal(t, corev1.Container{Name: "dind", Image: "docker:dind"}, job.Spec.Template.Spec.Containers[0], "sidecar untouched")
	assert.Equal(t, []string{agentBinaryPath}, job.Spec.Template.Spec.Containers[1].Command)
	assert.Equal(t, corev1.RestartPolicyNever, job.Spec.Template.Spec.RestartPolicy)
}

func TestK8sStartDeploy_RefMode_MissingTemplate(t *testing.T) {
	client, p := newFakeK8sProvider(t, time.Minute)
	setupK8sTrayConfig(t, "k8s-large", config.KubernetesTrayConfig{PodTemplateRef: "nope"})
	tray := newTestTray("k8s-large", "k8s-large-0123456789abcdef")

	err := p.StartDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err), "wraps the NotFound: %v", err)
	assert.NotContains(t, actionVerbs(client, "jobs"), "create")
	assert.Equal(t, testK8sNamespace, tray.ProviderData[kubernetesProviderDataNamespace], "staged before the failed call")
	assert.Equal(t, tray.Id, tray.ProviderData[kubernetesProviderDataJobName])
}

func TestK8sStartDeploy_TrayNamespaceOverridesProvider(t *testing.T) {
	client, p := newFakeK8sProvider(t, time.Minute)
	tray, _ := typedTray(t, map[string]any{"namespace": "other"})

	require.NoError(t, p.StartDeploy(context.Background(), tray))

	_, err := client.BatchV1().Jobs("other").Get(context.Background(), tray.Id, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "other", tray.ProviderData[kubernetesProviderDataNamespace])
}

func TestK8sStartDeploy_CreateErrorStillStagesProviderData(t *testing.T) {
	client, p := newFakeK8sProvider(t, time.Minute)
	client.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(jobsResource, "", errors.New("rbac"))
	})
	tray, _ := typedTray(t, nil)

	err := p.StartDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.True(t, apierrors.IsForbidden(err))
	assert.Equal(t, testK8sNamespace, tray.ProviderData[kubernetesProviderDataNamespace])
	assert.Equal(t, tray.Id, tray.ProviderData[kubernetesProviderDataJobName])
	assert.Empty(t, tray.ProviderData[kubernetesProviderDataJobUID])
}

func TestK8sStartDeploy_AlreadyExists(t *testing.T) {
	t.Run("adopts a job created for this tray", func(t *testing.T) {
		existing := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
			Name: "k8s-small-0123456789abcdef", Namespace: testK8sNamespace, UID: "existing-uid",
			Labels: map[string]string{labelTrayID: "k8s-small-0123456789abcdef"},
		}}
		_, p := newFakeK8sProvider(t, time.Minute, existing)
		tray, _ := typedTray(t, nil)

		require.NoError(t, p.StartDeploy(context.Background(), tray))
		assert.Equal(t, "existing-uid", tray.ProviderData[kubernetesProviderDataJobUID])
	})
	t.Run("refuses a job that is not ours, and cleanup leaves it alone", func(t *testing.T) {
		client, p := newFakeK8sProvider(t, time.Minute, foreignJob("k8s-small-0123456789abcdef"))
		tray, _ := typedTray(t, nil)

		err := p.StartDeploy(context.Background(), tray)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong")
		assert.Empty(t, tray.ProviderData[kubernetesProviderDataJobUID])

		// trayManager deletes the tray after a failed StartDeploy; that must
		// not take the foreign Job with it.
		require.NoError(t, p.CleanTray(context.Background(), tray))
		_, err = client.BatchV1().Jobs(testK8sNamespace).Get(context.Background(), tray.Id, metav1.GetOptions{})
		assert.NoError(t, err, "the foreign job must still exist")
	})
}

func TestK8sStartDeploy_WrongTrayConfigType(t *testing.T) {
	client, p := newFakeK8sProvider(t, time.Minute)
	setupK8sTrayConfig(t, "k8s-small", config.DockerTrayConfig{Image: "x"})
	tray := newTestTray("k8s-small", "k8s-small-0123456789abcdef")

	err := p.StartDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected tray config type")
	assert.Empty(t, client.Actions())
}

// ---------------------------------------------------------------------------
// WaitDeploy
// ---------------------------------------------------------------------------

func deployedTray(t *testing.T) *trays.Tray {
	t.Helper()
	tray := newTestTray("k8s-small", "k8s-small-0123456789abcdef")
	tray.ProviderData[kubernetesProviderDataNamespace] = testK8sNamespace
	tray.ProviderData[kubernetesProviderDataJobName] = tray.Id
	return tray
}

func TestK8sWaitDeploy_NoJobNameIsNoOp(t *testing.T) {
	client, p := newFakeK8sProvider(t, time.Minute)
	tray := newTestTray("k8s-small", "k8s-small-0123456789abcdef")

	require.NoError(t, p.WaitDeploy(context.Background(), tray))
	assert.Empty(t, client.Actions())
}

func TestK8sWaitDeploy_PodAlreadyRunning(t *testing.T) {
	tray := deployedTray(t)
	_, p := newFakeK8sProvider(t, 5*time.Second, labelledPod("p1", tray.Id, corev1.PodStatus{Phase: corev1.PodRunning}))

	require.NoError(t, p.WaitDeploy(context.Background(), tray))
}

func TestK8sWaitDeploy_PodBecomesRunning(t *testing.T) {
	tray := deployedTray(t)
	pending := labelledPod("p1", tray.Id, corev1.PodStatus{Phase: corev1.PodPending})
	client, p := newFakeK8sProvider(t, 10*time.Second, pending)

	go func() {
		time.Sleep(150 * time.Millisecond)
		running := pending.DeepCopy()
		running.Status.Phase = corev1.PodRunning
		_, _ = client.CoreV1().Pods(testK8sNamespace).Update(context.Background(), running, metav1.UpdateOptions{})
	}()

	start := time.Now()
	require.NoError(t, p.WaitDeploy(context.Background(), tray))
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestK8sWaitDeploy_PodCreatedLater(t *testing.T) {
	tray := deployedTray(t)
	client, p := newFakeK8sProvider(t, 10*time.Second)

	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = client.CoreV1().Pods(testK8sNamespace).Create(context.Background(),
			labelledPod("p1", tray.Id, corev1.PodStatus{Phase: corev1.PodRunning}), metav1.CreateOptions{})
	}()

	require.NoError(t, p.WaitDeploy(context.Background(), tray), "an empty initial list must not be an error")
}

func TestK8sWaitDeploy_IgnoresPodsOfOtherTrays(t *testing.T) {
	tray := deployedTray(t)
	other := labelledPod("other", "k8s-small-ffffffffffffffff", corev1.PodStatus{Phase: corev1.PodRunning})
	_, p := newFakeK8sProvider(t, 300*time.Millisecond, other)

	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no pod was created")
}

func TestK8sWaitDeploy_ImagePullBackOffFailsFast(t *testing.T) {
	tray := deployedTray(t)
	pod := labelledPod("p1", tray.Id, corev1.PodStatus{
		Phase: corev1.PodPending,
		InitContainerStatuses: []corev1.ContainerStatus{{
			Name:  agentInitContainerName,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "Back-off pulling image"}},
		}},
	})
	_, p := newFakeK8sProvider(t, 10*time.Second, pod)

	start := time.Now()
	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ImagePullBackOff")
	assert.Contains(t, err.Error(), agentInitContainerName)
	assert.Less(t, time.Since(start), 5*time.Second, "must not wait for the timeout")
}

func TestK8sWaitDeploy_PodFailedIncludesTermination(t *testing.T) {
	tray := deployedTray(t)
	pod := labelledPod("p1", tray.Id, corev1.PodStatus{
		Phase: corev1.PodFailed,
		InitContainerStatuses: []corev1.ContainerStatus{{
			Name:  agentInitContainerName,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error", Message: "wget: can't connect"}},
		}},
	})
	_, p := newFakeK8sProvider(t, 10*time.Second, pod)

	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Failed")
	assert.Contains(t, err.Error(), "cattery-agent: Error (exit 1): wget: can't connect")
}

func TestK8sWaitDeploy_PodSucceeded(t *testing.T) {
	tray := deployedTray(t)
	_, p := newFakeK8sProvider(t, 10*time.Second, labelledPod("p1", tray.Id, corev1.PodStatus{Phase: corev1.PodSucceeded}))

	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Succeeded")
}

func TestK8sWaitDeploy_PodDeleted(t *testing.T) {
	tray := deployedTray(t)
	pending := labelledPod("p1", tray.Id, corev1.PodStatus{Phase: corev1.PodPending})
	client, p := newFakeK8sProvider(t, 10*time.Second, pending)

	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = client.CoreV1().Pods(testK8sNamespace).Delete(context.Background(), "p1", metav1.DeleteOptions{})
	}()

	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleted")
}

func TestK8sWaitDeploy_TimeoutWithoutPod(t *testing.T) {
	tray := deployedTray(t)
	_, p := newFakeK8sProvider(t, 300*time.Millisecond)

	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out after 300ms")
	assert.Contains(t, err.Error(), "kubectl describe job")
}

func TestK8sWaitDeploy_TimeoutIncludesUnschedulableReason(t *testing.T) {
	tray := deployedTray(t)
	pod := labelledPod("p1", tray.Id, corev1.PodStatus{
		Phase: corev1.PodPending,
		Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable",
			Message: "0/3 nodes are available: 3 Insufficient cpu.",
		}},
	})
	_, p := newFakeK8sProvider(t, 300*time.Millisecond, pod)

	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.Contains(t, err.Error(), "0/3 nodes are available")
}

func TestK8sWaitDeploy_ParentContextCancelled(t *testing.T) {
	tray := deployedTray(t)
	_, p := newFakeK8sProvider(t, time.Minute, labelledPod("p1", tray.Id, corev1.PodStatus{Phase: corev1.PodPending}))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	err := p.WaitDeploy(ctx, tray)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestK8sWaitDeploy_ListErrorSurfacesImmediately(t *testing.T) {
	tray := deployedTray(t)
	client, p := newFakeK8sProvider(t, 10*time.Second)
	client.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("rbac"))
	})

	start := time.Now()
	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.True(t, apierrors.IsForbidden(err), "%v", err)
	assert.Less(t, time.Since(start), 5*time.Second)
}

// ---------------------------------------------------------------------------
// WaitDeploy: watch resilience
// ---------------------------------------------------------------------------

// fakeWatches replaces the tracker's watch with fake watchers the test drives.
// Every Watch call gets a fresh watcher, delivered on the returned channel in
// order, so a test can act once the provider is actually watching.
func fakeWatches(client *fake.Clientset) <-chan *watch.FakeWatcher {
	watchers := make(chan *watch.FakeWatcher, 4)
	client.PrependWatchReactor("pods", func(action k8stesting.Action) (bool, watch.Interface, error) {
		fw := watch.NewFakeWithChanSize(8, false)
		watchers <- fw
		return true, fw, nil
	})
	return watchers
}

func markRunning(t *testing.T, client *fake.Clientset, pod *corev1.Pod) {
	t.Helper()
	running := pod.DeepCopy()
	running.Status.Phase = corev1.PodRunning
	_, err := client.CoreV1().Pods(testK8sNamespace).Update(context.Background(), running, metav1.UpdateOptions{})
	require.NoError(t, err)
}

func TestK8sWaitDeploy_RelistsWhenWatchCloses(t *testing.T) {
	tray := deployedTray(t)
	pending := labelledPod("p1", tray.Id, corev1.PodStatus{Phase: corev1.PodPending})
	client, p := newFakeK8sProvider(t, 10*time.Second, pending)
	watchers := fakeWatches(client)

	go func() {
		fw := <-watchers // the initial list saw the pod Pending
		markRunning(t, client, pending)
		fw.Stop() // server-side timeout or dropped connection
	}()

	require.NoError(t, p.WaitDeploy(context.Background(), tray), "a closed watch must lead to a fresh list, not a failure")
}

func TestK8sWaitDeploy_RelistsOnExpiredResourceVersion(t *testing.T) {
	tray := deployedTray(t)
	pending := labelledPod("p1", tray.Id, corev1.PodStatus{Phase: corev1.PodPending})
	client, p := newFakeK8sProvider(t, 10*time.Second, pending)
	watchers := fakeWatches(client)

	go func() {
		fw := <-watchers
		markRunning(t, client, pending)
		fw.Error(&metav1.Status{Code: http.StatusGone, Reason: metav1.StatusReasonGone, Message: "too old resource version"})
	}()

	require.NoError(t, p.WaitDeploy(context.Background(), tray))
}

func TestK8sWaitDeploy_WatchErrorEventFails(t *testing.T) {
	tray := deployedTray(t)
	client, p := newFakeK8sProvider(t, 10*time.Second, labelledPod("p1", tray.Id, corev1.PodStatus{Phase: corev1.PodPending}))
	watchers := fakeWatches(client)

	go func() {
		fw := <-watchers
		fw.Error(&metav1.Status{Code: http.StatusForbidden, Reason: metav1.StatusReasonForbidden, Message: "pods is forbidden"})
	}()

	start := time.Now()
	err := p.WaitDeploy(context.Background(), tray)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pods is forbidden")
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestK8sWaitDeploy_IgnoresBookmarksAndForeignEvents(t *testing.T) {
	tray := deployedTray(t)
	pending := labelledPod("p1", tray.Id, corev1.PodStatus{Phase: corev1.PodPending})
	client, p := newFakeK8sProvider(t, 10*time.Second, pending)
	watchers := fakeWatches(client)

	go func() {
		fw := <-watchers
		fw.Action(watch.Bookmark, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", ResourceVersion: "9"}})
		fw.Modify(labelledPod("other", "k8s-small-ffffffffffffffff", corev1.PodStatus{Phase: corev1.PodRunning}))
		fw.Delete(labelledPod("other", "k8s-small-ffffffffffffffff", corev1.PodStatus{Phase: corev1.PodRunning}))
		running := pending.DeepCopy()
		running.Status.Phase = corev1.PodRunning
		fw.Modify(running)
	}()

	require.NoError(t, p.WaitDeploy(context.Background(), tray))
}

// ---------------------------------------------------------------------------
// CleanTray
// ---------------------------------------------------------------------------

// existingJob is a Job that was created for the tray of the same name.
func existingJob(name string) *batchv1.Job {
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: testK8sNamespace, UID: "uid-1",
		Labels: map[string]string{labelTrayID: name},
	}}
}

// foreignJob is a same-named Job that cattery did not create.
func foreignJob(name string) *batchv1.Job {
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testK8sNamespace, UID: "someone-elses"}}
}

func captureDelete(client *fake.Clientset) *metav1.DeleteOptions {
	captured := &metav1.DeleteOptions{}
	client.PrependReactor("delete", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		*captured = action.(k8stesting.DeleteActionImpl).DeleteOptions
		return false, nil, nil
	})
	return captured
}

func TestK8sCleanTray_DeletesJobWithBackgroundPropagationAndUIDPrecondition(t *testing.T) {
	tray := deployedTray(t)
	tray.ProviderData[kubernetesProviderDataJobUID] = "uid-1"
	client, p := newFakeK8sProvider(t, time.Minute, existingJob(tray.Id))
	opts := captureDelete(client)

	require.NoError(t, p.CleanTray(context.Background(), tray))

	require.NotNil(t, opts.PropagationPolicy)
	assert.Equal(t, metav1.DeletePropagationBackground, *opts.PropagationPolicy, "the API default would orphan the pod")
	require.NotNil(t, opts.Preconditions)
	require.NotNil(t, opts.Preconditions.UID)
	assert.Equal(t, types.UID("uid-1"), *opts.Preconditions.UID)

	_, err := client.BatchV1().Jobs(testK8sNamespace).Get(context.Background(), tray.Id, metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
}

func TestK8sCleanTray_NoUIDVerifiesOwnershipAndPinsFetchedUID(t *testing.T) {
	tray := deployedTray(t) // no UID recorded
	client, p := newFakeK8sProvider(t, time.Minute, existingJob(tray.Id))
	opts := captureDelete(client)

	require.NoError(t, p.CleanTray(context.Background(), tray))

	assert.Contains(t, actionVerbs(client, "jobs"), "get")
	require.NotNil(t, opts.Preconditions)
	require.NotNil(t, opts.Preconditions.UID)
	assert.Equal(t, types.UID("uid-1"), *opts.Preconditions.UID)
}

func TestK8sCleanTray_LeavesForeignJobAlone(t *testing.T) {
	tray := deployedTray(t) // no UID recorded
	client, p := newFakeK8sProvider(t, time.Minute, foreignJob(tray.Id))

	require.NoError(t, p.CleanTray(context.Background(), tray))

	assert.NotContains(t, actionVerbs(client, "jobs"), "delete")
	_, err := client.BatchV1().Jobs(testK8sNamespace).Get(context.Background(), tray.Id, metav1.GetOptions{})
	assert.NoError(t, err, "the job must still exist")
}

func TestK8sCleanTray_NotFoundIsSuccess(t *testing.T) {
	tray := deployedTray(t)
	_, p := newFakeK8sProvider(t, time.Minute)

	require.NoError(t, p.CleanTray(context.Background(), tray))
}

func TestK8sCleanTray_UIDConflictIsSuccess(t *testing.T) {
	tray := deployedTray(t)
	tray.ProviderData[kubernetesProviderDataJobUID] = "uid-1"
	client, p := newFakeK8sProvider(t, time.Minute, existingJob(tray.Id))
	client.PrependReactor("delete", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(jobsResource, tray.Id, errors.New("uid mismatch"))
	})

	require.NoError(t, p.CleanTray(context.Background(), tray))
}

func TestK8sCleanTray_OtherErrorPropagates(t *testing.T) {
	tray := deployedTray(t)
	client, p := newFakeK8sProvider(t, time.Minute, existingJob(tray.Id))
	client.PrependReactor("delete", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(jobsResource, tray.Id, errors.New("rbac"))
	})

	err := p.CleanTray(context.Background(), tray)
	require.Error(t, err)
	assert.True(t, apierrors.IsForbidden(err))
}

func TestK8sCleanTray_FallsBackToTrayIdAndProviderNamespace(t *testing.T) {
	tray := newTestTray("k8s-small", "k8s-small-0123456789abcdef") // empty ProviderData
	client, p := newFakeK8sProvider(t, time.Minute, existingJob(tray.Id))

	require.NoError(t, p.CleanTray(context.Background(), tray))

	_, err := client.BatchV1().Jobs(testK8sNamespace).Get(context.Background(), tray.Id, metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// writeSelfSignedCA writes a PEM certificate that client-go accepts as a CA
// bundle.
func writeSelfSignedCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	return path
}

func writeTestKubeconfig(t *testing.T) string {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["c"] = &clientcmdapi.Cluster{Server: "https://c.example:6443"}
	cfg.AuthInfos["u"] = &clientcmdapi.AuthInfo{Token: "t"}
	cfg.Contexts["ctx"] = &clientcmdapi.Context{Cluster: "c", AuthInfo: "u", Namespace: "ctx-ns"}
	cfg.CurrentContext = "ctx"
	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, clientcmd.WriteToFile(*cfg, path))
	return path
}

func TestNewKubernetesProvider(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "")
	kubeconfig := writeTestKubeconfig(t)

	t.Run("namespace from kubeconfig context", func(t *testing.T) {
		p := NewKubernetesProvider("k8s-local", config.ProviderConfig{"kubeconfig": kubeconfig})
		require.NotNil(t, p)
		assert.Equal(t, "k8s-local", p.GetProviderName())
		assert.Equal(t, "ctx-ns", p.namespace)
		assert.Equal(t, defaultKubernetesDeployTimeout, p.deployTimeout)
	})
	t.Run("explicit namespace and timeout", func(t *testing.T) {
		p := NewKubernetesProvider("k8s-local", config.ProviderConfig{"kubeconfig": kubeconfig, "namespace": "runners", "deploytimeout": "30s"})
		require.NotNil(t, p)
		assert.Equal(t, "runners", p.namespace)
		assert.Equal(t, 30*time.Second, p.deployTimeout)
	})
	t.Run("invalid deployTimeout", func(t *testing.T) {
		assert.Nil(t, NewKubernetesProvider("k8s-local", config.ProviderConfig{"kubeconfig": kubeconfig, "deploytimeout": "soon"}))
		assert.Nil(t, NewKubernetesProvider("k8s-local", config.ProviderConfig{"kubeconfig": kubeconfig, "deploytimeout": "-1m"}))
	})
	t.Run("missing kubeconfig", func(t *testing.T) {
		assert.Nil(t, NewKubernetesProvider("k8s-local", config.ProviderConfig{"kubeconfig": filepath.Join(t.TempDir(), "missing")}))
	})
	t.Run("provider name must be a label value", func(t *testing.T) {
		assert.Nil(t, NewKubernetesProvider("bad name!", config.ProviderConfig{"kubeconfig": kubeconfig}))
	})
	t.Run("explicit server with token file", func(t *testing.T) {
		// client-go reads both files while building the transport.
		tokenFile := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(tokenFile, []byte("secret"), 0o600))
		p := NewKubernetesProvider("k8s-remote", config.ProviderConfig{
			"server":    "https://target.example:6443",
			"tokenfile": tokenFile,
			"cafile":    writeSelfSignedCA(t),
		})
		require.NotNil(t, p)
		assert.Equal(t, "default", p.namespace, "a remote cluster does not inherit this pod's namespace")

		p = NewKubernetesProvider("k8s-remote", config.ProviderConfig{
			"server": "https://target.example:6443", "token": "t", "insecure": "true", "namespace": "runners",
		})
		require.NotNil(t, p)
		assert.Equal(t, "runners", p.namespace)
	})
	t.Run("server without credentials", func(t *testing.T) {
		assert.Nil(t, NewKubernetesProvider("k8s-remote", config.ProviderConfig{"server": "https://target.example:6443"}))
	})
	t.Run("server and kubeconfig are exclusive", func(t *testing.T) {
		assert.Nil(t, NewKubernetesProvider("k8s-remote", config.ProviderConfig{"server": "https://t:6443", "token": "t", "kubeconfig": kubeconfig}))
	})
	t.Run("invalid insecure", func(t *testing.T) {
		assert.Nil(t, NewKubernetesProvider("k8s-remote", config.ProviderConfig{"server": "https://t:6443", "token": "t", "insecure": "yes"}))
	})
}
