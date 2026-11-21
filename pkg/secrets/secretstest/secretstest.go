package secretstest

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets/internal/scryptcost"
)

const fixtureWorkFactor = 10

func Run(m *testing.M) int {
	defer scryptcost.Set(fixtureWorkFactor)()
	return m.Run()
}
