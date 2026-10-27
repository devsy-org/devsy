package platform

import (
	"testing"

	agentstoragev1 "github.com/devsy-org/agentapi/pkg/apis/devsy/storage/v1"
	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
)

type instanceReadinessSuite struct {
	suite.Suite
}

func TestInstanceReadinessSuite(t *testing.T) {
	suite.Run(t, new(instanceReadinessSuite))
}

func newInstanceWithPhase(phase storagev1.InstancePhase) *managementv1.DevsyWorkspaceInstance {
	instance := &managementv1.DevsyWorkspaceInstance{}
	instance.Status.Phase = phase
	return instance
}

func templateResolvedCondition(status corev1.ConditionStatus) agentstoragev1.Condition {
	return agentstoragev1.Condition{
		Type:   storagev1.InstanceTemplateResolved,
		Status: status,
	}
}

func (s *instanceReadinessSuite) TestIsReadyTreatsSleepingAsReady() {
	s.True(isReady(newInstanceWithPhase(storagev1.InstanceSleeping)))
	s.True(isReady(newInstanceWithPhase(storagev1.InstanceReady)))
}

func (s *instanceReadinessSuite) TestIsReadyRejectsOtherPhases() {
	s.False(isReady(newInstanceWithPhase(storagev1.InstancePending)))
	s.False(isReady(newInstanceWithPhase("")))
}

func (s *instanceReadinessSuite) TestIsTemplateSyncedRequiresResolvedTrueCondition() {
	instance := newInstanceWithPhase(storagev1.InstanceReady)
	s.False(isTemplateSynced(instance), "no conditions must not count as synced")

	instance.Status.Conditions = agentstoragev1.Conditions{
		templateResolvedCondition(corev1.ConditionFalse),
	}
	s.False(isTemplateSynced(instance))

	instance.Status.Conditions = agentstoragev1.Conditions{
		templateResolvedCondition(corev1.ConditionTrue),
	}
	s.True(isTemplateSynced(instance))
}

func (s *instanceReadinessSuite) TestIsTemplateSyncedIgnoresUnrelatedConditions() {
	instance := newInstanceWithPhase(storagev1.InstanceReady)
	instance.Status.Conditions = agentstoragev1.Conditions{
		{Type: "SomethingElse", Status: corev1.ConditionTrue},
	}
	s.False(isTemplateSynced(instance))
}

func (s *instanceReadinessSuite) TestIsTemplateSyncedSyncOnceOverridesResolvedCondition() {
	instance := newInstanceWithPhase(storagev1.InstanceReady)
	instance.Spec.TemplateRef = &storagev1.TemplateRef{SyncOnce: true}
	instance.Status.Conditions = agentstoragev1.Conditions{
		templateResolvedCondition(corev1.ConditionTrue),
	}
	s.False(
		isTemplateSynced(instance),
		"SyncOnce must hold the instance unsynced until the controller clears it",
	)
}

func (s *instanceReadinessSuite) TestInstanceReadyRequiresPhaseAndTemplateSync() {
	pending := newInstanceWithPhase(storagev1.InstancePending)
	pending.Status.Conditions = agentstoragev1.Conditions{
		templateResolvedCondition(corev1.ConditionTrue),
	}
	s.False(
		instanceReady(pending),
		"pending phase must block readiness even when template is synced",
	)

	instance := newInstanceWithPhase(storagev1.InstanceReady)
	s.False(instanceReady(instance), "ready phase alone is not enough without template sync")

	instance.Status.Conditions = agentstoragev1.Conditions{
		templateResolvedCondition(corev1.ConditionTrue),
	}
	s.True(instanceReady(instance))
}
