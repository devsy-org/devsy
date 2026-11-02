package up

import (
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2/types"
)

func TestIsPodmanMachineQualificationFilter(t *testing.T) {
	cases := []struct {
		name          string
		filter        string
		qualification bool
	}{
		{name: "unfiltered"},
		{name: "bare native", filter: podmanMachineBindLabel, qualification: true},
		{name: "nested native", filter: "((" + podmanMachineBindLabel + "))", qualification: true},
		{
			name:          "spaced native",
			filter:        " ( ( " + podmanMachineBindLabel + " ) ) ",
			qualification: true,
		},
		{
			name:          "case insensitive native",
			filter:        strings.ToUpper(podmanMachineBindLabel),
			qualification: true,
		},
		{
			name:          "vertical tab and form feed",
			filter:        "\v\f" + podmanMachineBindLabel + "\v\f",
			qualification: true,
		},
		{
			name:          "Unicode whitespace",
			filter:        "\u2003\u00a0" + podmanMachineBindLabel + "\u2003\u00a0",
			qualification: true,
		},
		{
			name:          "Ginkgo dotted capital I",
			filter:        strings.ReplaceAll(podmanMachineBindLabel, "i", "\u0130"),
			qualification: true,
		},
		{name: "broad exclusion", filter: "!kubernetes"},
		{name: "native exclusion", filter: "!" + podmanMachineBindLabel},
		{name: "OR includes native", filter: "up-behaviors || " + podmanMachineBindLabel},
		{name: "AND includes native", filter: podmanMachineBindLabel + " && !kubernetes"},
		{
			name:   "parenthesized compound",
			filter: "(" + podmanMachineBindLabel + " || up-behaviors)",
		},
		{name: "regex selects native", filter: "/up-provider-podman-machine-bind/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := types.ParseLabelFilter(tc.filter); err != nil {
				t.Fatalf("policy case must be valid Ginkgo syntax: %v", err)
			}
			if got := isPodmanMachineQualificationFilter(tc.filter); got != tc.qualification {
				t.Errorf("qualification for %q = %t, want %t", tc.filter, got, tc.qualification)
			}
		})
	}
}
