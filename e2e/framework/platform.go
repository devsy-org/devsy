package framework

import (
	"runtime"

	"github.com/onsi/ginkgo/v2"
)

func SkipOnWindows(reason string) {
	ginkgo.GinkgoHelper()
	if runtime.GOOS == "windows" {
		ginkgo.Skip(reason)
	}
}

func SkipUnlessOS(goos, reason string) {
	ginkgo.GinkgoHelper()
	if runtime.GOOS != goos {
		ginkgo.Skip(reason)
	}
}
