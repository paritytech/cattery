package providers

import (
	"cattery/lib/config"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// typedK8sConfig decodes a tray config the way LoadConfig does (mapstructure
// over the lowercased map viper produces).
func typedK8sConfig(t *testing.T, raw map[string]any) config.KubernetesTrayConfig {
	t.Helper()
	var kc config.KubernetesTrayConfig
	require.NoError(t, mapstructure.Decode(raw, &kc))
	return kc
}

func findContainer(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

func envValue(env []corev1.EnvVar, name string) (string, bool) {
	for _, e := range env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

func TestResolveAgentImage(t *testing.T) {
	tests := []struct {
		name       string
		kc         config.KubernetesTrayConfig
		wantImage  string
		wantServer bool
	}{
		{name: "empty defaults to server mode on latest", kc: config.KubernetesTrayConfig{}, wantImage: "docker.io/paritytech/cattery:latest", wantServer: true},
		{name: "server", kc: config.KubernetesTrayConfig{AgentVersion: "server"}, wantImage: "docker.io/paritytech/cattery:latest", wantServer: true},
		{name: "tag copies from published image", kc: config.KubernetesTrayConfig{AgentVersion: "0.2.0"}, wantImage: "docker.io/paritytech/cattery:0.2.0"},
		{name: "agentImage overrides in server mode", kc: config.KubernetesTrayConfig{AgentVersion: "server", AgentImage: "mirror/cattery:x"}, wantImage: "mirror/cattery:x", wantServer: true},
		{name: "agentImage overrides in tag mode", kc: config.KubernetesTrayConfig{AgentVersion: "0.2.0", AgentImage: "mirror/cattery:x"}, wantImage: "mirror/cattery:x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			image, serverMode := resolveAgentImage(tc.kc)
			assert.Equal(t, tc.wantImage, image)
			assert.Equal(t, tc.wantServer, serverMode)
		})
	}
}

func TestAgentInitContainer_ServerMode(t *testing.T) {
	c := agentInitContainer(agentSpec{ServerURL: "http://cattery:5137", Image: "img", ServerMode: true})

	assert.Equal(t, agentInitContainerName, c.Name)
	assert.Equal(t, "img", c.Image)
	require.Len(t, c.Command, 3)
	assert.Equal(t, []string{"sh", "-c"}, c.Command[:2])
	assert.Contains(t, c.Command[2], `wget -q -O /cattery-agent/cattery "$CATTERY_URL/agent/download"`)
	assert.Contains(t, c.Command[2], "chmod 0755 /cattery-agent/cattery")
	v, ok := envValue(c.Env, envCatteryURL)
	assert.True(t, ok)
	assert.Equal(t, "http://cattery:5137", v)
	require.Len(t, c.VolumeMounts, 1)
	assert.Equal(t, corev1.VolumeMount{Name: agentVolumeName, MountPath: agentMountPath}, c.VolumeMounts[0])
}

func TestAgentInitContainer_CopyMode(t *testing.T) {
	c := agentInitContainer(agentSpec{ServerURL: "http://cattery:5137", Image: "img", ServerMode: false})

	assert.Equal(t, []string{"cp", "/usr/local/bin/cattery", "/cattery-agent/cattery"}, c.Command)
	assert.Empty(t, c.Env)
}

func TestPodTemplateFromTypedConfig(t *testing.T) {
	grace := int64(45)
	tolSeconds := int64(30)
	kc := config.KubernetesTrayConfig{
		Image:              "runner:1",
		ImagePullPolicy:    "Always",
		ImagePullSecrets:   []string{"regcred"},
		ServiceAccountName: "runner-sa",
		Resources: config.KubernetesResources{
			Requests: map[string]string{"cpu": "2", "memory": "4Gi"},
			Limits:   map[string]string{"nvidia.com/gpu": "1"},
		},
		NodeSelector:                  map[string]string{"zone": "a"},
		Tolerations:                   []config.KubernetesToleration{{Key: "ci", Operator: "Equal", Value: "true", Effect: "NoSchedule", TolerationSeconds: &tolSeconds}},
		Labels:                        map[string]string{"team": "ci"},
		Annotations:                   map[string]string{"note": "x"},
		Env:                           []config.KubernetesEnvVar{{Name: "FOO", Value: "bar"}},
		RuntimeClassName:              "gvisor",
		PriorityClassName:             "ci",
		TerminationGracePeriodSeconds: &grace,
	}

	tpl, err := podTemplateFromTypedConfig(kc)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"team": "ci"}, tpl.Labels)
	assert.Equal(t, map[string]string{"note": "x"}, tpl.Annotations)
	require.Len(t, tpl.Spec.Containers, 1)
	c := tpl.Spec.Containers[0]
	assert.Equal(t, config.DefaultKubernetesRunnerContainer, c.Name)
	assert.Equal(t, "runner:1", c.Image)
	assert.Equal(t, corev1.PullAlways, c.ImagePullPolicy)
	assert.True(t, c.Resources.Requests.Cpu().Equal(resource.MustParse("2")))
	assert.True(t, c.Resources.Requests.Memory().Equal(resource.MustParse("4Gi")))
	gpu := c.Resources.Limits[corev1.ResourceName("nvidia.com/gpu")]
	assert.True(t, gpu.Equal(resource.MustParse("1")))
	assert.Equal(t, []corev1.EnvVar{{Name: "FOO", Value: "bar"}}, c.Env)
	assert.Equal(t, []corev1.LocalObjectReference{{Name: "regcred"}}, tpl.Spec.ImagePullSecrets)
	assert.Equal(t, "runner-sa", tpl.Spec.ServiceAccountName)
	assert.Equal(t, map[string]string{"zone": "a"}, tpl.Spec.NodeSelector)
	assert.Equal(t, []corev1.Toleration{{
		Key: "ci", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule, TolerationSeconds: &tolSeconds,
	}}, tpl.Spec.Tolerations)
	require.NotNil(t, tpl.Spec.RuntimeClassName)
	assert.Equal(t, "gvisor", *tpl.Spec.RuntimeClassName)
	assert.Equal(t, "ci", tpl.Spec.PriorityClassName)
	assert.Equal(t, &grace, tpl.Spec.TerminationGracePeriodSeconds)
	require.NotNil(t, tpl.Spec.EnableServiceLinks)
	assert.False(t, *tpl.Spec.EnableServiceLinks)
}

func TestPodTemplateFromTypedConfig_SecureDefaults(t *testing.T) {
	tpl, err := podTemplateFromTypedConfig(config.KubernetesTrayConfig{Image: "runner:1"})
	require.NoError(t, err)

	require.NotNil(t, tpl.Spec.AutomountServiceAccountToken)
	assert.False(t, *tpl.Spec.AutomountServiceAccountToken, "no API token for third-party code unless asked for")
	assert.Nil(t, tpl.Spec.RuntimeClassName, "empty runtimeClassName must stay unset")
	assert.Empty(t, tpl.Spec.ImagePullSecrets)
	assert.Empty(t, tpl.Spec.Containers[0].Resources.Requests)
	assert.Empty(t, tpl.Spec.Containers[0].ImagePullPolicy)

	on := true
	tpl, err = podTemplateFromTypedConfig(config.KubernetesTrayConfig{Image: "runner:1", AutomountServiceAccountToken: &on})
	require.NoError(t, err)
	assert.True(t, *tpl.Spec.AutomountServiceAccountToken)
}

func TestPodTemplateFromTypedConfig_BadQuantity(t *testing.T) {
	_, err := podTemplateFromTypedConfig(config.KubernetesTrayConfig{
		Image:     "runner:1",
		Resources: config.KubernetesResources{Limits: map[string]string{"memory": "four gigs"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resources.limits")
	assert.Contains(t, err.Error(), "memory")
}

func TestResolveRunnerContainer(t *testing.T) {
	t.Run("no containers", func(t *testing.T) {
		_, err := resolveRunnerContainer(&corev1.PodSpec{}, "runner")
		assert.Error(t, err)
	})
	t.Run("single container ignores name", func(t *testing.T) {
		idx, err := resolveRunnerContainer(&corev1.PodSpec{Containers: []corev1.Container{{Name: "anything"}}}, "runner")
		require.NoError(t, err)
		assert.Equal(t, 0, idx)
	})
	t.Run("match by name", func(t *testing.T) {
		spec := &corev1.PodSpec{Containers: []corev1.Container{{Name: "dind"}, {Name: "runner"}}}
		idx, err := resolveRunnerContainer(spec, "runner")
		require.NoError(t, err)
		assert.Equal(t, 1, idx)
	})
	t.Run("missing name", func(t *testing.T) {
		spec := &corev1.PodSpec{Containers: []corev1.Container{{Name: "dind"}, {Name: "worker"}}}
		_, err := resolveRunnerContainer(spec, "runner")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runnerContainer")
	})
}

func baseAgentSpec() agentSpec {
	return agentSpec{
		TrayID:       "k8s-small-0123456789abcdef",
		ServerURL:    "http://cattery:5137",
		RunnerFolder: "/home/runner",
		Image:        "docker.io/paritytech/cattery:latest",
		ServerMode:   true,
	}
}

func TestInjectAgent_RewritesRunnerContainer(t *testing.T) {
	tpl := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:         "runner",
			Image:        "runner:1",
			Command:      []string{"/home/runner/run.sh"},
			Env:          []corev1.EnvVar{{Name: "FOO", Value: "bar"}, {Name: envCatteryURL, Value: "stale"}},
			VolumeMounts: []corev1.VolumeMount{{Name: "work", MountPath: "/work"}},
		}},
		Volumes: []corev1.Volume{{Name: "work"}},
	}}

	require.NoError(t, injectAgent(tpl, 0, baseAgentSpec()))

	runner := tpl.Spec.Containers[0]
	assert.Equal(t, []string{agentBinaryPath}, runner.Command)
	assert.Equal(t, []string{"agent", "-i", "k8s-small-0123456789abcdef", "-s", "http://cattery:5137", "--runner-folder", "/home/runner"}, runner.Args)
	assert.Equal(t, "/home/runner", runner.WorkingDir)
	// User env is kept, provider-owned vars are appended last and replace
	// same-named entries.
	assert.Equal(t, []corev1.EnvVar{
		{Name: "FOO", Value: "bar"},
		{Name: envCatteryURL, Value: "http://cattery:5137"},
		{Name: envCatteryAgentID, Value: "k8s-small-0123456789abcdef"},
	}, runner.Env)
	assert.Equal(t, []corev1.VolumeMount{
		{Name: "work", MountPath: "/work"},
		{Name: agentVolumeName, MountPath: agentMountPath, ReadOnly: true},
	}, runner.VolumeMounts)

	require.Len(t, tpl.Spec.Volumes, 2)
	assert.Equal(t, agentVolumeName, tpl.Spec.Volumes[1].Name)
	assert.NotNil(t, tpl.Spec.Volumes[1].EmptyDir)
	require.Len(t, tpl.Spec.InitContainers, 1)
	assert.Equal(t, agentInitContainerName, tpl.Spec.InitContainers[0].Name)
}

func TestInjectAgent_PreservesWorkingDirAndSidecars(t *testing.T) {
	tpl := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{{Name: "setup"}},
		Containers: []corev1.Container{
			{Name: "dind", Image: "docker:dind", Args: []string{"--tls=false"}},
			{Name: "runner", Image: "runner:1", WorkingDir: "/custom"},
		},
	}}

	require.NoError(t, injectAgent(tpl, 1, baseAgentSpec()))

	assert.Equal(t, "/custom", tpl.Spec.Containers[1].WorkingDir)
	assert.Equal(t, corev1.Container{Name: "dind", Image: "docker:dind", Args: []string{"--tls=false"}}, tpl.Spec.Containers[0], "sidecar untouched")
	require.Len(t, tpl.Spec.InitContainers, 2)
	assert.Equal(t, agentInitContainerName, tpl.Spec.InitContainers[0].Name, "agent init container runs first")
	assert.Equal(t, "setup", tpl.Spec.InitContainers[1].Name)
}

func TestInjectAgent_Errors(t *testing.T) {
	t.Run("index out of range", func(t *testing.T) {
		tpl := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner"}}}}
		assert.Error(t, injectAgent(tpl, 1, baseAgentSpec()))
	})
	t.Run("volume name collision", func(t *testing.T) {
		tpl := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "runner"}},
			Volumes:    []corev1.Volume{{Name: agentVolumeName}},
		}}
		err := injectAgent(tpl, 0, baseAgentSpec())
		require.Error(t, err)
		assert.Contains(t, err.Error(), agentVolumeName)
	})
	t.Run("init container name collision", func(t *testing.T) {
		tpl := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers:     []corev1.Container{{Name: "runner"}},
			InitContainers: []corev1.Container{{Name: agentInitContainerName}},
		}}
		err := injectAgent(tpl, 0, baseAgentSpec())
		require.Error(t, err)
		assert.Contains(t, err.Error(), agentInitContainerName)
	})
}

func baseJobParams(t *testing.T, kc config.KubernetesTrayConfig, tpl *corev1.PodTemplateSpec, runnerIdx int) jobBuildParams {
	t.Helper()
	return jobBuildParams{
		TrayID:       "k8s-small-0123456789abcdef",
		TrayType:     "k8s-small",
		ProviderName: "k8s-local",
		Namespace:    "runners",
		ServerURL:    "http://cattery:5137",
		Config:       kc,
		Template:     tpl,
		RunnerIdx:    runnerIdx,
	}
}

func typedTemplate(t *testing.T, kc config.KubernetesTrayConfig) *corev1.PodTemplateSpec {
	t.Helper()
	tpl, err := podTemplateFromTypedConfig(kc)
	require.NoError(t, err)
	return &tpl
}

func TestBuildTrayJob_TypedMode(t *testing.T) {
	kc := typedK8sConfig(t, map[string]any{
		"image":  "runner:1",
		"labels": map[string]any{"team": "ci", labelTrayID: "spoofed"},
	})

	job, err := buildTrayJob(baseJobParams(t, kc, typedTemplate(t, kc), 0))
	require.NoError(t, err)

	assert.Equal(t, "k8s-small-0123456789abcdef", job.Name)
	assert.Equal(t, "runners", job.Namespace)
	wantLabels := map[string]string{
		labelManagedBy: "cattery",
		labelComponent: "runner",
		labelTrayID:    "k8s-small-0123456789abcdef",
		labelTrayType:  "k8s-small",
		labelProvider:  "k8s-local",
	}
	assert.Equal(t, wantLabels, job.Labels)
	assert.Equal(t, "ci", job.Spec.Template.Labels["team"], "user labels kept on the pod")
	assert.Equal(t, "k8s-small-0123456789abcdef", job.Spec.Template.Labels[labelTrayID], "provider-owned label wins")

	require.NotNil(t, job.Spec.BackoffLimit)
	assert.Equal(t, int32(0), *job.Spec.BackoffLimit)
	require.NotNil(t, job.Spec.PodReplacementPolicy)
	assert.Equal(t, batchv1.Failed, *job.Spec.PodReplacementPolicy)
	require.NotNil(t, job.Spec.TTLSecondsAfterFinished)
	assert.Equal(t, config.DefaultKubernetesTTLSecondsAfterFinished, *job.Spec.TTLSecondsAfterFinished)
	assert.Nil(t, job.Spec.ActiveDeadlineSeconds)
	assert.Equal(t, corev1.RestartPolicyNever, job.Spec.Template.Spec.RestartPolicy)

	runner := findContainer(job.Spec.Template.Spec.Containers, "runner")
	require.NotNil(t, runner)
	assert.Equal(t, []string{agentBinaryPath}, runner.Command)
	assert.Equal(t, config.DefaultKubernetesRunnerFolder, runner.Args[len(runner.Args)-1], "empty runnerFolder falls back to the default")
	init := findContainer(job.Spec.Template.Spec.InitContainers, agentInitContainerName)
	require.NotNil(t, init)
	assert.Equal(t, "docker.io/paritytech/cattery:latest", init.Image, "empty agentVersion means server mode")
	assert.Equal(t, "sh", init.Command[0], "server mode downloads")
}

func TestBuildTrayJob_JobSpecKnobs(t *testing.T) {
	kc := typedK8sConfig(t, map[string]any{
		"image":                   "runner:1",
		"ttlsecondsafterfinished": 0,
		"activedeadlineseconds":   3600,
		"agentversion":            "0.2.0",
		"runnerfolder":            "/runner",
	})

	job, err := buildTrayJob(baseJobParams(t, kc, typedTemplate(t, kc), 0))
	require.NoError(t, err)

	assert.Equal(t, int32(0), *job.Spec.TTLSecondsAfterFinished, "explicit 0 is kept (delete immediately)")
	require.NotNil(t, job.Spec.ActiveDeadlineSeconds)
	assert.Equal(t, int64(3600), *job.Spec.ActiveDeadlineSeconds)
	init := findContainer(job.Spec.Template.Spec.InitContainers, agentInitContainerName)
	require.NotNil(t, init)
	assert.Equal(t, "docker.io/paritytech/cattery:0.2.0", init.Image)
	assert.Equal(t, "cp", init.Command[0], "tag mode copies")
	runner := findContainer(job.Spec.Template.Spec.Containers, "runner")
	assert.Equal(t, "/runner", runner.Args[len(runner.Args)-1])

	kc = typedK8sConfig(t, map[string]any{"image": "runner:1", "activedeadlineseconds": 0})
	job, err = buildTrayJob(baseJobParams(t, kc, typedTemplate(t, kc), 0))
	require.NoError(t, err)
	assert.Nil(t, job.Spec.ActiveDeadlineSeconds, "0 means no deadline")
}

func TestBuildTrayJob_TemplateMode(t *testing.T) {
	kc := config.KubernetesTrayConfig{PodTemplateRef: "runner-large"}
	tpl := &corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "ignored",
			Namespace:   "ignored",
			Labels:      map[string]string{"Team": "CI"},
			Annotations: map[string]string{"note": "x"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways,
			NodeSelector:  map[string]string{"zone": "a"},
			Tolerations:   []corev1.Toleration{{Key: "ci", Operator: corev1.TolerationOpExists}},
			Affinity:      &corev1.Affinity{},
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser: ptrTo(int64(1001)),
			},
			Volumes: []corev1.Volume{{Name: "cache"}},
			Containers: []corev1.Container{
				{Name: "dind", Image: "docker:dind"},
				{Name: "runner", Image: "runner:1"},
			},
		},
	}
	before := tpl.DeepCopy()

	job, err := buildTrayJob(baseJobParams(t, kc, tpl, 1))
	require.NoError(t, err)

	pod := job.Spec.Template
	assert.Empty(t, pod.Name)
	assert.Empty(t, pod.Namespace)
	assert.Equal(t, "CI", pod.Labels["Team"])
	assert.Equal(t, "x", pod.Annotations["note"])
	assert.Equal(t, corev1.RestartPolicyNever, pod.Spec.RestartPolicy, "restartPolicy forced")
	assert.Equal(t, map[string]string{"zone": "a"}, pod.Spec.NodeSelector)
	assert.Equal(t, before.Spec.Tolerations, pod.Spec.Tolerations)
	assert.NotNil(t, pod.Spec.Affinity)
	assert.Equal(t, int64(1001), *pod.Spec.SecurityContext.RunAsUser)
	assert.Equal(t, "cache", pod.Spec.Volumes[0].Name)
	assert.Nil(t, pod.Spec.AutomountServiceAccountToken, "template mode does not add typed-mode defaults")
	assert.Equal(t, "docker:dind", findContainer(pod.Spec.Containers, "dind").Image)
	assert.Equal(t, []string{agentBinaryPath}, findContainer(pod.Spec.Containers, "runner").Command)
	assert.Equal(t, config.DefaultKubernetesRunnerFolder, findContainer(pod.Spec.Containers, "runner").WorkingDir)

	assert.Equal(t, before, tpl, "input template must not be mutated")
}

func TestBuildTrayJob_Errors(t *testing.T) {
	kc := config.KubernetesTrayConfig{Image: "runner:1"}
	_, err := buildTrayJob(baseJobParams(t, kc, nil, 0))
	assert.Error(t, err)

	_, err = buildTrayJob(baseJobParams(t, kc, typedTemplate(t, kc), 3))
	assert.Error(t, err)
}

func podWithStatus(status corev1.PodStatus) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}, Status: status}
}

func TestEvaluatePod(t *testing.T) {
	waiting := func(reason string) corev1.ContainerState {
		return corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: "details"}}
	}

	type testCase struct {
		name     string
		pod      *corev1.Pod
		wantDone bool
		wantErr  string
	}
	tests := []testCase{
		{name: "running", pod: podWithStatus(corev1.PodStatus{Phase: corev1.PodRunning}), wantDone: true},
		{name: "pending keeps waiting", pod: podWithStatus(corev1.PodStatus{Phase: corev1.PodPending})},
		{name: "container creating keeps waiting", pod: podWithStatus(corev1.PodStatus{
			Phase:             corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "runner", State: waiting("ContainerCreating")}},
		})},
		{name: "succeeded", pod: podWithStatus(corev1.PodStatus{Phase: corev1.PodSucceeded}), wantErr: "Succeeded"},
		{name: "failed with init termination", pod: podWithStatus(corev1.PodStatus{
			Phase: corev1.PodFailed,
			InitContainerStatuses: []corev1.ContainerStatus{{
				Name:  agentInitContainerName,
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error", Message: "download failed"}},
			}},
		}), wantErr: "cattery-agent: Error (exit 1): download failed"},
		{name: "failed with pod reason", pod: podWithStatus(corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted", Message: "node pressure"}), wantErr: "Evicted: node pressure"},
		{name: "being deleted", pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p", DeletionTimestamp: &metav1.Time{}},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		}, wantErr: "being deleted"},
	}
	for _, reason := range []string{"ErrImagePull", "ImagePullBackOff", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError"} {
		tests = append(tests,
			testCase{
				name: "init container " + reason,
				pod: podWithStatus(corev1.PodStatus{
					Phase:                 corev1.PodPending,
					InitContainerStatuses: []corev1.ContainerStatus{{Name: agentInitContainerName, State: waiting(reason)}},
				}),
				wantErr: reason,
			},
			testCase{
				name: "app container " + reason,
				pod: podWithStatus(corev1.PodStatus{
					Phase:             corev1.PodPending,
					ContainerStatuses: []corev1.ContainerStatus{{Name: "runner", State: waiting(reason)}},
				}),
				wantErr: reason,
			},
		)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			done, err := evaluatePod(tc.pod)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.False(t, done)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantDone, done)
		})
	}
}

func TestDescribeWaitFailure(t *testing.T) {
	assert.Equal(t,
		"no pod was created for job ns/job (kubectl describe job -n ns job)",
		describeWaitFailure(nil, "ns", "job"))

	unschedulable := podWithStatus(corev1.PodStatus{
		Phase: corev1.PodPending,
		Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "0/3 nodes are available",
		}},
	})
	assert.Equal(t, "pod p not scheduled: Unschedulable: 0/3 nodes are available", describeWaitFailure(unschedulable, "ns", "job"))

	pulling := podWithStatus(corev1.PodStatus{
		Phase:                 corev1.PodPending,
		InitContainerStatuses: []corev1.ContainerStatus{{Name: "cattery-agent", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}}},
	})
	assert.Equal(t, "container cattery-agent of pod p waiting: ContainerCreating: ", describeWaitFailure(pulling, "ns", "job"))

	assert.Equal(t, "pod p in phase Pending", describeWaitFailure(podWithStatus(corev1.PodStatus{Phase: corev1.PodPending}), "ns", "job"))
}
