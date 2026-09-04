package secrets

import "testing"

const (
	testAPISecret     = "API_TOKEN"
	testProjectSource = "project"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  SecretRef
	}{
		{
			name:  "local",
			input: testAPISecret,
			want:  SecretRef{Type: LocalSourceName, Source: LocalSourceName, Name: testAPISecret},
		},
		{
			name:  SOPSFormatter,
			input: SOPSFormatter + ":" + testProjectSource + "/" + testAPISecret,
			want:  SecretRef{Type: SOPSFormatter, Source: testProjectSource, Name: testAPISecret},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRef(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("ParseRef(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseRefRejectsMalformedQualifiedRefs(t *testing.T) {
	for _, value := range []string{"", "sops:", "sops:project", "sops:/TOKEN", "sops:project/", "sops:project/a/b", "sops:project/API=TOKEN"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseRef(value); err == nil {
				t.Fatalf("ParseRef(%q) unexpectedly succeeded", value)
			}
		})
	}
}
