package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestIsTransientLoadErr(t *testing.T) {
	var x any
	syntaxErr := json.Unmarshal([]byte("{"), &x)
	if syntaxErr == nil {
		t.Fatal("expected json.Unmarshal to return a syntax error")
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "io.ErrUnexpectedEOF",
			err:  io.ErrUnexpectedEOF,
			want: true,
		},
		{
			name: "wrapped io.ErrUnexpectedEOF",
			err:  fmt.Errorf("wrapped: %w", io.ErrUnexpectedEOF),
			want: true,
		},
		{
			name: "real json.SyntaxError",
			err:  syntaxErr,
			want: true,
		},
		{
			name: "wrapped json.SyntaxError",
			err:  fmt.Errorf("wrapped: %w", syntaxErr),
			want: true,
		},
		{
			name: "os.ErrNotExist",
			err:  os.ErrNotExist,
			want: false,
		},
		{
			name: "unrelated error",
			err:  errors.New("something else"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientLoadErr(tt.err); got != tt.want {
				t.Errorf("isTransientLoadErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
