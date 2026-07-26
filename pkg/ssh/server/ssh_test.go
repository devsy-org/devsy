package server

import (
	"testing"
	"time"
)

func TestKeepAliveConfig_Defaults(t *testing.T) {
	t.Setenv(envKeepAliveInterval, "")
	t.Setenv(envKeepAliveCountMax, "")

	interval, countMax := keepAliveConfig()
	if interval != defaultKeepAliveInterval {
		t.Errorf("interval = %s, want %s", interval, defaultKeepAliveInterval)
	}
	if countMax != defaultKeepAliveCountMax {
		t.Errorf("countMax = %d, want %d", countMax, defaultKeepAliveCountMax)
	}

	if tolerance := interval * time.Duration(countMax); tolerance < 60*time.Second {
		t.Errorf("keep-alive tolerance %s is too aggressive (want >= 60s)", tolerance)
	}
}

func TestKeepAliveConfig_Overrides(t *testing.T) {
	tests := []struct {
		name         string
		interval     string
		countMax     string
		wantInterval time.Duration
		wantCountMax int
	}{
		{"duration string", "30s", "4", 30 * time.Second, 4},
		{"bare seconds", "45", "3", 45 * time.Second, 3},
		{"invalid interval falls back", "nope", "5", defaultKeepAliveInterval, 5},
		{"invalid count falls back", "20s", "0", 20 * time.Second, defaultKeepAliveCountMax},
		{"negative interval falls back", "-5s", "2", defaultKeepAliveInterval, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envKeepAliveInterval, tt.interval)
			t.Setenv(envKeepAliveCountMax, tt.countMax)

			interval, countMax := keepAliveConfig()
			if interval != tt.wantInterval {
				t.Errorf("interval = %s, want %s", interval, tt.wantInterval)
			}
			if countMax != tt.wantCountMax {
				t.Errorf("countMax = %d, want %d", countMax, tt.wantCountMax)
			}
		})
	}
}
