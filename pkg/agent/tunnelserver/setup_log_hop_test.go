package tunnelserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNilHopIsInert(t *testing.T) {
	var hop *SetupLogHop

	assert.Nil(t, hop.Options())
	assert.True(t, hop.Flush(context.Background()))
	hop.Close()
	assert.Nil(t, NewSetupLogHop(context.Background(), HopOptions{}), "no session means no hop")
}

func TestSetupLogOptionsTravelInContext(t *testing.T) {
	ctx := context.Background()
	assert.Empty(t, SetupLogOptionsFrom(ctx))
	assert.Equal(t, ctx, WithSetupLogOptions(ctx, nil))

	opts := []Option{WithAllowKubeConfig(true)}
	assert.Len(t, SetupLogOptionsFrom(WithSetupLogOptions(ctx, opts)), 1)
}
