package apple

import (
	"encoding/json"
	"testing"
)

// containerInspectFixture is verbatim output from `container inspect` on
// macOS 26.5 / container 1.1.0.
const containerInspectFixture = `[
  {
    "configuration" : {
      "creationDate" : "2026-07-25T01:57:23Z",
      "id" : "devsy-probe",
      "image" : { "reference" : "docker.io/library/alpine:latest" },
      "initProcess" : {
        "arguments" : ["300"],
        "environment" : ["PATH=/usr/local/sbin:/usr/local/bin"],
        "executable" : "sleep",
        "user" : { "id" : { "gid" : 0, "uid" : 0 } },
        "workingDirectory" : "/"
      },
      "labels" : { "devsy.test" : "1", "dev.containers.id" : "ws-42" },
      "mounts" : [
        { "type" : { "virtiofs" : {} }, "options" : [], "source" : "/host/work", "destination" : "/workspaces/work" }
      ],
      "platform" : { "architecture" : "arm64", "os" : "linux" }
    },
    "id" : "devsy-probe",
    "status" : { "startedDate" : "2026-07-25T01:57:25Z", "state" : "running" }
  }
]`

func TestContainerInspectMapping(t *testing.T) {
	var raw []containerInspect
	if err := json.Unmarshal([]byte(containerInspectFixture), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 container, got %d", len(raw))
	}

	got := raw[0].toContainerDetails()

	fields := []struct {
		name, got, want string
	}{
		{"ID", got.ID, "devsy-probe"},
		{"State.Status", got.State.Status, stateRunning},
		{"StartedAt", got.State.StartedAt, "2026-07-25T01:57:25Z"},
		{"WorkingDir", got.Config.WorkingDir, "/"},
		{"User", got.Config.User, "0"},
		{"LegacyImage", got.Config.LegacyImage, "docker.io/library/alpine:latest"},
		{"label", got.Config.Labels["dev.containers.id"], "ws-42"},
	}
	for _, f := range fields {
		if f.got != f.want {
			t.Errorf("%s = %q, want %q", f.name, f.got, f.want)
		}
	}

	if len(got.Mounts) != 1 || got.Mounts[0].Destination != "/workspaces/work" {
		t.Errorf("mounts = %+v", got.Mounts)
	}
	// virtiofs host share must normalize to the Docker "bind" vocabulary.
	if got.Mounts[0].Type != "bind" {
		t.Errorf("mount type = %q, want bind", got.Mounts[0].Type)
	}
}

func TestNormalizeState(t *testing.T) {
	cases := []struct{ in, want string }{
		{"running", stateRunning},
		{"Running", stateRunning},
		{"stopped", stateExited},
		{"Stopped", stateExited},
	}
	for _, c := range cases {
		if got := normalizeState(c.in); got != c.want {
			t.Errorf("normalizeState(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// imageInspectFixture is verbatim output from `container image inspect` for a
// multi-arch image; only the arm64 variant should be selected on Apple silicon.
const imageInspectFixture = `[
  {
    "configuration" : { "name" : "docker.io/library/alpine:latest" },
    "id" : "28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b",
    "variants" : [
      {
        "config" : {
          "architecture" : "amd64",
          "os" : "linux",
          "config" : { "Cmd" : ["/bin/sh"], "Env" : ["PATH=/amd64"], "WorkingDir" : "/" }
        }
      },
      {
        "config" : {
          "architecture" : "unknown",
          "os" : "unknown",
          "config" : {}
        }
      },
      {
        "config" : {
          "architecture" : "arm64",
          "os" : "linux",
          "config" : { "Cmd" : ["/bin/sh"], "Env" : ["PATH=/arm64"], "User" : "root", "WorkingDir" : "/" }
        }
      }
    ]
  }
]`

func TestImageInspectMapping(t *testing.T) {
	var raw []imageInspect
	if err := json.Unmarshal([]byte(imageInspectFixture), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := raw[0].toImageDetails("arm64")

	if got.ID != "28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b" {
		t.Errorf("ID = %q", got.ID)
	}
	// Must pick the arm64 variant, not amd64 (first) or unknown.
	if len(got.Config.Env) != 1 || got.Config.Env[0] != "PATH=/arm64" {
		t.Errorf("Env = %v, want [PATH=/arm64] (arm64 variant)", got.Config.Env)
	}
	if got.Config.User != "root" {
		t.Errorf("User = %q, want root", got.Config.User)
	}
}

func TestMountTypeUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"object form", `{"virtiofs":{}}`, "bind"},
		{"plain string form", `"tmpfs"`, "tmpfs"},
		{"non-virtiofs object passthrough", `{"tmpfs":{}}`, "tmpfs"},
		{"empty object yields empty", `{}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var m mountType
			if err := json.Unmarshal([]byte(c.in), &m); err != nil {
				t.Fatalf("unmarshal %s: %v", c.in, err)
			}
			if got := m.dockerType(); got != c.want {
				t.Errorf("dockerType() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestImageInspectNoKnownVariant(t *testing.T) {
	raw := []imageInspect{{
		ID: "id1",
		Variants: []imageVariant{
			{Config: imageVariantConfig{Architecture: "unknown", OS: "unknown"}},
			{Config: imageVariantConfig{Architecture: ""}},
		},
	}}
	got := raw[0].toImageDetails("arm64")
	if got.ID != "id1" {
		t.Errorf("ID = %q, want id1", got.ID)
	}
	if got.Config.User != "" || len(got.Config.Env) != 0 {
		t.Errorf("expected empty config when no known-arch variant, got %+v", got.Config)
	}
}

func TestImageInspectSkipsUnknownArch(t *testing.T) {
	var raw []imageInspect
	if err := json.Unmarshal([]byte(imageInspectFixture), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// A platform with no matching variant falls back to the first known arch (amd64),
	// never the "unknown" variant.
	got := raw[0].toImageDetails("s390x")
	if len(got.Config.Env) == 0 || got.Config.Env[0] != "PATH=/amd64" {
		t.Errorf("fallback Env = %v, want amd64 variant", got.Config.Env)
	}
}
