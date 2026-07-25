package docker

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSELinuxLabelDisableArgs(t *testing.T) {
	labelDisable := []string{testSecurityOptFlag, selinuxLabelDisable}

	cases := []struct {
		name         string
		securityOpts []string
		enforcing    bool
		want         []string
	}{
		{
			name:      "enforcing adds label=disable",
			enforcing: true,
			want:      labelDisable,
		},
		{
			name:      "not enforcing is a no-op",
			enforcing: false,
			want:      nil,
		},
		{
			name:         "not enforcing with user opts is still a no-op",
			securityOpts: []string{testSeccompUnconfined},
			enforcing:    false,
			want:         nil,
		},
		{
			name:         "user label opt is respected",
			securityOpts: []string{"label=type:container_t"},
			enforcing:    true,
			want:         nil,
		},
		{
			name:         "unrelated security opt still adds label=disable",
			securityOpts: []string{testSeccompUnconfined},
			enforcing:    true,
			want:         labelDisable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selinuxLabelDisableArgs(tc.securityOpts, tc.enforcing)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSELinuxEnforcing(t *testing.T) {
	orig := selinuxEnforcePath
	t.Cleanup(func() { selinuxEnforcePath = orig })

	write := func(t *testing.T, content string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "enforce")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "enforcing", content: "1\n", want: true},
		{name: "permissive", content: "0\n", want: false},
		{name: "no trailing newline", content: "1", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selinuxEnforcePath = write(t, tc.content)
			if got := selinuxEnforcing(); got != tc.want {
				t.Fatalf("selinuxEnforcing() = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("missing file means not enforcing", func(t *testing.T) {
		selinuxEnforcePath = filepath.Join(t.TempDir(), "does-not-exist")
		if selinuxEnforcing() {
			t.Fatal("expected false when SELinux file is absent")
		}
	})
}

func TestUserSetsSELinuxLabel(t *testing.T) {
	cases := []struct {
		name string
		opts []string
		want bool
	}{
		{name: "nil", opts: nil, want: false},
		{name: "label=disable", opts: []string{selinuxLabelDisable}, want: true},
		{name: "label with spaces", opts: []string{" label=type:foo "}, want: true},
		{name: "label colon separator", opts: []string{"label:disable"}, want: true},
		{name: "bare label", opts: []string{"label"}, want: true},
		{name: "unrelated prefix not matched", opts: []string{"labelfoo=bar"}, want: false},
		{
			name: "other opts",
			opts: []string{testSeccompUnconfined, "no-new-privileges"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := userSetsSELinuxLabel(tc.opts); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
