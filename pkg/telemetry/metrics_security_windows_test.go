package telemetry

import (
	"encoding/binary"
	"testing"
)

func metricsSecurityFixture() ([]byte, []byte) {
	user := []byte{1, 1, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0}
	descriptor := make([]byte, 60)
	descriptor[0] = 1
	binary.LittleEndian.PutUint16(descriptor[2:4], 0x8004)
	binary.LittleEndian.PutUint32(descriptor[4:8], 20)
	binary.LittleEndian.PutUint32(descriptor[16:20], 32)
	copy(descriptor[20:32], user)
	descriptor[32] = 2
	binary.LittleEndian.PutUint16(descriptor[34:36], 28)
	binary.LittleEndian.PutUint16(descriptor[36:38], 1)
	binary.LittleEndian.PutUint16(descriptor[42:44], 20)
	copy(descriptor[48:60], user)
	return descriptor, user
}

func TestMetricsSecurityDescriptorBounds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte) []byte
		valid  bool
	}{
		{name: "owner only", mutate: func(d []byte) []byte { return d }, valid: true},
		{name: "truncated header", mutate: func(d []byte) []byte { return d[:19] }},
		{name: "null dacl", mutate: func(d []byte) []byte { clear(d[16:20]); return d }},
		{name: "missing dacl flag", mutate: func(d []byte) []byte { d[2] = 0; return d }},
		{
			name:   "owner offset",
			mutate: func(d []byte) []byte { binary.LittleEndian.PutUint32(d[4:8], ^uint32(0)); return d },
		},
		{
			name:   "acl offset",
			mutate: func(d []byte) []byte { binary.LittleEndian.PutUint32(d[16:20], ^uint32(0)); return d },
		},
		{
			name:   "acl oversized",
			mutate: func(d []byte) []byte { binary.LittleEndian.PutUint16(d[34:36], ^uint16(0)); return d },
		},
		{
			name:   "truncated ace header",
			mutate: func(d []byte) []byte { binary.LittleEndian.PutUint16(d[34:36], 10); return d },
		},
		{
			name:   "oversized ace",
			mutate: func(d []byte) []byte { binary.LittleEndian.PutUint16(d[42:44], ^uint16(0)); return d },
		},
		{
			name:   "truncated sid",
			mutate: func(d []byte) []byte { binary.LittleEndian.PutUint16(d[42:44], 15); return d },
		},
		{name: "oversized sid", mutate: func(d []byte) []byte { d[49] = 16; return d }},
		{name: "sid length exceeds ace", mutate: func(d []byte) []byte { d[49] = 2; return d }},
		{name: "wrong owner", mutate: func(d []byte) []byte { d[28]++; return d }},
		{name: "wrong ace sid", mutate: func(d []byte) []byte { d[56]++; return d }},
		{name: "unsupported ace", mutate: func(d []byte) []byte { d[40] = 1; return d }},
		{name: "unsupported acl revision", mutate: func(d []byte) []byte { d[32] = 1; return d }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor, user := metricsSecurityFixture()
			if got := validMetricsSecurity(test.mutate(descriptor), user); got != test.valid {
				t.Fatalf("validMetricsSecurity = %v, want %v", got, test.valid)
			}
		})
	}
}
