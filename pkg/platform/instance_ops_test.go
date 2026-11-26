package platform

import (
	"context"
	"errors"
	"testing"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	devsyclient "github.com/devsy-org/api/pkg/clientset/versioned"
	devsyfake "github.com/devsy-org/api/pkg/clientset/versioned/fake"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/kube"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

type instanceOpsSuite struct {
	suite.Suite
}

func TestInstanceOpsSuite(t *testing.T) {
	suite.Run(t, new(instanceOpsSuite))
}

func (s *instanceOpsSuite) TestSetTemplateSyncOnceSetsFlagWhenTemplateRefPresent() {
	instance := &managementv1.DevsyWorkspaceInstance{}
	instance.Spec.TemplateRef = &storagev1.TemplateRef{}

	setTemplateSyncOnce(instance)

	s.True(instance.Spec.TemplateRef.SyncOnce)
}

func (s *instanceOpsSuite) TestSetTemplateSyncOnceLeavesInstanceWithoutTemplateRefUnchanged() {
	instance := &managementv1.DevsyWorkspaceInstance{}

	setTemplateSyncOnce(instance)

	s.Nil(instance.Spec.TemplateRef)
}

func (s *instanceOpsSuite) TestCreateInstanceWrapsCreateError() {
	createErr := errors.New("boom")
	devsy := devsyfake.NewSimpleClientset()
	devsy.PrependReactor(
		"create",
		"devsyworkspaceinstances",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, createErr
		},
	)
	pc := &instanceOpsPlatformClient{management: &instanceOpsKubeClient{devsy: devsy}}
	instance := &managementv1.DevsyWorkspaceInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "ws", Namespace: "p-default"},
	}

	_, err := CreateInstance(context.Background(), pc, instance)

	s.ErrorIs(err, createErr)
	s.EqualError(err, "create workspace instance: boom")
}

// instanceOpsKubeClient provides only Devsy(); other kube.Interface methods are unused by these tests.
type instanceOpsKubeClient struct {
	kube.Interface
	devsy devsyclient.Interface
}

func (c *instanceOpsKubeClient) Devsy() devsyclient.Interface {
	return c.devsy
}

// instanceOpsPlatformClient provides only Management(); other client.Client methods are unused by these tests.
type instanceOpsPlatformClient struct {
	client.Client
	management kube.Interface
}

func (c *instanceOpsPlatformClient) Management() (kube.Interface, error) {
	return c.management, nil
}
