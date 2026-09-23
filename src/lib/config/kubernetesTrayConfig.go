package config

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	// KubernetesAgentVersionServer makes the agent init container download the
	// binary from the cattery server (`$CATTERY_URL/agent/download`) instead of
	// copying it out of a published cattery image. It is the default.
	KubernetesAgentVersionServer = "server"

	// DefaultKubernetesRunnerFolder is where ghcr.io/actions/actions-runner
	// ships the Actions runner (<folder>/bin/Runner.Listener).
	DefaultKubernetesRunnerFolder = "/home/runner"

	// DefaultKubernetesRunnerContainer is the container the agent is injected
	// into when a referenced PodTemplate has several containers.
	DefaultKubernetesRunnerContainer = "runner"

	// DefaultKubernetesTTLSecondsAfterFinished lets Kubernetes garbage-collect
	// finished Jobs that cattery failed to delete (e.g. while it was down).
	DefaultKubernetesTTLSecondsAfterFinished int32 = 3600

	// MaxKubernetesTrayTypeNameLength keeps the generated pod name
	// "<trayType>-<16 hex>-<5 random>" within the 63-character limit of a DNS
	// label (it becomes the pod hostname): 63 - 1 - 16 - 1 - 5.
	MaxKubernetesTrayTypeNameLength = 40
)

// KubernetesTrayConfig configures a tray run as a Kubernetes batch/v1 Job.
// Like the other providers' configs it is decoded with mapstructure and holds
// plain Go types; the provider maps them onto the pod spec (see
// lib/trays/providers/kubernetesJob.go) and applies the defaults.
//
// The pod shape comes from one of two mutually exclusive sources:
//
//   - Typed fields (Image required): cattery synthesizes a single-container
//     pod from Image, Resources, NodeSelector, Tolerations, etc.
//   - PodTemplateRef: the name of a core/v1 PodTemplate object in the tray
//     namespace. Its .template is used verbatim (labels, annotations, spec);
//     the typed pod-shape fields are ignored. Use it for anything the typed
//     fields cannot express (sidecars, volumes, affinity, env from Secrets,
//     runtime classes) and for map keys whose case matters: the config loader
//     lowercases every map key, a PodTemplate object is stored as-is.
//
// The agent-injection fields (AgentVersion, AgentImage, RunnerFolder,
// RunnerContainer) and the Job fields (Namespace, TTLSecondsAfterFinished,
// ActiveDeadlineSeconds) apply in both modes.
type KubernetesTrayConfig struct {
	TrayConfig

	// AgentVersion selects how the agent binary gets into the pod: "server"
	// (the default when empty) downloads it from the cattery server; any
	// other value is an image tag of docker.io/paritytech/cattery whose
	// /usr/local/bin/cattery is copied into the pod.
	AgentVersion string `yaml:"agentVersion"`
	// AgentImage overrides the init container image in either mode. In server
	// mode the image needs sh, wget and CA certificates; in tag mode it needs
	// /usr/local/bin/cattery and cp.
	AgentImage string `yaml:"agentImage"`
	// RunnerFolder is where <folder>/bin/Runner.Listener lives in the runner
	// image. Empty means DefaultKubernetesRunnerFolder.
	RunnerFolder string `yaml:"runnerFolder"`
	// RunnerContainer names the container of a referenced PodTemplate that
	// runs the agent. Ignored when the template has exactly one container.
	// Empty means DefaultKubernetesRunnerContainer.
	RunnerContainer string `yaml:"runnerContainer"`
	// Namespace overrides the provider's namespace for this tray type.
	Namespace string `yaml:"namespace"`
	// TTLSecondsAfterFinished is the Job's ttlSecondsAfterFinished. Unset
	// means DefaultKubernetesTTLSecondsAfterFinished; 0 deletes immediately.
	TTLSecondsAfterFinished *int32 `yaml:"ttlSecondsAfterFinished"`
	// ActiveDeadlineSeconds bounds the whole tray lifetime (waiting for a job
	// included). Unset or 0 means no deadline.
	ActiveDeadlineSeconds *int64 `yaml:"activeDeadlineSeconds"`

	// Typed pod shape (ignored when PodTemplateRef is set).
	Image                         string                 `yaml:"image"`
	ImagePullPolicy               string                 `yaml:"imagePullPolicy"`
	ImagePullSecrets              []string               `yaml:"imagePullSecrets"`
	ServiceAccountName            string                 `yaml:"serviceAccountName"`
	AutomountServiceAccountToken  *bool                  `yaml:"automountServiceAccountToken"`
	Resources                     KubernetesResources    `yaml:"resources"`
	NodeSelector                  map[string]string      `yaml:"nodeSelector"`
	Tolerations                   []KubernetesToleration `yaml:"tolerations"`
	Labels                        map[string]string      `yaml:"labels"`
	Annotations                   map[string]string      `yaml:"annotations"`
	Env                           []KubernetesEnvVar     `yaml:"env"`
	RuntimeClassName              string                 `yaml:"runtimeClassName"`
	PriorityClassName             string                 `yaml:"priorityClassName"`
	TerminationGracePeriodSeconds *int64                 `yaml:"terminationGracePeriodSeconds"`

	// PodTemplateRef names a core/v1 PodTemplate in the tray namespace.
	PodTemplateRef string `yaml:"podTemplateRef"`
}

// KubernetesResources mirrors a container's resources block. Quantities use
// the Kubernetes syntax and must be strings ("2", "500m", "4Gi").
type KubernetesResources struct {
	Requests map[string]string `yaml:"requests"`
	Limits   map[string]string `yaml:"limits"`
}

// KubernetesToleration mirrors a pod toleration.
type KubernetesToleration struct {
	Key               string `yaml:"key"`
	Operator          string `yaml:"operator"` // Exists or Equal
	Value             string `yaml:"value"`
	Effect            string `yaml:"effect"` // NoSchedule, PreferNoSchedule or NoExecute
	TolerationSeconds *int64 `yaml:"tolerationSeconds"`
}

// KubernetesEnvVar is a literal env var for the runner container. Values from
// Secrets or ConfigMaps (valueFrom) need a PodTemplate.
type KubernetesEnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// UsesPodTemplateRef reports whether the pod shape comes from a PodTemplate
// object rather than the typed fields.
func (kc KubernetesTrayConfig) UsesPodTemplateRef() bool {
	return kc.PodTemplateRef != ""
}

// TTLSecondsAfterFinishedOrDefault returns the configured TTL or the default.
func (kc KubernetesTrayConfig) TTLSecondsAfterFinishedOrDefault() int32 {
	if kc.TTLSecondsAfterFinished != nil {
		return *kc.TTLSecondsAfterFinished
	}
	return DefaultKubernetesTTLSecondsAfterFinished
}

var (
	kubernetesPullPolicies        = map[string]bool{"": true, "Always": true, "IfNotPresent": true, "Never": true}
	kubernetesTolerationOperators = map[string]bool{"": true, "Exists": true, "Equal": true}
	kubernetesTaintEffects        = map[string]bool{"": true, "NoSchedule": true, "PreferNoSchedule": true, "NoExecute": true}
)

// Validate checks everything the provider relies on so that a broken tray
// type is refused at start-up instead of failing every tray it creates.
// trayTypeName becomes the Job name, hence the DNS-label rule. Typed
// pod-shape fields set alongside podTemplateRef are ignored by the provider;
// Validate logs a warning for each of them.
func (kc KubernetesTrayConfig) Validate(trayTypeName string) error {
	if errs := validation.IsDNS1123Label(trayTypeName); len(errs) > 0 {
		return fmt.Errorf("kubernetes tray type name %q must be a DNS-1123 label (it becomes the Job name): %s",
			trayTypeName, strings.Join(errs, "; "))
	}
	if len(trayTypeName) > MaxKubernetesTrayTypeNameLength {
		return fmt.Errorf("kubernetes tray type name %q is longer than %d characters (the generated pod name must fit in 63)",
			trayTypeName, MaxKubernetesTrayTypeNameLength)
	}

	fail := func(format string, args ...any) error {
		return fmt.Errorf("tray type %s: %s", trayTypeName, fmt.Sprintf(format, args...))
	}

	switch {
	case kc.UsesPodTemplateRef() && kc.Image != "":
		return fail("image and podTemplateRef are mutually exclusive")
	case !kc.UsesPodTemplateRef() && kc.Image == "":
		return fail("one of image or podTemplateRef is required")
	}
	if !kubernetesPullPolicies[kc.ImagePullPolicy] {
		return fail("imagePullPolicy %q must be Always, IfNotPresent or Never", kc.ImagePullPolicy)
	}
	for section, list := range map[string]map[string]string{"requests": kc.Resources.Requests, "limits": kc.Resources.Limits} {
		for name, raw := range list {
			if _, err := resource.ParseQuantity(raw); err != nil {
				return fail("resources.%s.%s: %q is not a quantity: %v", section, name, raw, err)
			}
		}
	}
	for i, t := range kc.Tolerations {
		if !kubernetesTolerationOperators[t.Operator] {
			return fail("tolerations[%d].operator %q must be Exists or Equal", i, t.Operator)
		}
		if !kubernetesTaintEffects[t.Effect] {
			return fail("tolerations[%d].effect %q must be NoSchedule, PreferNoSchedule or NoExecute", i, t.Effect)
		}
	}
	for i, e := range kc.Env {
		if e.Name == "" {
			return fail("env[%d].name is required", i)
		}
	}
	if kc.TTLSecondsAfterFinished != nil && *kc.TTLSecondsAfterFinished < 0 {
		return fail("ttlSecondsAfterFinished must not be negative")
	}
	if kc.ActiveDeadlineSeconds != nil && *kc.ActiveDeadlineSeconds < 0 {
		return fail("activeDeadlineSeconds must not be negative")
	}
	if kc.TerminationGracePeriodSeconds != nil && *kc.TerminationGracePeriodSeconds < 0 {
		return fail("terminationGracePeriodSeconds must not be negative")
	}

	if kc.UsesPodTemplateRef() {
		for _, f := range kc.ignoredTypedFields() {
			log.Warnf("trayType %s: config.%s is ignored because podTemplateRef is set", trayTypeName, f)
		}
	} else if kc.RunnerContainer != "" {
		log.Warnf("trayType %s: config.runnerContainer only applies with podTemplateRef", trayTypeName)
	}
	return nil
}

// ignoredTypedFields lists the typed pod-shape fields that are set.
func (kc KubernetesTrayConfig) ignoredTypedFields() []string {
	var set []string
	add := func(cond bool, name string) {
		if cond {
			set = append(set, name)
		}
	}
	add(kc.ImagePullPolicy != "", "imagePullPolicy")
	add(len(kc.ImagePullSecrets) > 0, "imagePullSecrets")
	add(kc.ServiceAccountName != "", "serviceAccountName")
	add(kc.AutomountServiceAccountToken != nil, "automountServiceAccountToken")
	add(len(kc.Resources.Requests) > 0 || len(kc.Resources.Limits) > 0, "resources")
	add(len(kc.NodeSelector) > 0, "nodeSelector")
	add(len(kc.Tolerations) > 0, "tolerations")
	add(len(kc.Labels) > 0, "labels")
	add(len(kc.Annotations) > 0, "annotations")
	add(len(kc.Env) > 0, "env")
	add(kc.RuntimeClassName != "", "runtimeClassName")
	add(kc.PriorityClassName != "", "priorityClassName")
	add(kc.TerminationGracePeriodSeconds != nil, "terminationGracePeriodSeconds")
	return set
}
