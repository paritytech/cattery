package providers

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"cattery/lib/config"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Pure builders for the kubernetes provider: they turn a tray + its config
// into a batch/v1 Job and interpret pod status. No API calls happen here so
// everything is unit-testable without a cluster.

const (
	// The agent binary is staged by an init container into an emptyDir shared
	// with the runner container.
	agentVolumeName        = "cattery-agent"
	agentMountPath         = "/cattery-agent"
	agentBinaryPath        = agentMountPath + "/cattery"
	agentInitContainerName = "cattery-agent"
	// agentSourceBinaryPath is where the published cattery image keeps the
	// binary (see Dockerfile); tag mode copies it from there.
	agentSourceBinaryPath = "/usr/local/bin/cattery"
	agentImageRepository  = "docker.io/paritytech/cattery"
	agentImageLatestTag   = "latest"

	envCatteryURL     = "CATTERY_URL"
	envCatteryAgentID = "CATTERY_AGENT_ID"

	labelManagedBy      = "app.kubernetes.io/managed-by"
	labelComponent      = "app.kubernetes.io/component"
	labelTrayID         = "cattery.io/tray-id"
	labelTrayType       = "cattery.io/tray-type"
	labelProvider       = "cattery.io/provider"
	labelManagedByValue = "cattery"
	labelComponentValue = "runner"
)

// agentDownloadScript runs in the init container in server mode. The cattery
// image is alpine-based, so busybox wget and the CA bundle are available. A
// few retries cover a server that is briefly unreachable while the pod starts.
const agentDownloadScript = `set -e
for i in 1 2 3 4 5; do
  if wget -q -O /cattery-agent/cattery "$CATTERY_URL/agent/download"; then
    break
  fi
  if [ "$i" = 5 ]; then
    echo "failed to download the cattery agent from $CATTERY_URL" >&2
    exit 1
  fi
  sleep 3
done
chmod 0755 /cattery-agent/cattery
`

// fatalWaitingReasons are container waiting states that never resolve on
// their own; WaitDeploy fails fast on them instead of waiting for the timeout.
var fatalWaitingReasons = map[string]bool{
	"ErrImagePull":               true,
	"ImagePullBackOff":           true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
}

// agentSpec is everything the injection needs to know about one tray.
type agentSpec struct {
	TrayID       string
	ServerURL    string
	RunnerFolder string
	// Image is the init container image; ServerMode selects download vs copy.
	Image      string
	ServerMode bool
}

// jobBuildParams feeds buildTrayJob.
type jobBuildParams struct {
	TrayID       string
	TrayType     string
	ProviderName string
	Namespace    string
	ServerURL    string
	Config       config.KubernetesTrayConfig
	// Template is the pod shape (typed or from a PodTemplate object). It is
	// deep-copied before use; RunnerIdx indexes Template.Spec.Containers.
	Template  *corev1.PodTemplateSpec
	RunnerIdx int
}

func ptrTo[T any](v T) *T {
	return &v
}

// trayLabels are the provider-owned labels set on the Job and its pod.
func trayLabels(trayID, trayType, provider string) map[string]string {
	return map[string]string{
		labelManagedBy: labelManagedByValue,
		labelComponent: labelComponentValue,
		labelTrayID:    trayID,
		labelTrayType:  trayType,
		labelProvider:  provider,
	}
}

// resolveAgentImage picks the init container image and mode from the tray
// config: agentVersion "server" (or empty) downloads from the cattery server
// using the latest published image as the carrier; any other value is a tag
// of the published image whose binary is copied. agentImage overrides the
// image in both modes.
func resolveAgentImage(kc config.KubernetesTrayConfig) (image string, serverMode bool) {
	serverMode = kc.AgentVersion == "" || kc.AgentVersion == config.KubernetesAgentVersionServer
	image = kc.AgentImage
	if image == "" {
		tag := kc.AgentVersion
		if serverMode {
			tag = agentImageLatestTag
		}
		image = agentImageRepository + ":" + tag
	}
	return image, serverMode
}

// agentInitContainer stages the agent binary into the shared volume.
func agentInitContainer(a agentSpec) corev1.Container {
	c := corev1.Container{
		Name:  agentInitContainerName,
		Image: a.Image,
		VolumeMounts: []corev1.VolumeMount{
			{Name: agentVolumeName, MountPath: agentMountPath},
		},
	}
	if a.ServerMode {
		c.Command = []string{"sh", "-c", agentDownloadScript}
		c.Env = []corev1.EnvVar{{Name: envCatteryURL, Value: a.ServerURL}}
	} else {
		c.Command = []string{"cp", agentSourceBinaryPath, agentBinaryPath}
	}
	return c
}

// podTemplateFromTypedConfig maps the typed config fields onto a
// single-container pod. Quantities are parsed here; config.Validate has
// already checked them at load time, so an error means the config was built
// by hand.
func podTemplateFromTypedConfig(kc config.KubernetesTrayConfig) (corev1.PodTemplateSpec, error) {
	requests, err := toResourceList(kc.Resources.Requests)
	if err != nil {
		return corev1.PodTemplateSpec{}, fmt.Errorf("resources.requests: %w", err)
	}
	limits, err := toResourceList(kc.Resources.Limits)
	if err != nil {
		return corev1.PodTemplateSpec{}, fmt.Errorf("resources.limits: %w", err)
	}

	var env []corev1.EnvVar
	for _, e := range kc.Env {
		env = append(env, corev1.EnvVar{Name: e.Name, Value: e.Value})
	}
	var tolerations []corev1.Toleration
	for _, t := range kc.Tolerations {
		tolerations = append(tolerations, corev1.Toleration{
			Key:               t.Key,
			Operator:          corev1.TolerationOperator(t.Operator),
			Value:             t.Value,
			Effect:            corev1.TaintEffect(t.Effect),
			TolerationSeconds: t.TolerationSeconds,
		})
	}
	var pullSecrets []corev1.LocalObjectReference
	for _, name := range kc.ImagePullSecrets {
		pullSecrets = append(pullSecrets, corev1.LocalObjectReference{Name: name})
	}
	automount := false
	if kc.AutomountServiceAccountToken != nil {
		automount = *kc.AutomountServiceAccountToken
	}

	spec := corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:            config.DefaultKubernetesRunnerContainer,
			Image:           kc.Image,
			ImagePullPolicy: corev1.PullPolicy(kc.ImagePullPolicy),
			Resources:       corev1.ResourceRequirements{Requests: requests, Limits: limits},
			Env:             env,
		}},
		ImagePullSecrets:   pullSecrets,
		ServiceAccountName: kc.ServiceAccountName,
		// A CI runner executes third-party code: no API token unless asked
		// for, and no *_SERVICE_HOST env noise from every Service around.
		AutomountServiceAccountToken:  ptrTo(automount),
		EnableServiceLinks:            ptrTo(false),
		NodeSelector:                  maps.Clone(kc.NodeSelector),
		Tolerations:                   tolerations,
		PriorityClassName:             kc.PriorityClassName,
		TerminationGracePeriodSeconds: kc.TerminationGracePeriodSeconds,
	}
	if kc.RuntimeClassName != "" {
		spec.RuntimeClassName = ptrTo(kc.RuntimeClassName)
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      maps.Clone(kc.Labels),
			Annotations: maps.Clone(kc.Annotations),
		},
		Spec: spec,
	}, nil
}

// toResourceList parses a requests/limits map ("cpu": "2", "memory": "4Gi").
func toResourceList(m map[string]string) (corev1.ResourceList, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(corev1.ResourceList, len(m))
	for name, raw := range m {
		q, err := resource.ParseQuantity(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %q: %w", name, raw, err)
		}
		out[corev1.ResourceName(name)] = q
	}
	return out, nil
}

// resolveRunnerContainer returns the index of the container that runs the
// agent: the only container when there is exactly one (whatever its name),
// otherwise the one called name.
func resolveRunnerContainer(spec *corev1.PodSpec, name string) (int, error) {
	switch len(spec.Containers) {
	case 0:
		return 0, errors.New("pod template has no containers")
	case 1:
		return 0, nil
	}
	for i, c := range spec.Containers {
		if c.Name == name {
			return i, nil
		}
	}
	return 0, fmt.Errorf("pod template has %d containers and none is named %q (set runnerContainer)", len(spec.Containers), name)
}

// injectAgent mutates tpl so that container runnerIdx runs the cattery agent:
// it adds the shared volume, prepends the init container that stages the
// binary, and rewrites the runner container's command/args/env/mounts.
// Everything else in the template is left untouched.
func injectAgent(tpl *corev1.PodTemplateSpec, runnerIdx int, a agentSpec) error {
	if runnerIdx < 0 || runnerIdx >= len(tpl.Spec.Containers) {
		return fmt.Errorf("runner container index %d out of range (%d containers)", runnerIdx, len(tpl.Spec.Containers))
	}
	for _, v := range tpl.Spec.Volumes {
		if v.Name == agentVolumeName {
			return fmt.Errorf("pod template already defines a volume named %q", agentVolumeName)
		}
	}
	for _, c := range tpl.Spec.InitContainers {
		if c.Name == agentInitContainerName {
			return fmt.Errorf("pod template already defines an init container named %q", agentInitContainerName)
		}
	}

	tpl.Spec.Volumes = append(tpl.Spec.Volumes, corev1.Volume{
		Name:         agentVolumeName,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	})
	tpl.Spec.InitContainers = append([]corev1.Container{agentInitContainer(a)}, tpl.Spec.InitContainers...)

	runner := &tpl.Spec.Containers[runnerIdx]
	runner.Command = []string{agentBinaryPath}
	runner.Args = []string{"agent", "-i", a.TrayID, "-s", a.ServerURL, "--runner-folder", a.RunnerFolder}
	if runner.WorkingDir == "" {
		// The agent creates ./shutdown_file in its working directory; make
		// that the runner folder, which the runner image owns.
		runner.WorkingDir = a.RunnerFolder
	}
	runner.Env = withEnv(runner.Env,
		corev1.EnvVar{Name: envCatteryURL, Value: a.ServerURL},
		corev1.EnvVar{Name: envCatteryAgentID, Value: a.TrayID},
	)
	runner.VolumeMounts = append(runner.VolumeMounts, corev1.VolumeMount{
		Name:      agentVolumeName,
		MountPath: agentMountPath,
		ReadOnly:  true,
	})
	return nil
}

// withEnv appends vars to env, dropping any existing entries with the same
// names so the provider-owned values win.
func withEnv(env []corev1.EnvVar, vars ...corev1.EnvVar) []corev1.EnvVar {
	owned := make(map[string]bool, len(vars))
	for _, v := range vars {
		owned[v.Name] = true
	}
	out := make([]corev1.EnvVar, 0, len(env)+len(vars))
	for _, e := range env {
		if !owned[e.Name] {
			out = append(out, e)
		}
	}
	return append(out, vars...)
}

// buildTrayJob assembles the Job for one tray.
//
// The Job never re-runs the agent: backoffLimit 0 and podReplacementPolicy
// Failed make sure a pod that fails or is evicted is not replaced by a second
// agent registering under the same tray id (re-runs are the restarter's job,
// via a new tray). ttlSecondsAfterFinished lets Kubernetes garbage-collect
// finished Jobs cattery failed to delete.
func buildTrayJob(p jobBuildParams) (*batchv1.Job, error) {
	if p.Template == nil {
		return nil, errors.New("pod template is required")
	}
	tpl := p.Template.DeepCopy()

	runnerFolder := p.Config.RunnerFolder
	if runnerFolder == "" {
		runnerFolder = config.DefaultKubernetesRunnerFolder
	}
	image, serverMode := resolveAgentImage(p.Config)
	err := injectAgent(tpl, p.RunnerIdx, agentSpec{
		TrayID:       p.TrayID,
		ServerURL:    p.ServerURL,
		RunnerFolder: runnerFolder,
		Image:        image,
		ServerMode:   serverMode,
	})
	if err != nil {
		return nil, err
	}

	// Provider-owned labels are written last so a template cannot clobber
	// the tray-id label WaitDeploy selects on.
	ownLabels := trayLabels(p.TrayID, p.TrayType, p.ProviderName)
	if tpl.Labels == nil {
		tpl.Labels = make(map[string]string, len(ownLabels))
	}
	maps.Copy(tpl.Labels, ownLabels)
	tpl.Name = ""
	tpl.GenerateName = ""
	tpl.Namespace = ""
	tpl.Spec.RestartPolicy = corev1.RestartPolicyNever

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.TrayID,
			Namespace: p.Namespace,
			Labels:    maps.Clone(ownLabels),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptrTo(int32(0)),
			PodReplacementPolicy:    ptrTo(batchv1.Failed),
			TTLSecondsAfterFinished: ptrTo(p.Config.TTLSecondsAfterFinishedOrDefault()),
			Template:                *tpl,
		},
	}
	// The API rejects 0, and the documented "unset by default" example
	// spells it as 0.
	if p.Config.ActiveDeadlineSeconds != nil && *p.Config.ActiveDeadlineSeconds > 0 {
		job.Spec.ActiveDeadlineSeconds = ptrTo(*p.Config.ActiveDeadlineSeconds)
	}
	return job, nil
}

// evaluatePod maps a pod's status onto WaitDeploy's outcome: (true, nil) once
// the pod runs, an error when it can never run, (false, nil) to keep waiting.
func evaluatePod(pod *corev1.Pod) (bool, error) {
	if pod.DeletionTimestamp != nil {
		return false, fmt.Errorf("pod %s is being deleted", pod.Name)
	}
	switch pod.Status.Phase {
	case corev1.PodRunning:
		return true, nil
	case corev1.PodSucceeded, corev1.PodFailed:
		return false, fmt.Errorf("pod %s ended with phase %s before the tray became ready: %s",
			pod.Name, pod.Status.Phase, terminationSummary(pod))
	}
	for _, cs := range allContainerStatuses(pod) {
		if w := cs.State.Waiting; w != nil && fatalWaitingReasons[w.Reason] {
			return false, fmt.Errorf("container %s of pod %s cannot start: %s: %s",
				cs.Name, pod.Name, w.Reason, strings.TrimSpace(w.Message))
		}
	}
	return false, nil
}

// terminationSummary explains why a pod ended: the first container that
// terminated abnormally (init containers first), else the pod-level reason.
func terminationSummary(pod *corev1.Pod) string {
	for _, cs := range allContainerStatuses(pod) {
		t := cs.State.Terminated
		if t == nil || (t.ExitCode == 0 && (t.Reason == "" || t.Reason == "Completed")) {
			continue
		}
		s := fmt.Sprintf("%s: %s (exit %d)", cs.Name, t.Reason, t.ExitCode)
		if msg := strings.TrimSpace(t.Message); msg != "" {
			s += ": " + msg
		}
		return s
	}
	if pod.Status.Reason != "" || pod.Status.Message != "" {
		return strings.TrimSpace(pod.Status.Reason + ": " + pod.Status.Message)
	}
	return "no container reported a failure"
}

// describeWaitFailure explains a WaitDeploy timeout from the last pod seen.
func describeWaitFailure(last *corev1.Pod, ns, jobName string) string {
	if last == nil {
		return fmt.Sprintf("no pod was created for job %s/%s (kubectl describe job -n %s %s)", ns, jobName, ns, jobName)
	}
	for _, c := range last.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
			return fmt.Sprintf("pod %s not scheduled: %s: %s", last.Name, c.Reason, strings.TrimSpace(c.Message))
		}
	}
	for _, cs := range allContainerStatuses(last) {
		if w := cs.State.Waiting; w != nil {
			return fmt.Sprintf("container %s of pod %s waiting: %s: %s", cs.Name, last.Name, w.Reason, strings.TrimSpace(w.Message))
		}
	}
	return fmt.Sprintf("pod %s in phase %s", last.Name, last.Status.Phase)
}

func allContainerStatuses(pod *corev1.Pod) []corev1.ContainerStatus {
	out := make([]corev1.ContainerStatus, 0, len(pod.Status.InitContainerStatuses)+len(pod.Status.ContainerStatuses))
	out = append(out, pod.Status.InitContainerStatuses...)
	return append(out, pod.Status.ContainerStatuses...)
}
