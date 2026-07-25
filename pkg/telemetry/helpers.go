package telemetry

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"

	"github.com/devsy-org/devsy/pkg/telemetry/distinctid"
)

func GetMachineID() string {
	return distinctid.Get()
}

func hashScopedID(distinctID, value string) string {
	mac := hmac.New(sha256.New, []byte(distinctID))
	mac.Write([]byte(value))
	return fmt.Sprintf("%x", mac.Sum(nil))[:16]
}
