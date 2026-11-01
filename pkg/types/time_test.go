package types_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/types"
	"gotest.tools/assert"
)

const (
	utcStamp     = "2026-10-19T08:30:00Z"
	jsonUTCStamp = `"` + utcStamp + `"`
)

var (
	utcInstant     = time.Date(2026, 10, 19, 8, 30, 0, 0, time.UTC)
	plusTwoZone    = time.FixedZone("UTC+2", 2*60*60)
	plusTwoInstant = time.Date(2026, 10, 19, 10, 30, 0, 0, plusTwoZone)
	subSecond      = time.Date(2026, 10, 19, 8, 30, 0, 123456789, time.UTC)
)

func TestTimeMarshalJSON(t *testing.T) {
	testCases := []struct {
		name   string
		input  types.Time
		expect string
	}{
		{name: "zero value is null", input: types.Time{}, expect: "null"},
		{name: "UTC is formatted with Z", input: types.NewTime(utcInstant), expect: jsonUTCStamp},
		{
			name:   "offset is converted to UTC",
			input:  types.NewTime(plusTwoInstant),
			expect: jsonUTCStamp,
		},
		{name: "sub-second is dropped", input: types.NewTime(subSecond), expect: jsonUTCStamp},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out, err := json.Marshal(testCase.input)
			assert.NilError(t, err)
			assert.Equal(t, string(out), testCase.expect)
		})
	}
}

func TestTimeUnmarshalJSON(t *testing.T) {
	testCases := []struct {
		name    string
		input   string
		expect  time.Time
		wantErr bool
	}{
		{name: "null is zero", input: `{"at": null}`, expect: time.Time{}},
		{name: "UTC instant", input: `{"at": "` + utcStamp + `"}`, expect: utcInstant},
		{name: "offset instant", input: `{"at": "2026-10-19T10:30:00+02:00"}`, expect: utcInstant},
		{name: "date only is rejected", input: `{"at": "2026-10-19"}`, wantErr: true},
		{name: "missing zone is rejected", input: `{"at": "2026-10-19T08:30:00"}`, wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var data struct {
				At types.Time `json:"at"`
			}
			err := json.Unmarshal([]byte(testCase.input), &data)
			if testCase.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, data.At.Time.Equal(testCase.expect), "got %v", data.At.Time)
		})
	}
}

func TestTimeQueryParameterRoundTrip(t *testing.T) {
	testCases := []struct {
		name    string
		input   string
		expect  time.Time
		wantErr bool
	}{
		{name: "empty is zero", input: "", expect: time.Time{}},
		{name: "null is zero", input: "null", expect: time.Time{}},
		{name: "UTC instant", input: utcStamp, expect: utcInstant},
		{name: "offset instant", input: "2026-10-19T10:30:00+02:00", expect: utcInstant},
		{name: "missing zone is rejected", input: "2026-10-19T08:30:00", wantErr: true},
		{name: "garbage is rejected", input: "yesterday", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var got types.Time
			err := got.UnmarshalQueryParameter(testCase.input)
			if testCase.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, got.Time.Equal(testCase.expect), "got %v", got.Time)
		})
	}
}

func TestTimeMarshalQueryParameter(t *testing.T) {
	testCases := []struct {
		name   string
		input  types.Time
		expect string
	}{
		{name: "zero value is empty", input: types.Time{}, expect: ""},
		{name: "offset is encoded as UTC", input: types.NewTime(plusTwoInstant), expect: utcStamp},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			encoded, err := testCase.input.MarshalQueryParameter()
			assert.NilError(t, err)
			assert.Equal(t, encoded, testCase.expect)
		})
	}
}

func TestTimeNilAndOrderingHelpers(t *testing.T) {
	earlier := types.NewTime(utcInstant)
	later := types.NewTime(utcInstant.Add(time.Hour))
	sameInstant := types.NewTime(utcInstant.In(plusTwoZone))
	var nilTime *types.Time
	var zero types.Time

	testCases := []struct {
		name   string
		got    bool
		expect bool
	}{
		{name: "Before earlier than later", got: earlier.Before(&later), expect: true},
		{name: "Before later than earlier", got: later.Before(&earlier), expect: false},
		{name: "Before with nil argument", got: earlier.Before(nil), expect: false},
		{name: "Equal nil and nil", got: nilTime.Equal(nil), expect: true},
		{name: "Equal value and nil", got: earlier.Equal(nil), expect: false},
		{name: "Equal across zones", got: earlier.Equal(&sameInstant), expect: true},
		{name: "IsZero nil receiver", got: nilTime.IsZero(), expect: true},
		{name: "IsZero zero value", got: zero.IsZero(), expect: true},
		{name: "IsZero set value", got: later.IsZero(), expect: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.got, testCase.expect)
		})
	}
}
