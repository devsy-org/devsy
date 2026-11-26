package driver

import "testing"

type mountCapableStub struct {
	Driver
	tmpfs bool
}

func (m mountCapableStub) SupportsMountType(mountType string) bool {
	return mountType == MountTypeTmpfs && m.tmpfs
}

type plainDriverStub struct {
	Driver
}

func TestDriverSupportsMountType_DefaultsToTrue(t *testing.T) {
	if !DriverSupportsMountType(plainDriverStub{}, MountTypeTmpfs) {
		t.Fatal("drivers without the capability should default to supported")
	}
}

func TestDriverSupportsMountType_RespectsCapability(t *testing.T) {
	if !DriverSupportsMountType(mountCapableStub{tmpfs: true}, MountTypeTmpfs) {
		t.Fatal("tmpfs-capable driver should report supported")
	}
	if DriverSupportsMountType(mountCapableStub{tmpfs: false}, MountTypeTmpfs) {
		t.Fatal("non-tmpfs driver should report unsupported")
	}
}

func TestDriverPoliciesWithoutCapabilities(t *testing.T) {
	d := plainDriverStub{}
	if !DriverRequiresMountStreaming(d) {
		t.Fatal("unknown drivers require streamed mounts")
	}
	if !DriverRequiresWorkspaceChown(d) {
		t.Fatal("unknown drivers require workspace chown")
	}
	if DriverRecreateMode(d) != RecreateStop {
		t.Fatal("unknown drivers are stopped for recreate")
	}
}
