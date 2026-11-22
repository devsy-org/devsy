package secretstest

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets/internal/scryptcost"
)

const WorkFactor = 10

func Run(m *testing.M) int {
	defer scryptcost.Set(WorkFactor)()
	return m.Run()
}
