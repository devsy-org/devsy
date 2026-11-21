package diagnosticredact

import (
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
)

func TestRecordsHandleManyBlankRecordsInLinearTime(t *testing.T) {
	input := strings.Repeat("\n", 100_000) + "tail"
	start := time.Now()
	output := Records(redact.FromEntries(nil), input)
	assert.Equal(t, input, output)
	assert.Less(t, time.Since(start), time.Second)
}
