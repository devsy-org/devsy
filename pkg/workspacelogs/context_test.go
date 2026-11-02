package workspacelogs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecorderTravelsInContext(t *testing.T) {
	s, _ := newTestStore(t)
	runID := newRun(t, s, "uid")
	rec := newTestRecorder(t, s, runID, nil)

	assert.Nil(t, RecorderFrom(context.Background()))
	assert.Same(t, rec, RecorderFrom(WithRecorder(context.Background(), rec)))
	assert.Equal(t, runID, rec.RunID())

	base := context.Background()
	assert.Equal(t, base, WithRecorder(base, nil), "a nil recorder leaves the context alone")
}
