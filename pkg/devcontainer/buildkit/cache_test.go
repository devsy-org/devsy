package buildkit

import (
	"os"
	"reflect"
	"testing"

	"github.com/moby/buildkit/client"
)

const (
	cacheAttrRef         = "ref"
	cacheAttrToken       = "token"
	cacheAttrURL         = "url"
	cacheTypeRegistry    = "registry"
	cacheRuntimeTokenEnv = "ACTIONS_RUNTIME_TOKEN"
	cacheURLEnv          = "ACTIONS_CACHE_URL"
	cacheEnvToken        = "env-token"
	cacheEnvURL          = "https://env-cache"
	ghaScopeEntry        = "type=gha,scope=x"
)

type parseCacheEntryCase struct {
	name    string
	in      []string
	env     map[string]string
	want    []client.CacheOptionsEntry
	wantErr bool
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
}

func runParseCacheEntryCases(t *testing.T, cases []parseCacheEntryCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			unsetEnvForTest(t, cacheRuntimeTokenEnv)
			unsetEnvForTest(t, cacheURLEnv)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got, err := ParseCacheEntry(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got entries %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseCacheEntry(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseCacheEntryParsesRefAndTypedFields(t *testing.T) {
	runParseCacheEntryCases(t, []parseCacheEntryCase{
		{
			name: "ref only fields expand to registry entries",
			in:   []string{"ghcr.io/a:1,ghcr.io/b:2"},
			want: []client.CacheOptionsEntry{
				{Type: cacheTypeRegistry, Attrs: map[string]string{cacheAttrRef: "ghcr.io/a:1"}},
				{Type: cacheTypeRegistry, Attrs: map[string]string{cacheAttrRef: "ghcr.io/b:2"}},
			},
		},
		{
			name: "typed entry keeps type and attrs",
			in:   []string{"type=registry,ref=ghcr.io/x:cache"},
			want: []client.CacheOptionsEntry{
				{
					Type:  cacheTypeRegistry,
					Attrs: map[string]string{cacheAttrRef: "ghcr.io/x:cache"},
				},
			},
		},
		{
			name: "keys are lowercased and values keep embedded equals",
			in:   []string{"TYPE=local,SRC=/tmp/x,Dest=a=b"},
			want: []client.CacheOptionsEntry{
				{Type: "local", Attrs: map[string]string{"src": "/tmp/x", "dest": "a=b"}},
			},
		},
	})
}

func TestParseCacheEntryRejectsInvalidInput(t *testing.T) {
	runParseCacheEntryCases(t, []parseCacheEntryCase{
		{
			name:    "missing type is rejected",
			in:      []string{"ref=ghcr.io/x:cache"},
			wantErr: true,
		},
		{
			name:    "empty type is rejected",
			in:      []string{"type=,ref=ghcr.io/x:cache"},
			wantErr: true,
		},
		{
			name:    "field without equals in typed entry is rejected",
			in:      []string{"type=local,oops"},
			wantErr: true,
		},
		{
			name:    "malformed csv is rejected",
			in:      []string{`type=local,"ref=x`},
			wantErr: true,
		},
	})
}

func TestParseCacheEntryGhaTakesCredentialsFromEnvironment(t *testing.T) {
	runParseCacheEntryCases(t, []parseCacheEntryCase{
		{
			name: "gha entry takes token and url from environment",
			in:   []string{ghaScopeEntry},
			env: map[string]string{
				cacheRuntimeTokenEnv: cacheEnvToken,
				cacheURLEnv:          cacheEnvURL,
			},
			want: []client.CacheOptionsEntry{
				{Type: "gha", Attrs: map[string]string{
					"scope":        "x",
					cacheAttrToken: cacheEnvToken,
					cacheAttrURL:   cacheEnvURL,
				}},
			},
		},
		{
			name: "explicit gha attrs take precedence over environment",
			in:   []string{"type=gha,token=explicit,url=https://explicit"},
			env: map[string]string{
				cacheRuntimeTokenEnv: cacheEnvToken,
				cacheURLEnv:          cacheEnvURL,
			},
			want: []client.CacheOptionsEntry{
				{Type: "gha", Attrs: map[string]string{
					cacheAttrToken: "explicit",
					cacheAttrURL:   "https://explicit",
				}},
			},
		},
	})
}

func TestParseCacheEntryDropsGhaWithoutCredentials(t *testing.T) {
	runParseCacheEntryCases(t, []parseCacheEntryCase{
		{
			name: "gha entry without credentials is dropped",
			in:   []string{ghaScopeEntry},
			want: []client.CacheOptionsEntry{},
		},
		{
			name: "gha entry dropped when only url is available",
			in:   []string{ghaScopeEntry},
			env: map[string]string{
				cacheURLEnv: cacheEnvURL,
			},
			want: []client.CacheOptionsEntry{},
		},
		{
			name: "dropped gha entry does not drop sibling entries",
			in:   []string{ghaScopeEntry, "type=registry,ref=ghcr.io/r:1"},
			want: []client.CacheOptionsEntry{
				{Type: cacheTypeRegistry, Attrs: map[string]string{cacheAttrRef: "ghcr.io/r:1"}},
			},
		},
	})
}
