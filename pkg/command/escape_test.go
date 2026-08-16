package command

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQuote(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "empty returns empty", args: nil, want: ""},
		{name: "empty slice returns empty", args: []string{}, want: ""},
		{
			name: "single arg passed through unquoted",
			args: []string{"devsy-linux-amd64"},
			want: "devsy-linux-amd64",
		},
		{
			name: "single arg with spaces passed through unchanged",
			args: []string{"agent internal ssh-git-clone"},
			want: "agent internal ssh-git-clone",
		},
		{
			name: "multiple args are shell-quoted and joined",
			args: []string{"agent", "internal", "ssh-git-clone"},
			want: "agent internal ssh-git-clone",
		},
		{
			name: "arg needing escaping is single-quoted",
			args: []string{"agent", "--key-file=/tmp/my key"},
			want: "agent '--key-file=/tmp/my key'",
		},
		{
			name: "embedded single quote is escaped",
			args: []string{"echo", "it's"},
			want: "echo 'it'\"'\"'s'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Quote(tc.args))
		})
	}
}
