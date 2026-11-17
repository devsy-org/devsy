package telemetry

import (
	"bytes"
	"encoding/binary"
)

func validMetricsSecurity(descriptor, user []byte) bool {
	if len(descriptor) < 20 || descriptor[0] != 1 {
		return false
	}
	control := binary.LittleEndian.Uint16(descriptor[2:4])
	if control&0x8004 != 0x8004 {
		return false
	}
	ownerOffset := binary.LittleEndian.Uint32(descriptor[4:8])
	aclOffset := binary.LittleEndian.Uint32(descriptor[16:20])
	owner := securityOffset(descriptor, ownerOffset)
	acl := securityOffset(descriptor, aclOffset)
	return matchingSecuritySID(owner, user) && validMetricsACL(acl, user)
}

func securityOffset(descriptor []byte, offset uint32) []byte {
	if offset < 20 || uint64(offset) >= uint64(len(descriptor)) {
		return nil
	}
	return descriptor[int(offset):]
}

func matchingSecuritySID(data, user []byte) bool {
	if len(data) < 8 || data[0] != 1 || data[1] > 15 {
		return false
	}
	size := 8 + 4*int(data[1])
	return size <= len(data) && bytes.Equal(data[:size], user)
}

func validMetricsACL(data, user []byte) bool {
	if len(data) < 8 || (data[0] != 2 && data[0] != 4) {
		return false
	}
	size := int(binary.LittleEndian.Uint16(data[2:4]))
	if size < 8 || size > len(data) {
		return false
	}
	count := binary.LittleEndian.Uint16(data[4:6])
	remaining := data[8:size]
	for range count {
		var ok bool
		remaining, ok = nextMetricsACE(remaining, user)
		if !ok {
			return false
		}
	}
	return true
}

func nextMetricsACE(data, user []byte) ([]byte, bool) {
	if len(data) < 4 {
		return nil, false
	}
	size := int(binary.LittleEndian.Uint16(data[2:4]))
	if size < 8 || size > len(data) || data[0] != 0 {
		return nil, false
	}
	if !matchingSecuritySID(data[8:size], user) {
		return nil, false
	}
	return data[size:], true
}
