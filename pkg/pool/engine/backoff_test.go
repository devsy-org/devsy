package engine

import (
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/provider/providertest"
	"github.com/stretchr/testify/require"
)

func TestBackoffDoublesFromOneSecondToFiveMinutes(t *testing.T) {
	want := map[int]time.Duration{
		-1: 0, 0: 0, 1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second,
		4: 8 * time.Second, 5: 16 * time.Second, 9: 256 * time.Second,
		10: 5 * time.Minute, 11: 5 * time.Minute, 1 << 30: 5 * time.Minute,
	}
	for attempts, delay := range want {
		require.Equal(t, delay, Backoff(attempts), "attempts %d", attempts)
	}
}

func TestNewRejectsAnInvalidPoolAndMissingParts(t *testing.T) {
	good := Config{
		Pool: "pool", Store: nilStore{}, Audit: nilAuditor{}, Provider: new(providertest.Fake),
	}
	_, err := New(good)
	require.NoError(t, err)

	bad := good
	bad.Pool = "a_b"
	_, err = New(bad)
	require.ErrorIs(t, err, provider.ErrInvalidPool)
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Store = nil },
		func(c *Config) { c.Audit = nil },
		func(c *Config) { c.Provider = nil },
	} {
		cfg := good
		mutate(&cfg)
		_, err = New(cfg)
		require.Error(t, err)
	}
}
