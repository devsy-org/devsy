package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/dockercredentials"
	"github.com/devsy-org/devsy/pkg/driver"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

const (
	testNamespace             = "devsy-test"
	testPodID                 = "devsy-ws"
	testWorkspace             = "ws-uid"
	podResourceName           = "pods"
	preservedCustomerMetadata = "keep"
)

var podsResource = schema.GroupResource{Resource: podResourceName}

type fakeCluster struct {
	cs *fake.Clientset
	k  *KubernetesDriver
}

func newFakeCluster(t *testing.T, objects ...runtime.Object) *fakeCluster {
	t.Helper()
	oldPoll, oldTimeout := podPollInterval, podDeleteTimeout
	podPollInterval, podDeleteTimeout = 5*time.Millisecond, 2*time.Second
	t.Cleanup(func() { podPollInterval, podDeleteTimeout = oldPoll, oldTimeout })

	cs := fake.NewClientset(objects...)
	return &fakeCluster{
		cs: cs,
		k: &KubernetesDriver{
			client:    &Client{client: cs},
			namespace: testNamespace,
			options: &provider2.ProviderKubernetesDriverConfig{
				PodTimeout: "2s",
				DiskSize:   "1Gi",
			},
			agentConfig: &provider2.ProviderAgentConfig{},
		},
	}
}

func (c *fakeCluster) opts() *driver.RunOptions {
	return &driver.RunOptions{UID: testWorkspace}
}

type podSpec struct {
	uid      string
	phase    corev1.PodPhase
	options  *provider2.ProviderKubernetesDriverConfig
	deleting bool
	foreign  bool
	devsy    *corev1.ContainerStatus
}

func (c *fakeCluster) pod(t *testing.T, s podSpec) *corev1.Pod {
	t.Helper()
	if s.options == nil {
		s.options = c.k.options
	}
	raw, err := json.Marshal(s.options)
	require.NoError(t, err)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testPodID,
			Namespace:   testNamespace,
			UID:         types.UID(s.uid),
			Labels:      map[string]string{DevsyWorkspaceUIDLabel: testWorkspace},
			Annotations: map[string]string{DevsyLastAppliedAnnotation: string(raw)},
		},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: DevContainerName}}},
		Status: corev1.PodStatus{Phase: s.phase},
	}
	if !s.foreign {
		pod.Labels[DevsyCreatedLabel] = pkgconfig.BoolTrue
	}
	if s.deleting {
		now := metav1.Now()
		pod.DeletionTimestamp = &now
		pod.Finalizers = []string{"test/hold"}
	}
	if s.devsy != nil {
		s.devsy.Name = DevContainerName
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{*s.devsy}
	}
	return pod
}

func (c *fakeCluster) putPod(t *testing.T, pod *corev1.Pod) {
	t.Helper()
	pods := c.cs.CoreV1().Pods(testNamespace)
	_, err := pods.Get(context.Background(), pod.Name, metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		_, err = pods.Create(context.Background(), pod, metav1.CreateOptions{})
	} else {
		_, err = pods.Update(context.Background(), pod, metav1.UpdateOptions{})
	}
	require.NoError(t, err)
}

func (c *fakeCluster) podExists(t *testing.T) bool {
	t.Helper()
	_, err := c.cs.CoreV1().
		Pods(testNamespace).
		Get(context.Background(), testPodID, metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		return false
	}
	require.NoError(t, err)
	return true
}

func (c *fakeCluster) deleteCalls() int {
	n := 0
	for _, a := range c.cs.Actions() {
		if a.Matches("delete", podResourceName) {
			n++
		}
	}
	return n
}

func (c *fakeCluster) reconcile(ctx context.Context) (bool, error) {
	return c.k.reconcileExistingPod(ctx, testPodID, c.opts())
}

func TestReconcileNoPodAllowsCreate(t *testing.T) {
	c := newFakeCluster(t)

	reused, err := c.reconcile(context.Background())

	require.NoError(t, err)
	assert.False(t, reused)
}

func TestReconcileReusesRunningPodWithSameOptions(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{
		uid:   "u1",
		phase: corev1.PodRunning,
		devsy: &corev1.ContainerStatus{
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		},
	}))

	reused, err := c.reconcile(context.Background())

	require.NoError(t, err)
	assert.True(t, reused)
	assert.Zero(t, c.deleteCalls())
}

func TestReconcileReplacesTerminalPodEvenWithSameOptions(t *testing.T) {
	for _, phase := range []corev1.PodPhase{corev1.PodFailed, corev1.PodSucceeded} {
		t.Run(string(phase), func(t *testing.T) {
			c := newFakeCluster(t)
			c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: phase}))

			reused, err := c.reconcile(context.Background())

			require.NoError(t, err)
			assert.False(t, reused, "a terminal pod must not be reused")
			assert.False(
				t,
				c.podExists(t),
				"the terminal pod is gone before the new one is created",
			)
		})
	}
}

func TestReconcileReplacesPodWhoseDevsyContainerExited(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{
		uid: "u1", phase: corev1.PodRunning,
		devsy: &corev1.ContainerStatus{State: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: 0},
		}},
	}))

	reused, err := c.reconcile(context.Background())

	require.NoError(t, err)
	assert.False(t, reused)
	assert.False(t, c.podExists(t))
}

func TestReconcileReplacesPodWhenOptionsChanged(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{
		uid: "u1", phase: corev1.PodRunning,
		options: &provider2.ProviderKubernetesDriverConfig{PodTimeout: "2s", DiskSize: "9Gi"},
	}))

	reused, err := c.reconcile(context.Background())

	require.NoError(t, err)
	assert.False(t, reused)
	assert.False(t, c.podExists(t))
}

func TestReconcileReusesPodWhenOnlyImageConfigFileChanged(t *testing.T) {
	c := newFakeCluster(t)
	c.k.options.ImageConfigFile = "/new/image-config.json"
	c.putPod(t, c.pod(t, podSpec{
		uid:   "u1",
		phase: corev1.PodRunning,
		options: &provider2.ProviderKubernetesDriverConfig{
			PodTimeout:      "2s",
			DiskSize:        "1Gi",
			ImageConfigFile: "/old/image-config.json",
		},
		devsy: &corev1.ContainerStatus{
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		},
	}))

	reused, err := c.reconcile(context.Background())

	require.NoError(t, err)
	assert.True(t, reused)
	assert.Zero(t, c.deleteCalls())
}

func TestWithoutLocalOptionsDropsImageConfigFile(t *testing.T) {
	options := &provider2.ProviderKubernetesDriverConfig{
		DiskSize:        "1Gi",
		ImageConfigFile: "/home/me/image-config.json",
	}

	raw, err := json.Marshal(withoutLocalOptions(options))

	require.NoError(t, err)
	assert.NotContains(t, string(raw), "imageConfigFile")
	assert.Contains(t, string(raw), "1Gi")
	assert.Equal(t, "/home/me/image-config.json", options.ImageConfigFile)
}

func TestReconcilePendingPodWaitsForReadiness(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: corev1.PodPending}))
	observed := observePodGets(c)
	type result struct {
		reused bool
		err    error
	}
	done := make(chan result, 1)
	go func() {
		reused, err := c.reconcile(t.Context())
		done <- result{reused: reused, err: err}
	}()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("reconcile did not inspect the pending pod")
	}
	select {
	case got := <-done:
		t.Fatalf("reconcile returned before the pod was ready: %+v", got)
	case <-time.After(40 * time.Millisecond):
	}
	c.putPod(t, readyRunningPod(t, c))
	select {
	case got := <-done:
		require.NoError(t, got.err)
		assert.True(t, got.reused)
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile did not return after the pod became ready")
	}
}

func observePodGets(c *fakeCluster) chan struct{} {
	observed := make(chan struct{}, 1)
	c.cs.PrependReactor(
		"get", podResourceName, func(clienttesting.Action) (bool, runtime.Object, error) {
			select {
			case observed <- struct{}{}:
			default:
			}

			return false, nil, nil
		},
	)

	return observed
}

func readyRunningPod(t *testing.T, c *fakeCluster) *corev1.Pod {
	t.Helper()
	running := c.pod(t, podSpec{uid: "u1", phase: corev1.PodRunning})
	running.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name: DevContainerName, Ready: true,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}

	return running
}

func TestReconcileTerminatingPodWaitsForDeletionThenAllowsCreate(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: corev1.PodRunning, deleting: true}))
	go func() {
		time.Sleep(40 * time.Millisecond)
		_ = c.cs.CoreV1().Pods(testNamespace).Delete(
			context.Background(), testPodID, metav1.DeleteOptions{},
		)
	}()

	reused, err := c.reconcile(context.Background())

	require.NoError(t, err)
	assert.False(t, reused)
	assert.False(t, c.podExists(t))
	assert.Zero(t, c.deleteCalls()-1, "a terminating pod is waited on, not deleted again")
}

func TestReconcileForbiddenLookupIsNotAbsence(t *testing.T) {
	c := newFakeCluster(t)
	c.cs.PrependReactor(
		"get",
		podResourceName,
		func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, kerrors.NewForbidden(podsResource, testPodID, errors.New("rbac"))
		},
	)

	reused, err := c.reconcile(context.Background())

	require.Error(t, err)
	assert.True(t, kerrors.IsForbidden(err), "forbidden must surface")
	assert.Contains(t, err.Error(), "forbidden")
	assert.False(t, reused)
}

func TestReconcileDeleteForbiddenSurfaces(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: corev1.PodFailed}))
	c.cs.PrependReactor(
		"delete",
		podResourceName,
		func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, kerrors.NewForbidden(podsResource, testPodID, errors.New("rbac"))
		},
	)

	_, err := c.reconcile(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
	assert.True(t, c.podExists(t), "nothing is assumed deleted")
}

func TestWaitPodGoneDoesNotTreatLookupErrorsAsDeleted(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: corev1.PodFailed}))
	c.cs.PrependReactor(
		"get",
		podResourceName,
		func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("connection refused")
		},
	)

	err := c.k.waitPodGone(context.Background(), testPodID, "u1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestReconcileUIDConflictReobservesReplacementPod(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{uid: "old", phase: corev1.PodFailed}))
	var conflicted atomic.Bool
	c.cs.PrependReactor(
		"delete",
		podResourceName,
		func(a clienttesting.Action) (bool, runtime.Object, error) {
			if conflicted.Swap(true) {
				return false, nil, nil
			}
			// Someone replaced the pod between our observation and our delete.
			replacement := c.pod(t, podSpec{
				uid:   "new",
				phase: corev1.PodRunning,
				devsy: &corev1.ContainerStatus{
					State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				},
			})
			require.NoError(t, c.cs.Tracker().Update(
				corev1.SchemeGroupVersion.WithResource(podResourceName), replacement, testNamespace,
			))
			return true, nil, kerrors.NewConflict(podsResource, testPodID, errors.New("uid"))
		},
	)

	reused, err := c.reconcile(context.Background())

	require.NoError(t, err)
	assert.True(t, reused, "the healthy replacement is kept, not deleted by stale knowledge")
	assert.True(t, c.podExists(t))
}

func TestReconcileDeleteCarriesObservedUID(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{uid: "observed", phase: corev1.PodFailed}))
	var got *types.UID
	c.cs.PrependReactor(
		"delete",
		podResourceName,
		func(a clienttesting.Action) (bool, runtime.Object, error) {
			if del, ok := a.(clienttesting.DeleteActionImpl); ok &&
				del.DeleteOptions.Preconditions != nil {
				got = del.DeleteOptions.Preconditions.UID
			}
			return false, nil, nil
		},
	)

	_, err := c.reconcile(context.Background())

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, types.UID("observed"), *got)
}

func TestReconcileCancellationWhileWaitingForDeletion(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: corev1.PodRunning, deleting: true}))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.reconcile(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, c.podExists(t))
}

func TestReconcileRefusesPodsNotOwnedByDevsy(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: corev1.PodFailed, foreign: true}))

	_, err := c.reconcile(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not created by Devsy")
	assert.True(t, c.podExists(t))
}

func TestReconcileRefusesPodOfAnotherWorkspace(t *testing.T) {
	c := newFakeCluster(t)
	pod := c.pod(t, podSpec{uid: "u1", phase: corev1.PodFailed})
	pod.Labels[DevsyWorkspaceUIDLabel] = "someone-else"
	c.putPod(t, pod)

	_, err := c.reconcile(context.Background())

	require.Error(t, err)
	assert.True(t, c.podExists(t))
}

func TestReconcileAdoptsOldPodThroughWorkspaceVolume(t *testing.T) {
	c := newFakeCluster(t)
	pod := c.pod(t, podSpec{uid: "u1", phase: corev1.PodFailed})
	delete(pod.Labels, DevsyWorkspaceUIDLabel)
	pod.Spec.Volumes = []corev1.Volume{{
		Name: DevContainerName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: testPodID},
		},
	}}
	c.putPod(t, pod)

	_, err := c.reconcile(context.Background())

	require.NoError(t, err)
	assert.False(t, c.podExists(t))
}

func TestReplacementPreservesWorkspaceVolume(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: testPodID, Namespace: testNamespace, UID: "pvc-uid"},
	}
	c := newFakeCluster(t, pvc)
	c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: corev1.PodSucceeded}))

	_, err := c.reconcile(context.Background())
	require.NoError(t, err)

	got, err := c.cs.CoreV1().PersistentVolumeClaims(testNamespace).
		Get(context.Background(), testPodID, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, types.UID("pvc-uid"), got.UID)
	for _, a := range c.cs.Actions() {
		assert.False(t, a.Matches("delete", "persistentvolumeclaims"), "PVC must never be deleted")
	}
}

func TestFindDevContainerReportsLookupErrors(t *testing.T) {
	c := newFakeCluster(t, workspacePVC(t))
	c.cs.PrependReactor(
		"get",
		podResourceName,
		func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, kerrors.NewForbidden(podsResource, testPodID, errors.New("rbac"))
		},
	)

	details, err := c.k.FindDevContainer(context.Background(), "ws")

	require.Error(t, err)
	assert.Nil(t, details)
}

func TestFindDevContainerTreatsTerminalPodAsStopped(t *testing.T) {
	c := newFakeCluster(t, workspacePVC(t))
	c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: corev1.PodSucceeded}))

	details, err := c.k.FindDevContainer(context.Background(), "ws")

	require.NoError(t, err)
	assert.Equal(t, config.ContainerStatusExited, details.State.Status)
}

func TestFindDevContainerKeepsVolumeWithoutSavedOptionsFindable(t *testing.T) {
	pvc := workspacePVC(t)
	raw, err := json.Marshal(&DevContainerInfo{WorkspaceID: "ws"})
	require.NoError(t, err)
	pvc.Annotations[DevsyInfoAnnotation] = string(raw)
	c := newFakeCluster(t, pvc)

	details, err := c.k.FindDevContainer(context.Background(), "ws")

	require.NoError(t, err, "stop and delete must still find a volume with no saved options")
	require.NotNil(t, details)
	assert.Equal(t, config.ContainerStatusExited, details.State.Status)
	assert.Contains(t, details.LifecycleTopologyError, "no saved run options")
}

func TestReusedRunningPodWithExtraContainerIsRejectedWhenTimeoutEnabled(t *testing.T) {
	managed := agentd.LifecycleConfig{Owner: agentd.LifecycleOwnerManagedDaemon}
	c := newFakeCluster(t)
	pod := c.pod(t, podSpec{uid: "u1", phase: corev1.PodRunning})
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: "istio-proxy"})
	c.putPod(t, pod)

	reused, err := c.k.reconcileDesiredPod(
		context.Background(), pod,
		daemonOptions(t, agentd.DaemonConfig{Timeout: "1m", Lifecycle: managed}),
	)

	require.ErrorContains(t, err, "istio-proxy")
	assert.False(t, reused)
	assert.True(t, c.podExists(t), "a rejected reuse must not delete the pod")
}

func TestReusedRunningPodWithExtraContainerIsKeptWhenTimeoutDisabled(t *testing.T) {
	managed := agentd.LifecycleConfig{Owner: agentd.LifecycleOwnerManagedDaemon}
	c := newFakeCluster(t)
	pod := c.pod(t, podSpec{uid: "u1", phase: corev1.PodRunning})
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: "istio-proxy"})
	c.putPod(t, pod)

	reused, err := c.k.reconcileDesiredPod(
		context.Background(), pod,
		daemonOptions(t, agentd.DaemonConfig{Lifecycle: managed}),
	)

	require.NoError(t, err)
	assert.True(t, reused)
}

func TestVerifyDesiredPodReportsMissingPodWithoutInternalSentinel(t *testing.T) {
	options := canonicalTopologyOptions(t, false)
	c := newFakeCluster(t, savedRunVolume(t, options))

	err := c.k.verifyDesiredPod(context.Background(), testPodID, options)

	require.Error(t, err)
	assert.NotErrorIs(t, err, errPodChanged)
	assert.Contains(t, err.Error(), "retry the operation")
}

func workspacePVC(t *testing.T) *corev1.PersistentVolumeClaim {
	t.Helper()
	info, err := json.Marshal(
		&DevContainerInfo{WorkspaceID: "ws", Options: &driver.RunOptions{UID: testWorkspace}},
	)
	require.NoError(t, err)
	return &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name: testPodID, Namespace: testNamespace,
		Annotations: map[string]string{DevsyInfoAnnotation: string(info)},
	}}
}

func daemonOptions(t *testing.T, cfg agentd.DaemonConfig) *driver.RunOptions {
	t.Helper()
	encoded, err := agentd.EncodeDaemonConfig(&cfg)
	require.NoError(t, err)
	return &driver.RunOptions{Env: map[string]string{"DEVSY_WORKSPACE_DAEMON_CONFIG": encoded}}
}

func TestTopologyValidation(t *testing.T) {
	regular := func(names ...string) *corev1.Pod {
		pod := &corev1.Pod{}
		for _, n := range names {
			pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: n})
		}
		return pod
	}
	always := corev1.ContainerRestartPolicyAlways

	t.Run("single devsy container", func(t *testing.T) {
		assert.NoError(t, validatePodTopology(regular(DevContainerName), time.Minute))
	})
	t.Run("native sidecar and init container allowed", func(t *testing.T) {
		pod := regular(DevContainerName)
		pod.Spec.InitContainers = []corev1.Container{
			{Name: "mesh", RestartPolicy: &always},
			{Name: "setup"},
		}
		assert.NoError(t, validatePodTopology(pod, time.Minute))
	})
	t.Run("extra regular containers rejected by name", func(t *testing.T) {
		err := validatePodTopology(regular(DevContainerName, "istio-proxy", "logger"), time.Minute)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "istio-proxy")
		assert.Contains(t, err.Error(), "logger")
	})
	t.Run("extra containers fine when timeout disabled", func(t *testing.T) {
		assert.NoError(t, validatePodTopology(regular(DevContainerName, "istio-proxy"), 0))
	})
	t.Run("missing devsy container rejected", func(t *testing.T) {
		assert.Error(t, validatePodTopology(regular("other"), time.Minute))
	})
}

func TestLifecycleTimeoutFromGeneratedConfig(t *testing.T) {
	managed := agentd.LifecycleConfig{Owner: agentd.LifecycleOwnerManagedDaemon}

	got, err := lifecycleTimeout(
		daemonOptions(t, agentd.DaemonConfig{Timeout: "2m", Lifecycle: managed}),
	)
	require.NoError(t, err)
	assert.Equal(t, 2*time.Minute, got)

	got, err = lifecycleTimeout(daemonOptions(t, agentd.DaemonConfig{
		Timeout: "2m", Lifecycle: managed, ShutdownAction: config.ShutdownActionNone,
	}))
	require.NoError(t, err)
	assert.Zero(t, got, "shutdownAction none disables the timeout")

	got, err = lifecycleTimeout(&driver.RunOptions{})
	require.NoError(t, err)
	assert.Zero(t, got)
}

func TestRefreshStoredLifecycleUsesCurrentProviderValues(t *testing.T) {
	c := newFakeCluster(t)
	c.k.agentConfig.ContainerTimeout = "7m"
	stored := daemonOptions(t, agentd.DaemonConfig{
		Timeout:        "1m",
		ShutdownAction: config.ShutdownActionStopContainer,
		Ssh:            agentd.SshConfig{User: "root"},
	})

	refreshed, err := c.k.refreshStoredLifecycle(stored)
	require.NoError(t, err)

	cfg, err := agentd.DecodeDaemonConfig(refreshed.Env["DEVSY_WORKSPACE_DAEMON_CONFIG"])
	require.NoError(t, err)
	assert.Equal(t, "7m0s", cfg.Timeout)
	assert.True(t, cfg.Lifecycle.Managed())
	assert.Equal(t, "root", cfg.Ssh.User)
	oldCfg, err := agentd.DecodeDaemonConfig(stored.Env["DEVSY_WORKSPACE_DAEMON_CONFIG"])
	require.NoError(t, err)
	assert.Equal(t, "1m", oldCfg.Timeout, "stored options are not mutated")
}

func TestRefreshStoredLifecycleLegacyAliasAndConflict(t *testing.T) {
	c := newFakeCluster(t)
	c.k.options.InactivityTimeout = "3m"

	refreshed, err := c.k.refreshStoredLifecycle(&driver.RunOptions{})
	require.NoError(t, err)
	cfg, err := agentd.DecodeDaemonConfig(refreshed.Env["DEVSY_WORKSPACE_DAEMON_CONFIG"])
	require.NoError(t, err)
	assert.Equal(t, "3m0s", cfg.Timeout)

	c.k.agentConfig.ContainerTimeout = "4m"
	_, err = c.k.refreshStoredLifecycle(&driver.RunOptions{})
	assert.ErrorContains(t, err, "conflicts")
}

func TestStoredLifecycleOwnershipFollowsDockerlessTopology(t *testing.T) {
	for _, dockerless := range []bool{false, true} {
		name := "ordinary image keeps managed ownership"
		if dockerless {
			name = "dockerless clears stale managed ownership"
		}
		t.Run(name, func(t *testing.T) {
			c := newFakeCluster(t)
			cfg := agentd.DaemonConfig{
				Timeout:        "1m",
				ShutdownAction: config.ShutdownActionStopContainer,
				Lifecycle: agentd.LifecycleConfig{
					Owner: agentd.LifecycleOwnerManagedDaemon, SetupArmsTimer: true,
				},
				Ssh: agentd.SshConfig{User: "vscode", Workdir: "/workspaces/app"},
			}
			cfg.Platform.AccessKey = "key"
			cfg.Platform.PlatformHost = "platform"
			cfg.Platform.WorkspaceHost = "workspace"
			stored := daemonOptions(t, cfg)
			stored.Dockerless = dockerless
			original := stored.Env["DEVSY_WORKSPACE_DAEMON_CONFIG"]

			refreshed, err := c.k.refreshStoredLifecycle(stored)
			require.NoError(t, err)
			got, err := agentd.DecodeDaemonConfig(refreshed.Env["DEVSY_WORKSPACE_DAEMON_CONFIG"])
			require.NoError(t, err)
			assert.Equal(t, !dockerless, got.Lifecycle.Managed())
			assert.Equal(t, !dockerless, got.Lifecycle.SetupArmsTimer)
			assert.Empty(t, got.Timeout)
			assert.Equal(t, cfg.Platform, got.Platform)
			assert.Equal(t, cfg.Ssh, got.Ssh)
			assert.Equal(t, cfg.ShutdownAction, got.ShutdownAction)
			assert.Equal(t, original, stored.Env["DEVSY_WORKSPACE_DAEMON_CONFIG"])
		})
	}
}

func TestDockerlessResumeRejectsEnabledTimeoutBeforeMutation(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "canonical"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			pvc := workspacePVC(t)
			info, err := json.Marshal(&DevContainerInfo{
				WorkspaceID: "ws",
				Options:     &driver.RunOptions{UID: testWorkspace, Dockerless: true},
			})
			require.NoError(t, err)
			pvc.Annotations[DevsyInfoAnnotation] = string(info)
			c := newFakeCluster(t, pvc)
			if legacy {
				c.k.options.InactivityTimeout = "1m"
			} else {
				c.k.agentConfig.ContainerTimeout = "1m"
			}

			err = c.k.RunDevContainer(context.Background(), "ws", nil)
			require.ErrorContains(t, err, "dockerless")
			for _, action := range c.cs.Actions() {
				assert.Equal(
					t,
					"get",
					action.GetVerb(),
					"invalid resume must not mutate cluster state",
				)
			}
		})
	}
}

func TestDockerlessStoredLifecycleHonorsShutdownNone(t *testing.T) {
	c := newFakeCluster(t)
	c.k.agentConfig.ContainerTimeout = "1m"
	stored := daemonOptions(t, agentd.DaemonConfig{
		ShutdownAction: config.ShutdownActionNone,
	})
	stored.Dockerless = true

	refreshed, err := c.k.refreshStoredLifecycle(stored)
	require.NoError(t, err)
	cfg, err := agentd.DecodeDaemonConfig(refreshed.Env["DEVSY_WORKSPACE_DAEMON_CONFIG"])
	require.NoError(t, err)
	assert.False(t, cfg.Lifecycle.Managed())
	assert.Empty(t, cfg.Timeout)
	assert.Equal(t, config.ShutdownActionNone, cfg.ShutdownAction)
}

func TestAdmittedPodWithInjectedContainerIsRemovedAndVolumeKept(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: testPodID, Namespace: testNamespace},
	}
	c := newFakeCluster(t, pvc)
	requested := c.pod(t, podSpec{uid: "u1", phase: corev1.PodPending})
	admitted := requested.DeepCopy()
	admitted.Spec.Containers = append(admitted.Spec.Containers, corev1.Container{Name: "injected"})
	c.putPod(t, admitted)

	err := c.k.rejectAdmittedPod(context.Background(), admitted, requested, time.Minute)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "injected")
	assert.False(t, c.podExists(t))
	_, getErr := c.cs.CoreV1().PersistentVolumeClaims(testNamespace).
		Get(context.Background(), testPodID, metav1.GetOptions{})
	assert.NoError(t, getErr)
}

func TestAdmittedPodAcceptedWhenTopologyHolds(t *testing.T) {
	c := newFakeCluster(t)
	pod := c.pod(t, podSpec{uid: "u1", phase: corev1.PodPending})
	c.putPod(t, pod)

	err := c.k.rejectAdmittedPod(context.Background(), pod, pod.DeepCopy(), time.Minute)

	require.NoError(t, err)
	assert.True(t, c.podExists(t))
}

func TestAdmittedPodWithRewrittenCommandIsRemovedWhenTimeoutEnabled(t *testing.T) {
	requested := newFakeCluster(t).pod(t, podSpec{uid: "u1", phase: corev1.PodPending})
	requested.Spec.Containers[0].Command = []string{"/bin/sh"}
	admitted := requested.DeepCopy()
	admitted.Spec.Containers[0].Command = []string{"/webhook/entrypoint"}

	t.Run("enabled", func(t *testing.T) {
		c := newFakeCluster(t)
		c.putPod(t, admitted)

		err := c.k.rejectAdmittedPod(context.Background(), admitted, requested, time.Minute)

		require.ErrorContains(t, err, "Devsy daemon as its main process")
		assert.False(t, c.podExists(t))
	})
	t.Run("disabled", func(t *testing.T) {
		c := newFakeCluster(t)
		c.putPod(t, admitted)

		require.NoError(t, c.k.rejectAdmittedPod(context.Background(), admitted, requested, 0))
		assert.True(t, c.podExists(t), "no stop contract is at stake without a timeout")
	})
}

func TestPendingPodWithDifferentLaunchRecipeIsReplaced(t *testing.T) {
	c := newFakeCluster(t)
	pod := c.pod(t, podSpec{uid: "u1", phase: corev1.PodPending})
	pod.Spec.Containers[0].Image = "registry.example/old:1"
	c.putPod(t, pod)
	options := &driver.RunOptions{Image: "registry.example/new:2"}

	reused, err := c.k.reconcileDesiredPod(context.Background(), pod, options)

	require.NoError(t, err)
	assert.False(t, reused, "the caller creates the requested pod")
	assert.False(t, c.podExists(t), "a stale pending pod must not be waited on")
}

func TestSameRunOptionsTreatsNilAndEmptyCollectionsAsEqual(t *testing.T) {
	const image = "registry.example/app:1"
	nilForm := &driver.RunOptions{Image: image}
	emptyForm := &driver.RunOptions{
		Image: image, Cmd: []string{}, Env: map[string]string{}, Labels: []string{},
	}

	assert.True(t, sameRunOptions(nilForm, emptyForm))
	assert.False(t, sameRunOptions(nilForm, &driver.RunOptions{Image: "other"}))
	assert.False(t, sameRunOptions(nilForm, &driver.RunOptions{Image: image, Cmd: []string{"x"}}))
	assert.True(t, sameRunOptions(nil, nil))
	assert.False(t, sameRunOptions(nilForm, nil))
}

func TestExplicitStopWaitsForPodToBeGone(t *testing.T) {
	c := newFakeCluster(t)
	c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: corev1.PodRunning}))

	require.NoError(t, c.k.StopDevContainer(context.Background(), "ws"))

	assert.False(t, c.podExists(t))
}

func TestWaitPodDeletedIgnoresAlreadyAbsentPod(t *testing.T) {
	c := newFakeCluster(t)

	require.NoError(t, c.k.waitPodDeleted(context.Background(), testPodID))
}

func TestWaitPodDeletedTimesOutWhenPodStaysTerminating(t *testing.T) {
	c := newFakeCluster(t)
	podDeleteTimeout = 50 * time.Millisecond
	c.putPod(t, c.pod(t, podSpec{uid: "u1", phase: corev1.PodRunning, deleting: true}))
	c.cs.PrependReactor(
		"delete",
		podResourceName,
		func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, nil
		},
	)

	err := c.k.waitPodDeleted(context.Background(), testPodID)

	require.Error(t, err)
	assert.ErrorContains(t, err, "timeout waiting for pod")
	assert.True(t, c.podExists(t))
}

func runOptionsForWorkspace(timeout string) *driver.RunOptions {
	cfg, _ := agentd.EncodeDaemonConfig(&agentd.DaemonConfig{
		Timeout:        timeout,
		ShutdownAction: config.ShutdownActionStopContainer,
		Lifecycle:      agentd.LifecycleConfig{Owner: agentd.LifecycleOwnerManagedDaemon},
	})
	return &driver.RunOptions{
		UID:            testWorkspace,
		Image:          "example/image:1",
		Entrypoint:     "/bin/sh",
		Env:            map[string]string{"DEVSY_WORKSPACE_DAEMON_CONFIG": cfg},
		WorkspaceMount: &config.Mount{Type: "bind", Target: "/workspaces/app"},
	}
}

func readyOnCreate(c *fakeCluster, uid types.UID) {
	nextUID := uid
	c.cs.PrependReactor(
		"create",
		podResourceName,
		func(a clienttesting.Action) (bool, runtime.Object, error) {
			pod := a.(clienttesting.CreateAction).GetObject().(*corev1.Pod)
			pod.UID = nextUID
			nextUID += "-next"
			pod.Status.Phase = corev1.PodRunning
			pod.Status.ContainerStatuses = nil
			for _, container := range pod.Spec.Containers {
				pod.Status.ContainerStatuses = append(
					pod.Status.ContainerStatuses,
					corev1.ContainerStatus{
						Name: container.Name, Ready: true,
						State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
					},
				)
			}
			return false, nil, nil
		},
	)
}

func TestRunContainerRecreatesTerminalPodAndKeepsVolume(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: testPodID, Namespace: testNamespace, UID: "pvc-uid"},
	}
	c := newFakeCluster(t, pvc)
	c.putPod(t, c.pod(t, podSpec{uid: "stopped", phase: corev1.PodSucceeded}))
	readyOnCreate(c, "fresh")

	err := c.k.runContainer(context.Background(), testPodID, runOptionsForWorkspace("1m"), false)

	require.NoError(t, err)
	pod, err := c.cs.CoreV1().
		Pods(testNamespace).
		Get(context.Background(), testPodID, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, types.UID("fresh"), pod.UID, "a new pod replaced the terminal one")
	assert.Equal(t, corev1.RestartPolicyNever, pod.Spec.RestartPolicy)
	gotPVC, err := c.cs.CoreV1().PersistentVolumeClaims(testNamespace).
		Get(context.Background(), testPodID, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, types.UID("pvc-uid"), gotPVC.UID)
	for _, a := range c.cs.Actions() {
		assert.False(t, a.Matches("delete", "persistentvolumeclaims"))
	}
}

func TestRunContainerRejectsExtraContainersBeforeTouchingTheCluster(t *testing.T) {
	c := newFakeCluster(t)
	template := t.TempDir() + "/pod.yaml"
	require.NoError(
		t,
		writeFile(template, "spec:\n  containers:\n  - name: istio-proxy\n    image: proxy\n"),
	)
	c.k.options.PodManifestTemplate = template

	err := c.k.runContainer(context.Background(), testPodID, runOptionsForWorkspace("1m"), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "istio-proxy")
	for _, a := range c.cs.Actions() {
		assert.False(t,
			a.Matches("create", podResourceName) ||
				a.Matches("delete", podResourceName) ||
				a.Matches("create", "secrets"),
			"nothing is created or deleted: %v", a,
		)
	}
}

func TestRunContainerAllowsExtraContainersWhenTimeoutDisabled(t *testing.T) {
	c := newFakeCluster(t)
	template := t.TempDir() + "/pod.yaml"
	require.NoError(
		t,
		writeFile(template, "spec:\n  containers:\n  - name: istio-proxy\n    image: proxy\n"),
	)
	c.k.options.PodManifestTemplate = template
	readyOnCreate(c, "fresh")

	err := c.k.runContainer(context.Background(), testPodID, runOptionsForWorkspace(""), false)

	assert.NoError(t, err)
}

func TestRunContainerRemovesPodWhoseAdmittedTopologyBreaksTheContract(t *testing.T) {
	c := newFakeCluster(t)
	c.cs.PrependReactor(
		"create",
		podResourceName,
		func(a clienttesting.Action) (bool, runtime.Object, error) {
			pod := a.(clienttesting.CreateAction).GetObject().(*corev1.Pod)
			pod.UID = "admitted"
			pod.Spec.Containers = append(
				pod.Spec.Containers,
				corev1.Container{Name: "webhook-injected"},
			)
			return false, nil, nil
		},
	)

	err := c.k.runContainer(context.Background(), testPodID, runOptionsForWorkspace("1m"), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook-injected")
	assert.False(t, c.podExists(t))
}

func TestDockerlessSuppressorsSurviveRepeatAndResume(t *testing.T) {
	for _, disableDaemon := range []bool{false, true} {
		name := "shutdown none"
		if disableDaemon {
			name = "current disable-daemon flag"
		}
		t.Run(name, func(t *testing.T) {
			c := newFakeCluster(t)
			c.k.agentConfig.ContainerTimeout = "1m"
			c.k.disableDaemon = disableDaemon
			options := topologyRunOptions(t, true)
			action := config.ShutdownActionStopContainer
			if !disableDaemon {
				action = config.ShutdownActionNone
			}
			options.Env = daemonOptions(t, agentd.DaemonConfig{
				ShutdownAction: action,
			}).Env
			readyOnCreate(c, "initial")
			ctx := context.Background()

			require.NoError(t, c.k.RunDevContainer(ctx, "ws", options))
			require.NoError(t, c.k.RunDevContainer(ctx, "ws", nil))
			assertRunningTopology(t, c, options)
			require.NoError(t, c.k.StopDevContainer(ctx, "ws"))
			require.NoError(t, c.k.StartDevContainer(ctx, "ws"))
			assertRunningTopology(t, c, options)

			c.k.disableDaemon = false
			c.k.SetLifecycleShutdownAction(config.ShutdownActionStopContainer)
			c.cs.ClearActions()
			err := c.k.RunDevContainer(ctx, "ws", nil)
			require.ErrorContains(t, err, "dockerless")
			assertOnlyReads(t, c)
		})
	}
}

func TestDockerlessRejectsNewPositivePolicyBeforeResumeMutation(t *testing.T) {
	c := newFakeCluster(t)
	options := topologyRunOptions(t, true)
	readyOnCreate(c, "initial")
	ctx := context.Background()
	require.NoError(t, c.k.RunDevContainer(ctx, "ws", options))
	require.NoError(t, c.k.StopDevContainer(ctx, "ws"))
	c.k.agentConfig.ContainerTimeout = "1m"
	c.cs.ClearActions()

	err := c.k.StartDevContainer(ctx, "ws")

	require.ErrorContains(t, err, "dockerless")
	assertOnlyReads(t, c)
}

func TestExternalImageMainStaysUnmanagedAcrossResume(t *testing.T) {
	c := newFakeCluster(t)
	options := runOptionsForWorkspace("")
	options.ImageEntrypoint = true
	options.Cmd = []string{"-c", `exec "$@"`, "-", "/app"}
	options.Env = daemonOptions(t, agentd.DaemonConfig{
		ShutdownAction: config.ShutdownActionStopContainer,
	}).Env
	ctx := context.Background()
	readyOnCreate(c, "external")
	require.NoError(t, c.k.RunDevContainer(ctx, "ws", options))
	require.NoError(t, c.k.RunDevContainer(ctx, "ws", nil))
	assertRunningTopology(t, c, options)
	require.NoError(t, c.k.StopDevContainer(ctx, "ws"))
	require.NoError(t, c.k.StartDevContainer(ctx, "ws"))
	assertRunningTopology(t, c, options)
	c.k.agentConfig.ContainerTimeout = "1m"
	c.cs.ClearActions()

	err := c.k.RunDevContainer(ctx, "ws", nil)

	require.ErrorContains(t, err, "overrideCommand")
	assertOnlyReads(t, c)
}

func TestLegacyDockerlessResumeDoesNotRequireGenerationMetadata(t *testing.T) {
	options := topologyRunOptions(t, true)
	options.Env = map[string]string{"DOCKERLESS": pkgconfig.BoolTrue}
	pvc := workspacePVC(t)
	pvc.Annotations["customer.example/annotation"] = preservedCustomerMetadata
	raw, err := json.Marshal(&DevContainerInfo{WorkspaceID: "ws", Options: options})
	require.NoError(t, err)
	pvc.Annotations[DevsyInfoAnnotation] = string(raw)
	c := newFakeCluster(t, pvc)
	c.putPod(t, c.pod(t, podSpec{uid: "legacy", phase: corev1.PodSucceeded}))
	readyOnCreate(c, "resumed")

	require.NoError(t, c.k.RunDevContainer(context.Background(), "ws", nil))

	assertRunningTopology(t, c, options)
	kept, err := c.cs.CoreV1().PersistentVolumeClaims(testNamespace).
		Get(context.Background(), testPodID, metav1.GetOptions{})
	require.NoError(t, err)
	assertPreservedVolume(t, pvc, kept)
}

func TestCurrentShutdownActionOverridesStoredNoneOnResume(t *testing.T) {
	c := newFakeCluster(t)
	c.k.agentConfig.ContainerTimeout = "1m"
	options := runOptionsForWorkspace("")
	options.Env = daemonOptions(t, agentd.DaemonConfig{
		ShutdownAction: config.ShutdownActionNone,
		Lifecycle:      agentd.LifecycleConfig{Owner: agentd.LifecycleOwnerManagedDaemon},
	}).Env
	ctx := context.Background()
	readyOnCreate(c, "disabled")
	require.NoError(t, c.k.RunDevContainer(ctx, "ws", options))
	require.NoError(t, c.k.StopDevContainer(ctx, "ws"))
	c.k.SetLifecycleShutdownAction(config.ShutdownActionStopContainer)

	require.NoError(t, c.k.StartDevContainer(ctx, "ws"))

	secret, err := c.cs.CoreV1().Secrets(testNamespace).
		Get(ctx, getDaemonSecretName(testPodID), metav1.GetOptions{})
	require.NoError(t, err)
	cfg, err := agentd.DecodeDaemonConfig(string(secret.Data[DaemonConfigKey]))
	require.NoError(t, err)
	timeout, err := lifecycleTimeout(daemonOptions(t, *cfg))
	require.NoError(t, err)
	assert.Equal(t, time.Minute, timeout)
	assert.Equal(t, config.ShutdownActionStopContainer, cfg.ShutdownAction)
}

func topologyRunOptions(t *testing.T, dockerless bool) *driver.RunOptions {
	t.Helper()
	options := runOptionsForWorkspace("")
	options.Dockerless = dockerless
	if dockerless {
		options.Image = "example/dockerless:1"
		options.Entrypoint = "/.dockerless/dockerless"
		options.Cmd = []string{"start", "--wait"}
		options.Env = daemonOptions(t, agentd.DaemonConfig{
			ShutdownAction: config.ShutdownActionStopContainer,
		}).Env
	}
	return options
}

func assertRunningTopology(t *testing.T, c *fakeCluster, options *driver.RunOptions) {
	t.Helper()
	ctx := context.Background()
	details, err := c.k.FindDevContainer(ctx, "ws")
	require.NoError(t, err)
	assert.Equal(t, options.Dockerless, details.Dockerless)
	assert.Equal(t, options.ImageEntrypoint, details.ImageEntrypoint)
	pod, err := c.cs.CoreV1().Pods(testNamespace).Get(ctx, testPodID, metav1.GetOptions{})
	require.NoError(t, err)
	var main *corev1.Container
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == DevContainerName {
			main = &pod.Spec.Containers[i]
		}
	}
	require.NotNil(t, main)
	assert.Equal(t, options.Image, main.Image)
	assert.Equal(t, []string{options.Entrypoint}, main.Command)
	assert.Equal(t, options.Cmd, main.Args)
	secret, err := c.cs.CoreV1().Secrets(testNamespace).
		Get(ctx, getDaemonSecretName(testPodID), metav1.GetOptions{})
	require.NoError(t, err)
	cfg, err := agentd.DecodeDaemonConfig(string(secret.Data[DaemonConfigKey]))
	require.NoError(t, err)
	assert.Equal(t, !options.Dockerless && !options.ImageEntrypoint, cfg.Lifecycle.Managed())
	assert.Empty(t, cfg.Timeout)
}

func assertOnlyReads(t *testing.T, c *fakeCluster) {
	t.Helper()
	for _, action := range c.cs.Actions() {
		assert.Equal(t, "get", action.GetVerb(), "policy rejection must not mutate cluster state")
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func credentialHelperMarker(t *testing.T) string {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("credential helper spy requires a Unix shell")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	helper := filepath.Join(dir, "docker-credential-devsyspy")
	script := "#!/bin/sh\nprintf called > \"$DEVSY_TEST_CREDENTIAL_MARKER\"\n" +
		"printf '{\"Username\":\"test\",\"Secret\":\"test\"}\\n'\n"
	require.NoError(t, (runenv.OsFileStore{}).WriteFile(helper, []byte(script), 0o700))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"credsStore":"devsyspy"}`), 0o600),
	)
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("DEVSY_TEST_CREDENTIAL_MARKER", marker)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := dockercredentials.GetAuthConfig("registry.example.com")
	require.NoError(t, err)
	require.FileExists(t, marker)
	require.NoError(t, os.Remove(marker))
	return marker
}

func TestImageConfigPreservesClusterPullCredentials(t *testing.T) {
	marker := credentialHelperMarker(t)
	const adminSecret = "admin-pull"
	account := &corev1.ServiceAccount{
		ObjectMeta:       metav1.ObjectMeta{Name: "cluster-account", Namespace: testNamespace},
		ImagePullSecrets: []corev1.LocalObjectReference{{Name: adminSecret}},
	}
	c := newFakeCluster(t, account)
	c.k.options.ImageConfigFile = "/local/image.json"
	c.k.options.ServiceAccount = account.Name
	c.k.options.KubernetesPullSecretsEnabled = pkgconfig.BoolFalse
	c.k.agentConfig.InjectDockerCredentials = pkgconfig.BoolFalse
	template := filepath.Join(t.TempDir(), "pod.yaml")
	require.NoError(t, writeFile(template, "spec:\n  imagePullSecrets:\n  - name: template-pull\n"))
	c.k.options.PodManifestTemplate = template
	reads := 0
	c.cs.PrependReactor(
		"get",
		"secrets",
		func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.(clienttesting.GetAction).GetName() != adminSecret {
				return false, nil, nil
			}
			reads++
			return true, nil, kerrors.NewForbidden(
				schema.GroupResource{Resource: "secrets"},
				adminSecret,
				errors.New("denied"),
			)
		},
	)
	options := runOptionsForWorkspace("1m")
	options.Image = testImage
	pod, err := c.k.buildPod(context.Background(), testPodID, options, false)
	require.NoError(t, err)
	assert.Equal(t, account.Name, pod.Spec.ServiceAccountName)
	assert.Equal(
		t,
		[]corev1.LocalObjectReference{{Name: "template-pull"}},
		pod.Spec.ImagePullSecrets,
	)
	saved, err := c.cs.CoreV1().ServiceAccounts(testNamespace).
		Get(context.Background(), account.Name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, account.ImagePullSecrets, saved.ImagePullSecrets)
	assert.Zero(t, reads)
	assert.NoFileExists(t, marker)
}
