package config

import "testing"

func TestMountIsReadOnly(t *testing.T) {
	tests := []struct {
		name string
		m    *Mount
		want bool
	}{
		{"nil mount", nil, false},
		{"no other options", &Mount{Type: "bind"}, false},
		{"bare readonly", &Mount{Other: []string{"readonly"}}, true},
		{"bare ro", &Mount{Other: []string{"ro"}}, true},
		{"uppercase RO", &Mount{Other: []string{"RO"}}, true},
		{"mixed-case ReadOnly", &Mount{Other: []string{"ReadOnly"}}, true},
		{"surrounding whitespace", &Mount{Other: []string{" readonly "}}, true},
		{"readonly=true", &Mount{Other: []string{"readonly=true"}}, true},
		{"readonly=1", &Mount{Other: []string{"readonly=1"}}, true},
		{"readonly=yes", &Mount{Other: []string{"readonly=yes"}}, true},
		{"readonly=on", &Mount{Other: []string{"readonly=on"}}, true},
		{"readonly=TRUE", &Mount{Other: []string{"readonly=TRUE"}}, true},
		{"readonly=false", &Mount{Other: []string{"readonly=false"}}, false},
		{"readonly=0", &Mount{Other: []string{"readonly=0"}}, false},
		{"unrelated option", &Mount{Other: []string{"consistency=cached"}}, false},
		{
			"option containing ro substring",
			&Mount{Other: []string{"bind-propagation=rprivate"}},
			false,
		},
		{"readonly mixed with others", &Mount{Other: []string{"consistency=cached", "ro"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.IsReadOnly(); got != tt.want {
				t.Errorf("IsReadOnly() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMountBindPropagation(t *testing.T) {
	tests := []struct {
		name string
		m    *Mount
		want string
	}{
		{"nil", nil, ""},
		{"unset", &Mount{Other: []string{"readonly"}}, ""},
		{"docker form", &Mount{Other: []string{"bind-propagation=rslave"}}, "rslave"},
		{"compose form", &Mount{Other: []string{"propagation=rprivate"}}, "rprivate"},
		{"case-insensitive key", &Mount{Other: []string{"BIND-PROPAGATION=shared"}}, "shared"},
		{"first match wins", &Mount{Other: []string{
			"propagation=rprivate", "bind-propagation=rshared",
		}}, "rprivate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.BindPropagation(); got != tt.want {
				t.Errorf("BindPropagation() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMountIsBindNonRecursive(t *testing.T) {
	tests := []struct {
		name string
		m    *Mount
		want bool
	}{
		{"nil", nil, false},
		{"unset", &Mount{Other: []string{"readonly"}}, false},
		{"bare token", &Mount{Other: []string{"bind-nonrecursive"}}, true},
		{"=true", &Mount{Other: []string{"bind-nonrecursive=true"}}, true},
		{"=false", &Mount{Other: []string{"bind-nonrecursive=false"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.IsBindNonRecursive(); got != tt.want {
				t.Errorf("IsBindNonRecursive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMountConsistency(t *testing.T) {
	tests := []struct {
		name string
		m    *Mount
		want string
	}{
		{"nil", nil, ""},
		{"unset", &Mount{Other: []string{"readonly"}}, ""},
		{"cached", &Mount{Other: []string{"consistency=cached"}}, "cached"},
		{"delegated", &Mount{Other: []string{"consistency=delegated"}}, "delegated"},
		{"case-insensitive key", &Mount{Other: []string{"CONSISTENCY=consistent"}}, "consistent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.Consistency(); got != tt.want {
				t.Errorf("Consistency() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMountVolumeNoCopy(t *testing.T) {
	tests := []struct {
		name string
		m    *Mount
		want bool
	}{
		{"nil", nil, false},
		{"unset", &Mount{Other: []string{"readonly"}}, false},
		{"docker bare", &Mount{Other: []string{"volume-nocopy"}}, true},
		{"compose bare", &Mount{Other: []string{"nocopy"}}, true},
		{"=true", &Mount{Other: []string{"volume-nocopy=true"}}, true},
		{"=false", &Mount{Other: []string{"volume-nocopy=false"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.VolumeNoCopy(); got != tt.want {
				t.Errorf("VolumeNoCopy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMountVolumeSubpath(t *testing.T) {
	tests := []struct {
		name string
		m    *Mount
		want string
	}{
		{"nil", nil, ""},
		{"unset", &Mount{Other: []string{"readonly"}}, ""},
		{"docker form", &Mount{Other: []string{"volume-subpath=app"}}, "app"},
		{"compose form", &Mount{Other: []string{"subpath=data"}}, "data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.VolumeSubpath(); got != tt.want {
				t.Errorf("VolumeSubpath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMountTmpfsAccessors(t *testing.T) {
	m := &Mount{Other: []string{"tmpfs-size=104857600", "tmpfs-mode=1770"}}
	if got := m.TmpfsSize(); got != "104857600" {
		t.Errorf("TmpfsSize() = %q, want %q", got, "104857600")
	}
	if got := m.TmpfsMode(); got != "1770" {
		t.Errorf("TmpfsMode() = %q, want %q", got, "1770")
	}
	empty := &Mount{}
	if got := empty.TmpfsSize(); got != "" {
		t.Errorf("TmpfsSize() empty = %q, want \"\"", got)
	}
	if got := empty.TmpfsMode(); got != "" {
		t.Errorf("TmpfsMode() empty = %q, want \"\"", got)
	}
}
