package ghfixture

import (
	"encoding/json"
	"errors"
)

const ReportVersion = 1

type Check struct {
	Name string `json:"check"`
	OK   bool   `json:"ok"`
}

type Report struct {
	Version      int      `json:"version"`
	Completed    bool     `json:"completed"`
	CLI          string   `json:"cli"`
	Architecture string   `json:"architecture"`
	Checks       []Check  `json:"checks"`
	Failure      *Failure `json:"failure,omitempty"`
}

type Failure struct {
	Exit         int  `json:"exit"`
	TLS          bool `json:"tls"`
	Lookup       bool `json:"lookup"`
	Readiness    bool `json:"readiness"`
	Unauthorized bool `json:"unauthorized"`
	EmptyStderr  bool `json:"empty_stderr"`
}

func ValidateReport(data []byte, expected []string) (Report, error) {
	var report Report
	if json.Unmarshal(data, &report) != nil || !report.complete() {
		return Report{}, errors.New("incomplete GitHub fixture report")
	}
	wanted, err := expectedCases(expected)
	if err != nil {
		return Report{}, err
	}
	if len(report.Checks) != len(wanted) {
		return Report{}, errors.New("GitHub fixture case count mismatch")
	}
	return report, validateCases(report.Checks, wanted)
}

func (r Report) complete() bool {
	return r.Version == ReportVersion && r.Completed && r.CLI != "" && r.Architecture != "" &&
		r.Failure == nil
}

func expectedCases(expected []string) (map[string]bool, error) {
	if len(expected) == 0 {
		return nil, errors.New("empty expected GitHub fixture cases")
	}
	wanted := map[string]bool{}
	for _, name := range expected {
		if name == "" || wanted[name] {
			return nil, errors.New("invalid expected GitHub fixture cases")
		}
		wanted[name] = true
	}
	return wanted, nil
}

func validateCases(checks []Check, wanted map[string]bool) error {
	for _, check := range checks {
		if !check.OK || !wanted[check.Name] {
			return errors.New("failed, duplicate or unexpected GitHub fixture case")
		}
		delete(wanted, check.Name)
	}
	return nil
}
