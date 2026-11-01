package kubernetes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const testImage = "registry.example.com/team/image@sha256:" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

const testArchitecture = "amd64"

type imageDetailsTestFixture struct {
	driver  *KubernetesDriver
	path    string
	request driver.ImageDetailsRequest
	wrapper func(string) string
	write   func(string)
}

func imageDetailsFixture(t *testing.T) imageDetailsTestFixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	d := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			ImageConfigFile: path,
			Architecture:    testArchitecture,
		},
	}
	request := driver.ImageDetailsRequest{ImageName: testImage}
	wrapper := func(payload string) string {
		return `{"schemaVersion":1,"image":"` + testImage + `","os":"linux","architecture":"amd64","config":` + payload + `}`
	}
	write := func(data string) { t.Helper(); require.NoError(t, os.WriteFile(path, []byte(data), 0o600)) }
	return imageDetailsTestFixture{
		driver:  d,
		path:    path,
		request: request,
		wrapper: wrapper,
		write:   write,
	}
}

func TestImageDetailsFile(t *testing.T) {
	f := imageDetailsFixture(t)
	d, path, request, wrapper, write := f.driver, f.path, f.request, f.wrapper, f.write
	d.options.ImageConfigFile = ""
	details, err := d.ImageDetails(request)
	require.NoError(t, err)
	require.Nil(t, details)
	d.options.ImageConfigFile = path
	_, err = d.ImageDetails(driver.ImageDetailsRequest{UnsupportedReason: "Features"})
	require.ErrorContains(t, err, "Features")
	write(
		wrapper(
			`{"User":"vscode","Env":["A=b=c"],"Labels":{"devcontainer.metadata":"[]"},"Entrypoint":["a b"],"Cmd":["$x"]}`,
		),
	)
	details, err = d.ImageDetails(request)
	require.NoError(t, err)
	require.Equal(t, testImage, details.ID)
	require.Equal(t, []string{"A=b=c"}, details.Config.Env)
	require.Equal(t, []string{"a b"}, details.Config.Entrypoint)
	require.Equal(t, []string{"$x"}, details.Config.Cmd)
	write(wrapper(`{}`))
	_, err = d.ImageDetails(request)
	require.NoError(t, err)
	exact := wrapper(`{}`)
	write(exact + strings.Repeat(" ", imageConfigLimit-len(exact)))
	_, err = d.ImageDetails(request)
	require.NoError(t, err)
	_, err = d.ImageDetails(
		driver.ImageDetailsRequest{ImageName: "registry.example.com/team/image:latest"},
	)
	require.Error(t, err)
}

func invalidImageDetailsFiles(wrapper func(string) string) map[string]string {
	return map[string]string{
		"missing config": `{"schemaVersion":1}`,
		"null config":    wrapper(`null`),
		"unknown config": wrapper(`{"Bad":1}`),
		"trailing":       wrapper(`{}`) + ` {}`,
		"schema": strings.Replace(
			wrapper(`{}`),
			`"schemaVersion":1`,
			`"schemaVersion":2`,
			1,
		),
		"wrong image":    strings.Replace(wrapper(`{}`), "/team/image@", "/team/other@", 1),
		"wrong platform": strings.Replace(wrapper(`{}`), `"amd64"`, `"arm64"`, 1),
		"metadata":       wrapper(`{"Labels":{"devcontainer.metadata":"[null]"}}`),
		"invalid utf8":   wrapper(`{}`) + string([]byte{0xff}),
		"oversize": wrapper(
			`{}`,
		) + strings.Repeat(
			" ",
			imageConfigLimit+1-len(wrapper(`{}`)),
		),
		"wrong digest": strings.Replace(
			wrapper(`{}`),
			strings.Repeat("a", 64),
			strings.Repeat("b", 64),
			1,
		),
		"tagged file": strings.Replace(
			wrapper(`{}`),
			testImage,
			"registry.example.com/team/image:latest",
			1,
		),
		"non linux":         strings.Replace(wrapper(`{}`), `"linux"`, `"windows"`, 1),
		"null architecture": strings.Replace(wrapper(`{}`), `"amd64"`, `null`, 1),
		"array config":      wrapper(`[]`),
		"wrong env type":    wrapper(`{"Env":[null]}`),
		"unknown wrapper": strings.Replace(
			wrapper(`{}`),
			`"schemaVersion":1`,
			`"schemaVersion":1,"extra":true`,
			1,
		),
		"case alias": strings.Replace(wrapper(`{}`), `"schemaVersion"`, `"SchemaVersion"`, 1),
		"duplicate": strings.Replace(
			wrapper(`{}`),
			`"schemaVersion":1`,
			`"schemaVersion":1,"schemaVersion":1`,
			1,
		),
		"contradictory variant": strings.Replace(
			wrapper(`{}`),
			`"architecture":"amd64"`,
			`"architecture":"amd64","variant":"v8"`,
			1,
		),
	}
}

func TestImageDetailsInvalidFiles(t *testing.T) {
	f := imageDetailsFixture(t)
	d, request, wrapper, write := f.driver, f.request, f.wrapper, f.write
	cases := invalidImageDetailsFiles(wrapper)
	for name, data := range cases {
		t.Run(
			name,
			func(t *testing.T) { write(data); _, err := d.ImageDetails(request); require.Error(t, err) },
		)
	}
}

func TestImageDetailsMetadataAndPaths(t *testing.T) {
	f := imageDetailsFixture(t)
	d, path, request, wrapper, write := f.driver, f.path, f.request, f.wrapper, f.write
	b, err := json.Marshal(map[string]string{"devcontainer.metadata": `{"remoteUser":1}`})
	require.NoError(t, err)
	write(wrapper(`{"Labels":` + string(b) + `}`))
	_, err = d.ImageDetails(request)
	require.ErrorContains(t, err, "devcontainer.metadata")
	write(
		strings.Replace(
			wrapper(
				`{"Labels":{"devcontainer.metadata":"[{\"customizations\":{\"example\":null}}]"}}`,
			),
			`"architecture":"amd64"`,
			`"architecture":"aarch64","variant":"v8"`,
			1,
		),
	)
	d.options.Architecture = "arm64"
	details, err := d.ImageDetails(request)
	require.NoError(t, err)
	require.Equal(t, "arm64", details.Architecture)
	require.Empty(t, details.Variant)
	d.options.Architecture = testArchitecture
	d.options.ImageConfigFile = filepath.Join(filepath.Dir(path), "missing")
	_, err = d.ImageDetails(request)
	require.ErrorContains(t, err, "read image config file")
	d.options.ImageConfigFile = filepath.Dir(path)
	_, err = d.ImageDetails(request)
	require.ErrorContains(t, err, "regular")
	d.options.ImageConfigFile = "relative"
	_, err = d.ImageDetails(request)
	require.ErrorContains(t, err, "absolute")
	d.options.ImageConfigFile = path
	d.options.Architecture = ""
	_, err = d.ImageDetails(request)
	require.ErrorContains(t, err, "ARCHITECTURE")
}
