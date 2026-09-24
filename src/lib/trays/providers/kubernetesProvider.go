package providers

import (
	"cattery/lib/config"
	"cattery/lib/kube"
	"cattery/lib/trays"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

const (
	kubernetesProviderDataNamespace = "namespace"
	kubernetesProviderDataJobName   = "jobName"
	kubernetesProviderDataJobUID    = "jobUid"

	defaultKubernetesDeployTimeout = 5 * time.Minute
)

// KubernetesProvider runs each tray as a batch/v1 Job whose pod executes the
// cattery agent. See kubernetesJob.go for the Job shape and
// config.KubernetesTrayConfig for the two ways to describe the pod.
type KubernetesProvider struct {
	name string

	client        kubernetes.Interface
	namespace     string
	deployTimeout time.Duration

	logger *logrus.Entry
}

// NewKubernetesProvider builds the provider from its config entry. Keys, all
// optional: kubeconfig, context, server, token, tokenFile, caFile, insecure
// (cluster access, see kube.Options), namespace, deployTimeout. Returns nil
// (after logging) when the config is unusable, matching the other providers.
func NewKubernetesProvider(name string, providerConfig config.ProviderConfig) *KubernetesProvider {
	logger := logrus.WithFields(logrus.Fields{
		"name":         "KubernetesProvider",
		"providerName": name,
		"providerType": "kubernetes",
	})

	// The provider name is stamped on every Job as a label value.
	if errs := validation.IsValidLabelValue(name); len(errs) > 0 {
		logger.Errorf("kubernetes provider name %q must be a valid label value (it becomes the %s label): %s",
			name, labelProvider, strings.Join(errs, "; "))
		return nil
	}

	deployTimeout := defaultKubernetesDeployTimeout
	if raw := providerConfig.Get("deployTimeout"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			logger.Errorf("kubernetes provider has invalid 'deployTimeout' %q: must be a positive duration such as 5m", raw)
			return nil
		}
		deployTimeout = d
	}

	opts := kube.Options{
		Kubeconfig: providerConfig.Get("kubeconfig"),
		Context:    providerConfig.Get("context"),
		Server:     providerConfig.Get("server"),
		Token:      providerConfig.Get("token"),
		TokenFile:  providerConfig.Get("tokenFile"),
		CAFile:     providerConfig.Get("caFile"),
	}
	// Provider config is a string map; viper renders an unquoted YAML bool
	// into it as "1"/"0", a quoted one as "true"/"false". ParseBool accepts
	// both spellings.
	if raw := providerConfig.Get("insecure"); raw != "" {
		insecure, err := strconv.ParseBool(raw)
		if err != nil {
			logger.Errorf("kubernetes provider has invalid 'insecure' value %q: %v", raw, err)
			return nil
		}
		opts.Insecure = insecure
	}

	restCfg, kubeNamespace, err := kube.NewRestConfig(opts)
	if err != nil {
		logger.Errorf("failed to load kubernetes client config: %v", err)
		return nil
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		logger.Errorf("failed to create kubernetes client: %v", err)
		return nil
	}

	namespace := providerConfig.Get("namespace")
	if namespace == "" {
		namespace = kubeNamespace
	}
	logger.Infof("Kubernetes provider ready: host=%s namespace=%s deployTimeout=%s", restCfg.Host, namespace, deployTimeout)
	return newKubernetesProvider(name, client, namespace, deployTimeout)
}

// newKubernetesProvider builds the provider from an already-constructed
// client. Split from NewKubernetesProvider so tests can inject a fake.
func newKubernetesProvider(name string, client kubernetes.Interface, namespace string, deployTimeout time.Duration) *KubernetesProvider {
	if deployTimeout <= 0 {
		deployTimeout = defaultKubernetesDeployTimeout
	}
	return &KubernetesProvider{
		name:          name,
		client:        client,
		namespace:     namespace,
		deployTimeout: deployTimeout,
		logger: logrus.WithFields(logrus.Fields{
			"name":         "KubernetesProvider",
			"providerName": name,
			"providerType": "kubernetes",
		}),
	}
}

func (k *KubernetesProvider) GetProviderName() string {
	return k.name
}

// StartDeploy creates the tray's Job.
//
// namespace and jobName are staged on tray.ProviderData before any API call
// so that, once trayManager persists ProviderData (on the success and the
// error path alike), CleanTray can always address the Job — a create whose
// response was lost still gets cleaned up, and a create that never happened
// resolves to NotFound. jobUid is recorded from the response and guards the
// delete against a same-named Job created by someone else.
func (k *KubernetesProvider) StartDeploy(ctx context.Context, tray *trays.Tray) error {
	kc, ok := tray.TrayConfig().(config.KubernetesTrayConfig)
	if !ok {
		return fmt.Errorf("unexpected tray config type for kubernetes provider, tray %s", tray.Id)
	}

	ns := kc.Namespace
	if ns == "" {
		ns = k.namespace
	}
	tray.ProviderData[kubernetesProviderDataNamespace] = ns
	tray.ProviderData[kubernetesProviderDataJobName] = tray.Id

	tpl, runnerIdx, err := k.resolvePodTemplate(ctx, ns, kc)
	if err != nil {
		k.logger.Errorf("Failed to resolve pod template for tray %s: %v", tray.Id, err)
		return err
	}

	job, err := buildTrayJob(jobBuildParams{
		TrayID:       tray.Id,
		TrayType:     tray.TrayTypeName,
		ProviderName: k.name,
		Namespace:    ns,
		ServerURL:    config.Get().Server.AdvertiseUrl,
		Config:       kc,
		Template:     tpl,
		RunnerIdx:    runnerIdx,
	})
	if err != nil {
		return fmt.Errorf("build job for tray %s: %w", tray.Id, err)
	}

	created, err := k.client.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return k.adoptExistingJob(ctx, ns, tray)
		}
		k.logger.Errorf("Failed to create job %s/%s for tray %s: %v", ns, tray.Id, tray.Id, err)
		return err
	}
	if created.UID != "" {
		tray.ProviderData[kubernetesProviderDataJobUID] = string(created.UID)
	}

	k.logger.Infof("Created job %s/%s for tray %s", ns, tray.Id, tray.Id)
	return nil
}

// adoptExistingJob handles an AlreadyExists on create: a Job with the tray's
// name is fine as long as it was created for this tray (a retried create whose
// first response was lost); anything else is someone else's Job.
func (k *KubernetesProvider) adoptExistingJob(ctx context.Context, ns string, tray *trays.Tray) error {
	existing, err := k.client.BatchV1().Jobs(ns).Get(ctx, tray.Id, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("job %s/%s already exists but could not be read: %w", ns, tray.Id, err)
	}
	if existing.Labels[labelTrayID] != tray.Id {
		return fmt.Errorf("job %s/%s already exists and does not belong to tray %s", ns, tray.Id, tray.Id)
	}
	if existing.UID != "" {
		tray.ProviderData[kubernetesProviderDataJobUID] = string(existing.UID)
	}
	k.logger.Warnf("Job %s/%s already exists for tray %s; adopting it", ns, tray.Id, tray.Id)
	return nil
}

// resolvePodTemplate returns the pod shape for the tray and the index of the
// container that will run the agent. In podTemplateRef mode the PodTemplate
// object is read on every call so edits apply to the next tray.
func (k *KubernetesProvider) resolvePodTemplate(ctx context.Context, ns string, kc config.KubernetesTrayConfig) (*corev1.PodTemplateSpec, int, error) {
	if !kc.UsesPodTemplateRef() {
		tpl, err := podTemplateFromTypedConfig(kc)
		if err != nil {
			return nil, 0, fmt.Errorf("typed pod config: %w", err)
		}
		return &tpl, 0, nil
	}

	pt, err := k.client.CoreV1().PodTemplates(ns).Get(ctx, kc.PodTemplateRef, metav1.GetOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("get PodTemplate %s/%s: %w", ns, kc.PodTemplateRef, err)
	}
	tpl := pt.Template.DeepCopy()
	runnerContainer := kc.RunnerContainer
	if runnerContainer == "" {
		runnerContainer = config.DefaultKubernetesRunnerContainer
	}
	idx, err := resolveRunnerContainer(&tpl.Spec, runnerContainer)
	if err != nil {
		return nil, 0, fmt.Errorf("PodTemplate %s/%s: %w", ns, kc.PodTemplateRef, err)
	}
	return tpl, idx, nil
}

// WaitDeploy blocks until the Job's pod is Running, bounded by deployTimeout.
// Mapping:
//
//   - pod Running:                         nil (agent registration is the
//     readiness signal from here)
//   - image pull / container config error: error, immediately
//   - pod Failed/Succeeded/deleted:         error, immediately
//   - nothing running within deployTimeout: error naming the last known
//     obstacle (unschedulable, still pulling, no pod created at all)
//   - ctx cancellation:                     ctx error
//
// The pod is found through the cattery.io/tray-id label the Job's pod
// template carries; the Job controller assigns the pod name.
func (k *KubernetesProvider) WaitDeploy(ctx context.Context, tray *trays.Tray) error {
	jobName := tray.ProviderData[kubernetesProviderDataJobName]
	if jobName == "" {
		k.logger.Tracef("No job name stored for tray %s; skipping wait", tray.Id)
		return nil
	}
	ns := k.namespaceFor(tray)

	waitCtx, cancel := context.WithTimeout(ctx, k.deployTimeout)
	defer cancel()

	var last *corev1.Pod
	err := k.waitForRunningPod(waitCtx, ns, tray.Id, &last)
	switch {
	case err == nil:
		k.logger.Infof("Pod of job %s/%s is running (tray %s)", ns, jobName, tray.Id)
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case waitCtx.Err() != nil:
		return fmt.Errorf("timed out after %s waiting for job %s/%s (tray %s): %s",
			k.deployTimeout, ns, jobName, tray.Id, describeWaitFailure(last, ns, jobName))
	default:
		return fmt.Errorf("job %s/%s (tray %s): %w", ns, jobName, tray.Id, err)
	}
}

// waitForRunningPod lists the tray's pods, then follows a watch from the
// list's resource version, re-listing whenever the watch ends or the server
// reports the version as expired. It returns nil once a pod is Running, an
// error when a pod can never run, or the ctx error. last tracks the most
// recent pod seen so a timeout can be explained.
//
// This is a deliberately small list+watch loop rather than an informer: one
// pod, one bounded wait, and it works with any client (informers in this
// client-go version stream their initial state, which fakes do not support).
func (k *KubernetesProvider) waitForRunningPod(ctx context.Context, ns, trayID string, last **corev1.Pod) error {
	selector := labels.SelectorFromSet(labels.Set{labelTrayID: trayID}).String()
	pods := k.client.CoreV1().Pods(ns)

	// The label is re-checked on every object: it costs nothing with the real
	// API and keeps the logic correct with clients that do not filter watches.
	check := func(p *corev1.Pod) (bool, error) {
		if p == nil || p.Labels[labelTrayID] != trayID {
			return false, nil
		}
		*last = p
		return evaluatePod(p)
	}

	for {
		list, err := pods.List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return fmt.Errorf("list pods: %w", err)
		}
		for i := range list.Items {
			if done, err := check(&list.Items[i]); done || err != nil {
				return err
			}
		}

		w, err := pods.Watch(ctx, metav1.ListOptions{
			LabelSelector:       selector,
			ResourceVersion:     list.ResourceVersion,
			AllowWatchBookmarks: true,
		})
		if err != nil {
			return fmt.Errorf("watch pods: %w", err)
		}
		done, err := followWatch(ctx, w, trayID, check)
		if done || err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The watch ended (server-side timeout, dropped connection or an
		// expired resource version): start over from a fresh list.
	}
}

// followWatch consumes events until check reports an outcome, ctx ends, or
// the watch closes. (false, nil) means the watch ended and the caller should
// list again.
func followWatch(ctx context.Context, w watch.Interface, trayID string, check func(*corev1.Pod) (bool, error)) (bool, error) {
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case ev, ok := <-w.ResultChan():
			if !ok {
				return false, nil
			}
			switch ev.Type {
			case watch.Bookmark:
				continue
			case watch.Error:
				err := apierrors.FromObject(ev.Object)
				if apierrors.IsGone(err) || apierrors.IsResourceExpired(err) {
					return false, nil
				}
				return false, err
			case watch.Deleted:
				if p, ok := ev.Object.(*corev1.Pod); ok && p.Labels[labelTrayID] == trayID {
					return false, fmt.Errorf("pod %s was deleted before the tray became ready", p.Name)
				}
				continue
			}
			p, ok := ev.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			if done, err := check(p); done || err != nil {
				return done, err
			}
		}
	}
}

// CleanTray deletes the tray's Job and, through background propagation, its
// pod. Safe on a tray StartDeploy never finished: the Job name is derived
// from the tray id when ProviderData lacks it, and NotFound is success.
//
// The delete is always pinned to the Job's UID. When StartDeploy did not
// record one (it failed before or during the create, or it found a same-named
// Job that is not ours), the Job is read first and only deleted when it
// carries this tray's label, so cleanup after a failed deploy can never take
// somebody else's Job with it.
func (k *KubernetesProvider) CleanTray(ctx context.Context, tray *trays.Tray) error {
	name := tray.ProviderData[kubernetesProviderDataJobName]
	if name == "" {
		name = tray.Id
	}
	ns := k.namespaceFor(tray)

	uid := types.UID(tray.ProviderData[kubernetesProviderDataJobUID])
	if uid == "" {
		existing, err := k.client.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			k.logger.Tracef("Job %s/%s does not exist; nothing to do", ns, name)
			return nil
		case err != nil:
			k.logger.Errorf("Failed to read job %s/%s for tray %s: %v", ns, name, tray.Id, err)
			return err
		case existing.Labels[labelTrayID] != tray.Id:
			k.logger.Warnf("Job %s/%s does not belong to tray %s (%s=%q); leaving it alone",
				ns, name, tray.Id, labelTrayID, existing.Labels[labelTrayID])
			return nil
		}
		uid = existing.UID
	}

	// batch/v1 Jobs orphan their pods on delete unless a propagation policy
	// is set (kubectl sets one; the API default is orphan).
	policy := metav1.DeletePropagationBackground
	opts := metav1.DeleteOptions{PropagationPolicy: &policy}
	if uid != "" {
		opts.Preconditions = &metav1.Preconditions{UID: &uid}
	}

	err := k.client.BatchV1().Jobs(ns).Delete(ctx, name, opts)
	switch {
	case err == nil:
		k.logger.Infof("Deleted job %s/%s for tray %s", ns, name, tray.Id)
		return nil
	case apierrors.IsNotFound(err):
		k.logger.Tracef("Job %s/%s already gone; nothing to do", ns, name)
		return nil
	case apierrors.IsConflict(err):
		k.logger.Warnf("Job %s/%s exists with a different UID than tray %s recorded; leaving it alone", ns, name, tray.Id)
		return nil
	default:
		k.logger.Errorf("Failed to delete job %s/%s for tray %s: %v", ns, name, tray.Id, err)
		return err
	}
}

func (k *KubernetesProvider) namespaceFor(tray *trays.Tray) string {
	if ns := tray.ProviderData[kubernetesProviderDataNamespace]; ns != "" {
		return ns
	}
	return k.namespace
}
