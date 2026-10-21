package ghfixture

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReportRejectsIncompleteMissingDuplicateUnexpectedAndFailedControls(t *testing.T) {
	valid := Report{
		Version:      ReportVersion,
		Completed:    true,
		CLI:          "gh fixture-version",
		Architecture: "arm64",
		Checks: []Check{
			{Name: firstReportCase, OK: true},
			{Name: secondReportCase, OK: true},
		},
	}
	data, err := json.Marshal(valid)
	require.NoError(t, err)
	_, err = ValidateReport(data, []string{firstReportCase, secondReportCase})
	require.NoError(t, err)
	for _, bad := range []string{"[]", "null", "{", string(data[:len(data)-1])} {
		_, err := ValidateReport([]byte(bad), []string{firstReportCase, secondReportCase})
		require.Error(t, err)
	}
	for _, change := range []func(*Report){
		func(r *Report) { r.Completed = false }, func(r *Report) { r.Checks = r.Checks[:1] },
		func(r *Report) { r.Checks[1].Name = firstReportCase }, func(r *Report) { r.Checks[1].Name = "unexpected" },
		func(r *Report) { r.Checks[1].OK = false }, func(r *Report) { r.Version++ },
		func(r *Report) { r.Failure = &Failure{Exit: 1} },
		func(r *Report) { r.CLI = "" }, func(r *Report) { r.Architecture = "" },
	} {
		candidate := valid
		candidate.Checks = append([]Check(nil), valid.Checks...)
		change(&candidate)
		b, err := json.Marshal(candidate)
		require.NoError(t, err)
		_, err = ValidateReport(b, []string{firstReportCase, secondReportCase})
		require.Error(t, err)
	}
	_, err = ValidateReport(data, nil)
	require.Error(t, err)
}

const firstReportCase = "one"

const secondReportCase = "two"
