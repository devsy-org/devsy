package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clienttesting "k8s.io/client-go/testing"
)

const (
	parentMountRecreateOperation = "recreate"
	parentMountReuseOperation    = "reuse"
)

var pvcResource = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}

func canonicalTopologyOptions(t *testing.T, dockerless bool) *driver.RunOptions {
	t.Helper()
	options := topologyRunOptions(t, dockerless)
	if !dockerless {
		options.Env = daemonOptions(t, agentd.DaemonConfig{
			ShutdownAction: config.ShutdownActionStopContainer,
			Lifecycle: agentd.LifecycleConfig{
				Owner: agentd.LifecycleOwnerManagedDaemon, SetupArmsTimer: true,
			},
		}).Env
	}
	return options
}

func savedRunVolume(t *testing.T, options *driver.RunOptions) *corev1.PersistentVolumeClaim {
	t.Helper()
	pvc := workspacePVC(t)
	pvc.UID, pvc.ResourceVersion = "workspace-volume", "4"
	pvc.Labels = map[string]string{
		DevsyWorkspaceUIDLabel:   testWorkspace,
		"customer.example/label": preservedCustomerMetadata,
	}
	pvc.Annotations["customer.example/annotation"] = preservedCustomerMetadata
	pvc.Spec.VolumeName = "customer-volume"
	pvc.Finalizers = []string{"kubernetes.io/pvc-protection"}
	raw, err := json.Marshal(&DevContainerInfo{WorkspaceID: "ws", Options: options})
	require.NoError(t, err)
	pvc.Annotations[DevsyInfoAnnotation] = string(raw)
	return pvc
}

func assertPreservedVolume(t *testing.T, original, current *corev1.PersistentVolumeClaim) {
	t.Helper()
	assert.Equal(t, original.UID, current.UID)
	assert.Equal(t, original.Labels, current.Labels)
	assert.Equal(t, original.Spec, current.Spec)
	assert.Equal(t, original.Finalizers, current.Finalizers)
	assert.Equal(
		t,
		original.Annotations["customer.example/annotation"],
		current.Annotations["customer.example/annotation"],
	)
}

func TestRecreatedTopologyControlsRepeatAndResume(t *testing.T) {
	for _, dockerless := range []bool{false, true} {
		name := "ordinary to dockerless"
		if !dockerless {
			name = "dockerless to ordinary"
		}
		t.Run(name, func(t *testing.T) {
			initial := canonicalTopologyOptions(t, !dockerless)
			volume := savedRunVolume(t, initial)
			c := newFakeCluster(t, volume)
			ctx := context.Background()
			readyOnCreate(c, "initial")
			require.NoError(t, c.k.RunDevContainer(ctx, "ws", initial))
			require.NoError(t, c.k.StopDevContainer(ctx, "ws"))
			next := canonicalTopologyOptions(t, dockerless)
			require.NoError(t, c.k.RunDevContainer(ctx, "ws", next))
			assertRunningTopology(t, c, next)
			require.NoError(t, c.k.RunDevContainer(ctx, "ws", nil))
			assertRunningTopology(t, c, next)
			require.NoError(t, c.k.StopDevContainer(ctx, "ws"))
			require.NoError(t, c.k.StartDevContainer(ctx, "ws"))
			assertRunningTopology(t, c, next)
			current, info, err := c.k.getDevContainerPvc(ctx, testPodID)
			require.NoError(t, err)
			assertPreservedVolume(t, volume, current)
			assert.Equal(t, next, info.Options)
		})
	}
}

func TestUnchangedRunOptionsDoNotRequireVolumeUpdate(t *testing.T) {
	options := canonicalTopologyOptions(t, false)
	c := newFakeCluster(t, savedRunVolume(t, options))
	c.cs.PrependReactor(
		"update",
		"persistentvolumeclaims",
		func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, kerrors.NewForbidden(
				pvcResource.GroupResource(),
				testPodID,
				errors.New("update denied"),
			)
		},
	)
	readyOnCreate(c, "stable")
	ctx := context.Background()
	require.NoError(t, c.k.RunDevContainer(ctx, "ws", nil))
	first, err := c.k.getPod(ctx, testPodID)
	require.NoError(t, err)
	require.NoError(t, c.k.RunDevContainer(ctx, "ws", nil))
	reused, err := c.k.getPod(ctx, testPodID)
	require.NoError(t, err)
	assert.Equal(t, first.UID, reused.UID)
	require.NoError(t, c.k.StopDevContainer(ctx, "ws"))
	require.NoError(t, c.k.StartDevContainer(ctx, "ws"))
	resumed, err := c.k.getPod(ctx, testPodID)
	require.NoError(t, err)
	assert.NotEqual(t, first.UID, resumed.UID)
	assertRunningTopology(t, c, options)
}

func TestVolumePublicationErrorsLeavePreviousRecordAndNoNewPod(t *testing.T) {
	for _, cause := range []error{
		kerrors.NewForbidden(pvcResource.GroupResource(), testPodID, errors.New("update denied")),
		kerrors.NewConflict(pvcResource.GroupResource(), testPodID, errors.New("concurrent owner")),
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			old := savedRunVolume(t, canonicalTopologyOptions(t, false))
			c := newFakeCluster(t, old)
			c.cs.PrependReactor(
				"update",
				"persistentvolumeclaims",
				func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, cause
				},
			)
			err := c.k.RunDevContainer(
				context.Background(),
				"ws",
				canonicalTopologyOptions(t, true),
			)
			require.ErrorIs(t, err, cause)
			if kerrors.IsForbidden(cause) {
				assert.ErrorContains(t, err, "permission update")
			}
			current, _, getErr := c.k.getDevContainerPvc(context.Background(), testPodID)
			require.NoError(t, getErr)
			assert.Equal(t, old, current)
			for _, action := range c.cs.Actions() {
				if action.GetVerb() != "get" {
					assert.True(
						t,
						action.Matches("update", "persistentvolumeclaims"),
						"no pod/config changes after failed publication: %v",
						action,
					)
				}
			}
		})
	}
}

func TestFailedPodCreationRetainsNewDesiredOptionsForRetry(t *testing.T) {
	old := savedRunVolume(t, canonicalTopologyOptions(t, false))
	c := newFakeCluster(t, old)
	next := canonicalTopologyOptions(t, true)
	readyOnCreate(c, "retried")
	reject := true
	c.cs.PrependReactor(
		"create",
		podResourceName,
		func(clienttesting.Action) (bool, runtime.Object, error) {
			if reject {
				reject = false
				return true, nil, errors.New("pod admission unavailable")
			}
			return false, nil, nil
		},
	)
	ctx := context.Background()
	require.ErrorContains(t, c.k.RunDevContainer(ctx, "ws", next), "pod admission unavailable")
	current, info, err := c.k.getDevContainerPvc(ctx, testPodID)
	require.NoError(t, err)
	assertPreservedVolume(t, old, current)
	assert.Equal(t, next, info.Options)
	assert.False(t, c.podExists(t))
	c.cs.ClearActions()

	require.NoError(t, c.k.RunDevContainer(ctx, "ws", nil))

	assertRunningTopology(t, c, next)
	for _, action := range c.cs.Actions() {
		assert.False(
			t,
			action.Matches("update", "persistentvolumeclaims"),
			"retry must use the published desired state",
		)
	}
}

func TestStaleVolumeSnapshotCannotOverwriteReplacedVolume(t *testing.T) {
	old := savedRunVolume(t, canonicalTopologyOptions(t, false))
	c := newFakeCluster(t, old)
	stale, info, err := c.k.getDevContainerPvc(context.Background(), testPodID)
	require.NoError(t, err)
	replacement := old.DeepCopy()
	replacement.UID, replacement.ResourceVersion = "replacement-volume", "5"
	require.NoError(t, c.cs.Tracker().Update(pvcResource, replacement, testNamespace))
	c.cs.PrependReactor(
		"update",
		"persistentvolumeclaims",
		func(a clienttesting.Action) (bool, runtime.Object, error) {
			requested := a.(clienttesting.UpdateAction).GetObject().(*corev1.PersistentVolumeClaim)
			if requested.UID != replacement.UID ||
				requested.ResourceVersion != replacement.ResourceVersion {
				return true, nil, kerrors.NewConflict(
					pvcResource.GroupResource(),
					testPodID,
					errors.New("volume changed"),
				)
			}
			return false, nil, nil
		},
	)

	err = c.k.persistRunOptions(
		context.Background(),
		stale,
		info,
		canonicalTopologyOptions(t, true),
	)

	require.True(t, kerrors.IsConflict(err))
	current, _, err := c.k.getDevContainerPvc(context.Background(), testPodID)
	require.NoError(t, err)
	assert.Equal(t, replacement, current)
}

func TestForeignWorkspaceVolumeIsNotUpdated(t *testing.T) {
	old := savedRunVolume(t, canonicalTopologyOptions(t, false))
	old.Labels[DevsyWorkspaceUIDLabel] = "another-workspace"
	c := newFakeCluster(t, old)

	err := c.k.RunDevContainer(context.Background(), "ws", canonicalTopologyOptions(t, true))

	require.ErrorContains(t, err, "different workspace")
	assertOnlyReads(t, c)
}

func TestLivePodTopologyOverridesPendingDesiredRecord(t *testing.T) {
	old := canonicalTopologyOptions(t, false)
	c := newFakeCluster(t, savedRunVolume(t, old))
	ctx := context.Background()
	readyOnCreate(c, "running")
	require.NoError(t, c.k.RunDevContainer(ctx, "ws", old))
	volume, info, err := c.k.getDevContainerPvc(ctx, testPodID)
	require.NoError(t, err)
	next := canonicalTopologyOptions(t, true)
	require.NoError(t, c.k.persistRunOptions(ctx, volume, info, next))

	details, err := c.k.FindDevContainer(ctx, "ws")

	require.NoError(t, err)
	assert.False(t, details.Dockerless, "the live pod is still the ordinary generation")
	require.NoError(t, c.k.RunDevContainer(ctx, "ws", nil))
	assertRunningTopology(t, c, next)
}

func TestForeignPodPreventsVolumeAndConfigMutation(t *testing.T) {
	c := newFakeCluster(t, savedRunVolume(t, canonicalTopologyOptions(t, false)))
	c.putPod(t, c.pod(t, podSpec{uid: "foreign", phase: corev1.PodRunning, foreign: true}))
	c.cs.ClearActions()

	err := c.k.RunDevContainer(context.Background(), "ws", canonicalTopologyOptions(t, true))

	require.Error(t, err)
	assertOnlyReads(t, c)
}

func TestLegacyRunningDockerlessPodIsReusedWithoutNewAnnotations(t *testing.T) {
	options := topologyRunOptions(t, true)
	options.Env = map[string]string{"DOCKERLESS": pkgconfig.BoolTrue}
	c := newFakeCluster(t, savedRunVolume(t, options))
	pod := c.pod(t, podSpec{uid: "legacy-running", phase: corev1.PodRunning})
	pod.Spec.Containers[0].Image = options.Image
	pod.Spec.Containers[0].Command = []string{options.Entrypoint}
	pod.Spec.Containers[0].Args = options.Cmd
	c.putPod(t, pod)
	ctx := context.Background()

	require.NoError(t, c.k.RunDevContainer(ctx, "ws", nil))

	current, err := c.k.getPod(ctx, testPodID)
	require.NoError(t, err)
	assert.Equal(t, pod.UID, current.UID)
	assertRunningTopology(t, c, options)
}

func TestMalformedLiveTopologyFailsClosedWithoutDeletingWork(t *testing.T) {
	options := canonicalTopologyOptions(t, false)
	c := newFakeCluster(t, savedRunVolume(t, options))
	readyOnCreate(c, "protected")
	ctx := context.Background()
	require.NoError(t, c.k.RunDevContainer(ctx, "ws", nil))
	pod, err := c.k.getPod(ctx, testPodID)
	require.NoError(t, err)
	pod.Annotations[DevsyDockerlessAnnotation] = "not-a-boolean"
	require.NoError(t, c.cs.Tracker().Update(
		schema.GroupVersionResource{Version: "v1", Resource: podResourceName}, pod, testNamespace,
	))

	err = c.k.RunDevContainer(ctx, "ws", nil)

	require.Error(t, err)
	current, getErr := c.k.getPod(ctx, testPodID)
	require.NoError(t, getErr)
	assert.Equal(t, pod.UID, current.UID)
}

func TestPositiveUnsupportedTopologyDoesNotPublishDesiredState(t *testing.T) {
	for _, dockerless := range []bool{false, true} {
		name := "external image main"
		if dockerless {
			name = "dockerless"
		}
		t.Run(name, func(t *testing.T) {
			volume := savedRunVolume(t, canonicalTopologyOptions(t, false))
			c := newFakeCluster(t, volume)
			c.k.agentConfig.ContainerTimeout = "1m"
			next := canonicalTopologyOptions(t, dockerless)
			if !dockerless {
				next.ImageEntrypoint = true
				next.Cmd = []string{"-c", `exec "$@"`, "-", "/app"}
			}

			err := c.k.RunDevContainer(context.Background(), "ws", next)

			require.Error(t, err)
			assertOnlyReads(t, c)
			current, _, getErr := c.k.getDevContainerPvc(context.Background(), testPodID)
			require.NoError(t, getErr)
			assert.Equal(t, volume, current)
		})
	}
}

func TestConcurrentUserMetadataConflictIsNotOverwritten(t *testing.T) {
	volume := savedRunVolume(t, canonicalTopologyOptions(t, false))
	c := newFakeCluster(t, volume)
	concurrent := volume.DeepCopy()
	concurrent.ResourceVersion = "5"
	concurrent.Labels["customer.example/new"] = "concurrent"
	c.cs.PrependReactor(
		"update",
		"persistentvolumeclaims",
		func(clienttesting.Action) (bool, runtime.Object, error) {
			err := c.cs.Tracker().Update(pvcResource, concurrent, testNamespace)
			if err != nil {
				return true, nil, err
			}
			return true, nil, kerrors.NewConflict(
				pvcResource.GroupResource(),
				testPodID,
				errors.New("concurrent metadata"),
			)
		},
	)

	err := c.k.RunDevContainer(context.Background(), "ws", canonicalTopologyOptions(t, true))

	require.True(t, kerrors.IsConflict(err))
	current, _, err := c.k.getDevContainerPvc(context.Background(), testPodID)
	require.NoError(t, err)
	assert.Equal(t, concurrent, current)
	assert.False(t, c.podExists(t))
}

func TestParentWorkspaceVolumeMountLifecycle(t *testing.T) {
	for _, operation := range []string{"initial", parentMountReuseOperation, parentMountRecreateOperation, "resume"} {
		t.Run(operation, func(t *testing.T) {
			options := canonicalTopologyOptions(t, false)
			c, volume := newParentMountLifecycleCluster(t, operation, options)
			ctx := context.Background()
			var err error
			switch operation {
			case "resume":
				err = c.k.StartDevContainer(ctx, "ws")
			case parentMountReuseOperation:
				err = c.k.RunDevContainer(ctx, "ws", nil)
			default:
				err = c.k.RunDevContainer(ctx, "ws", options)
			}
			require.NoError(t, err)
			assertParentMountedWorkspace(t, c, options.Image)
			pod, err := c.k.getPod(ctx, testPodID)
			require.NoError(t, err)
			if operation == parentMountReuseOperation {
				assert.Equal(t, types.UID("existing"), pod.UID)
			} else {
				assert.Equal(t, types.UID("replacement"), pod.UID)
			}
			current, _, err := c.k.getDevContainerPvc(ctx, testPodID)
			require.NoError(t, err)
			if volume != nil {
				assertPreservedVolume(t, volume, current)
			}
		})
	}
}

func newParentMountLifecycleCluster(
	t *testing.T,
	operation string,
	options *driver.RunOptions,
) (*fakeCluster, *corev1.PersistentVolumeClaim) {
	t.Helper()
	var volume *corev1.PersistentVolumeClaim
	var c *fakeCluster
	if operation == "initial" {
		c = newFakeCluster(t)
	} else {
		volume = savedRunVolume(t, options)
		c = newFakeCluster(t, volume)
	}
	c.k.options.WorkspaceVolumeMount = "/workspaces"
	if operation == parentMountReuseOperation || operation == parentMountRecreateOperation {
		pod := c.pod(t, podSpec{uid: "existing", phase: corev1.PodRunning})
		pod.Spec.Containers[0].Image = options.Image
		pod.Spec.Containers[0].Command = []string{options.Entrypoint}
		pod.Spec.Containers[0].Args = options.Cmd
		pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
			{Name: DevContainerName, MountPath: "/workspaces", SubPath: "devsy/0"},
		}
		c.putPod(t, pod)
	}
	readyOnCreate(c, "replacement")
	if operation == parentMountRecreateOperation {
		require.NoError(t, c.k.StopDevContainer(context.Background(), "ws"))
		options.Image = "example/recreated:2"
	}
	return c, volume
}

func assertParentMountedWorkspace(t *testing.T, c *fakeCluster, image string) {
	t.Helper()
	ctx := context.Background()
	pod, err := c.k.getPod(ctx, testPodID)
	require.NoError(t, err)
	var mounted *corev1.VolumeMount
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		if container.Name != DevContainerName {
			continue
		}
		assert.Equal(t, image, container.Image)
		for j := range container.VolumeMounts {
			if container.VolumeMounts[j].Name == DevContainerName {
				mounted = &container.VolumeMounts[j]
			}
		}
	}
	require.NotNil(t, mounted)
	assert.Equal(t, "/workspaces", mounted.MountPath)
	assert.Equal(t, "devsy/0", mounted.SubPath)
	_, info, err := c.k.getDevContainerPvc(ctx, testPodID)
	require.NoError(t, err)
	assert.Equal(t, "/workspaces/app", info.Options.WorkspaceMount.Target)
	assert.Equal(t, image, info.Options.Image)
}

func TestMalformedLiveTopologyRejectsProviderAndCommandDriftBeforeMutation(t *testing.T) {
	for _, drift := range []string{"provider options", "command", "partial topology"} {
		t.Run(drift, func(t *testing.T) {
			options := canonicalTopologyOptions(t, false)
			c := newFakeCluster(t, savedRunVolume(t, options))
			readyOnCreate(c, "protected")
			ctx := context.Background()
			require.NoError(t, c.k.RunDevContainer(ctx, "ws", nil))
			pod, err := c.k.getPod(ctx, testPodID)
			require.NoError(t, err)
			pod.Annotations[DevsyDockerlessAnnotation] = "not-a-boolean"
			if drift == "partial topology" {
				pod.Annotations[DevsyDockerlessAnnotation] = "false"
				delete(pod.Annotations, DevsyImageEntrypointAnnotation)
			}
			require.NoError(t, c.cs.Tracker().Update(
				schema.GroupVersionResource{
					Version:  "v1",
					Resource: podResourceName,
				}, pod, testNamespace,
			),
			)
			beforeVolume, _, err := c.k.getDevContainerPvc(ctx, testPodID)
			require.NoError(t, err)
			beforeSecret, err := c.cs.CoreV1().Secrets(testNamespace).
				Get(ctx, getDaemonSecretName(testPodID), metav1.GetOptions{})
			require.NoError(t, err)
			next := canonicalTopologyOptions(t, false)
			if drift == "command" {
				next.Cmd = []string{"-c", "exec sleep 3600"}
			} else {
				c.k.options.DiskSize = "2Gi"
			}
			c.cs.ClearActions()

			err = c.k.RunDevContainer(ctx, "ws", next)

			require.Error(t, err)
			assertOnlyReads(t, c)
			currentPod, getErr := c.k.getPod(ctx, testPodID)
			require.NoError(t, getErr)
			assert.Equal(t, pod, currentPod)
			currentVolume, _, getErr := c.k.getDevContainerPvc(ctx, testPodID)
			require.NoError(t, getErr)
			assert.Equal(t, beforeVolume, currentVolume)
			currentSecret, getErr := c.cs.CoreV1().Secrets(testNamespace).
				Get(ctx, getDaemonSecretName(testPodID), metav1.GetOptions{})
			require.NoError(t, getErr)
			assert.Equal(t, beforeSecret, currentSecret)
		})
	}
}
