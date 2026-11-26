package framework

import (
	"runtime"

	"github.com/onsi/ginkgo/v2"
)

const osWindows = "windows"

func SkipOnWindows(reason string) {
	ginkgo.GinkgoHelper()
	if runtime.GOOS == osWindows {
		ginkgo.Skip(reason)
	}
}

func SkipUnlessOS(goos, reason string) {
	ginkgo.GinkgoHelper()
	if runtime.GOOS != goos {
		ginkgo.Skip(reason)
	}
}
