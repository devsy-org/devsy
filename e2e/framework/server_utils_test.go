package framework

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchIPv4_IPNet_ClassC(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("192.168.1.0/24")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.0", matchIPv4(ipNet))
}

func TestMatchIPv4_IPNet_ClassA(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("10.0.0.0/8")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.0", matchIPv4(ipNet))
}

func TestMatchIPv4_IPNet_ClassB_NotSelected(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("172.16.0.0/16")
	require.NoError(t, err)
	assert.Empty(t, matchIPv4(ipNet))
}

func TestMatchIPv4_IPAddr_ClassC(t *testing.T) {
	addr := &net.IPAddr{IP: net.ParseIP("192.168.1.5")}
	require.NotNil(t, addr.IP)
	assert.Equal(t, "192.168.1.5", matchIPv4(addr))
}

func TestMatchIPv4_IPv6_NotSelected(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("fd00::1/64")
	require.NoError(t, err)
	assert.Empty(t, matchIPv4(ipNet))
}

func TestMatchIPv4_UnknownAddrType_NotSelected(t *testing.T) {
	assert.Empty(t, matchIPv4(&net.UDPAddr{IP: net.ParseIP("192.168.1.5"), Port: 1234}))
}
