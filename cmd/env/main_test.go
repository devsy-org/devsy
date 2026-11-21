package env

import (
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets/secretstest"
)

func TestMain(m *testing.M) {
	os.Exit(secretstest.Run(m))
}
