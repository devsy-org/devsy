package framework

import (
	"os"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverrideEnvRestoresUnsetAfterSet(t *testing.T) {
	const name = "DEVSY_E2E_TEST_ENV_UNSET_THEN_SET"
	require.NoError(t, os.Unsetenv(name))

	restore, err := overrideEnv(name, new("value"))
	require.NoError(t, err)
	assert.Equal(t, "value", os.Getenv(name))

	restore()
	_, present := os.LookupEnv(name)
	assert.False(t, present, "unset prior state must come back as unset, not empty")
}

func TestOverrideEnvRestoresEmptyAfterSet(t *testing.T) {
	const name = "DEVSY_E2E_TEST_ENV_EMPTY_THEN_SET"
	t.Setenv(name, "")

	restore, err := overrideEnv(name, new("value"))
	require.NoError(t, err)
	assert.Equal(t, "value", os.Getenv(name))

	restore()
	got, present := os.LookupEnv(name)
	assert.True(t, present, "set-to-empty prior state must stay set")
	assert.Empty(t, got)
}

func TestOverrideEnvRestoresPreviousValue(t *testing.T) {
	const name = "DEVSY_E2E_TEST_ENV_VALUE_THEN_SET"
	t.Setenv(name, "original")

	restore, err := overrideEnv(name, new("replacement"))
	require.NoError(t, err)
	assert.Equal(t, "replacement", os.Getenv(name))

	restore()
	assert.Equal(t, "original", os.Getenv(name))
}

func TestOverrideEnvUnsetRestoresPreviousValue(t *testing.T) {
	const name = "DEVSY_E2E_TEST_ENV_VALUE_THEN_UNSET"
	t.Setenv(name, "original")

	restore, err := overrideEnv(name, nil)
	require.NoError(t, err)
	_, present := os.LookupEnv(name)
	assert.False(t, present, "nil value must unset the variable")

	restore()
	assert.Equal(t, "original", os.Getenv(name))
}

func TestEnvSpecHelpers(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "framework env helpers")
}

var _ = ginkgo.Describe("scoped environment helpers", ginkgo.Ordered, func() {
	const (
		setName   = "DEVSY_E2E_TEST_SPEC_ENV_SET_THEN_RESTORE"
		unsetName = "DEVSY_E2E_TEST_SPEC_ENV_VALUE_THEN_RESTORE"
	)

	ginkgo.BeforeAll(func() {
		ginkgo.DeferCleanup(os.Unsetenv, setName)
		ginkgo.DeferCleanup(os.Unsetenv, unsetName)
		gomega.Expect(os.Unsetenv(setName)).To(gomega.Succeed())
		gomega.Expect(os.Setenv(unsetName, "original")).To(gomega.Succeed())
	})

	ginkgo.It("sets the variable for the spec", func() {
		SetEnvForSpec(setName, "scoped")
		gomega.Expect(os.Getenv(setName)).To(gomega.Equal("scoped"))
	})

	ginkgo.It("restores the unset prior state after the previous spec", func() {
		_, present := os.LookupEnv(setName)
		gomega.Expect(present).To(gomega.BeFalse())
	})

	ginkgo.It("unsets the variable for the spec", func() {
		UnsetEnvForSpec(unsetName)
		_, present := os.LookupEnv(unsetName)
		gomega.Expect(present).To(gomega.BeFalse())
	})

	ginkgo.It("restores the prior value after the previous spec", func() {
		got, present := os.LookupEnv(unsetName)
		gomega.Expect(present).To(gomega.BeTrue())
		gomega.Expect(got).To(gomega.Equal("original"))
	})
})
