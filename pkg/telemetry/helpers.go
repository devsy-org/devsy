package telemetry

import "github.com/devsy-org/devsy/pkg/telemetry/distinctid"

func GetMachineID() string {
	return distinctid.Get()
}

func hashScopedID(_ string, value string) string {
	return distinctid.Hash("workspace", value)
}
