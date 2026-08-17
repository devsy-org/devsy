package provider

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOptions(t *testing.T) {
	cases := []struct {
		name    string
		options []string
		want    map[string]string
		wantErr string
	}{
		{
			name:    "empty input yields empty map",
			options: []string{},
			want:    map[string]string{},
		},
		{
			name:    "single key value",
			options: []string{"foo=bar"},
			want:    map[string]string{strings.ToUpper(testNameFoo): testNameBar},
		},
		{
			name:    "value containing equals is preserved",
			options: []string{"foo=a=b"},
			want:    map[string]string{strings.ToUpper(testNameFoo): "a=b"},
		},
		{
			name:    "lowercase key is uppercased",
			options: []string{"foo=bar"},
			want:    map[string]string{strings.ToUpper(testNameFoo): testNameBar},
		},
		{
			name:    "surrounding whitespace around key is trimmed",
			options: []string{" foo =bar"},
			want:    map[string]string{strings.ToUpper(testNameFoo): testNameBar},
		},
		{
			name:    "multiple options are collected",
			options: []string{"A=1", "B=two", "C="},
			want:    map[string]string{"A": "1", "B": "two", "C": ""},
		},
		{
			name:    "option without equals is rejected",
			options: []string{strings.ToUpper(testNameFoo)},
			wantErr: `invalid option "FOO", expected format KEY=VALUE`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOptions(tc.options)
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
