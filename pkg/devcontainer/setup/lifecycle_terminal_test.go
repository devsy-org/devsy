package setup

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/suite"
)

type interactiveValidationSuite struct{ suite.Suite }

func TestInteractiveValidationSuite(t *testing.T) { suite.Run(t, new(interactiveValidationSuite)) }

func (s *interactiveValidationSuite) TestValidation() {
	s.NoError(ValidateInteractivePostStart(nil))
	s.NoError(
		ValidateInteractivePostStart(
			[]types.LifecycleHook{{"a": {"echo"}, "b": nil}, {"c": {"true"}}},
		),
	)
	err := ValidateInteractivePostStart([]types.LifecycleHook{nil, {"a": {"echo"}, "b": {"true"}}})
	s.ErrorContains(err, "postStartCommand")
	s.ErrorContains(err, "1")
}
