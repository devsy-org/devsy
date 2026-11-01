package kubernetes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/containerd/platforms"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/google/go-containerregistry/pkg/name"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const imageConfigLimit = 1 << 20

var arm64VariantPattern = regexp.MustCompile(`^v(?:8|9)(?:\.[0-9]+)?$`)

type imageConfigFile struct {
	SchemaVersion int             `json:"schemaVersion"`
	Image         string          `json:"image"`
	OS            string          `json:"os"`
	Architecture  string          `json:"architecture"`
	Variant       string          `json:"variant"`
	Config        json.RawMessage `json:"config"`
}

func (k *KubernetesDriver) ImageDetails(
	request driver.ImageDetailsRequest,
) (*config.ImageDetails, error) {
	path := k.options.ImageConfigFile
	if path == "" {
		return nil, nil
	}
	if request.UnsupportedReason != "" {
		return nil, fmt.Errorf(
			"IMAGE_CONFIG_FILE does not support %s; remove IMAGE_CONFIG_FILE or use a plain prebuilt image",
			request.UnsupportedReason,
		)
	}
	if k.options.Architecture == "" {
		return nil, fmt.Errorf("IMAGE_CONFIG_FILE requires the ARCHITECTURE option to be set")
	}
	details, err := loadImageDetails(path, request.ImageName, k.options.Architecture)
	if err != nil {
		return nil, fmt.Errorf("IMAGE_CONFIG_FILE %q: %w", path, err)
	}
	return details, nil
}

func loadImageDetails(path, imageName, architecture string) (*config.ImageDetails, error) {
	data, err := readImageConfig(path)
	if err != nil {
		return nil, err
	}
	file, payload, err := parseImageConfig(data)
	if err != nil {
		return nil, err
	}
	if err := matchImageReferences(imageName, file.Image); err != nil {
		return nil, err
	}
	platform, err := imageConfigPlatform(file, architecture)
	if err != nil {
		return nil, err
	}
	if err := validateImageMetadata(payload.Labels[metadata.ImageMetadataLabel]); err != nil {
		return nil, err
	}
	return &config.ImageDetails{
		ID: imageName, OS: platform.OS, Architecture: platform.Architecture,
		Variant: platform.Variant, Config: payload,
	}, nil
}

func readImageConfig(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("path must be absolute")
	}
	f, err := openImageConfig(path)
	if err != nil {
		return nil, fmt.Errorf("read image config file: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("image config file must be regular")
	}
	data, err := io.ReadAll(io.LimitReader(f, imageConfigLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read image config file: %w", err)
	}
	if len(data) > imageConfigLimit {
		return nil, fmt.Errorf("image config file exceeds 1 MiB")
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("image config file is not UTF-8")
	}
	return data, nil
}

func parseImageConfig(data []byte) (imageConfigFile, config.ImageDetailsConfig, error) {
	var file imageConfigFile
	var payload config.ImageDetailsConfig
	if err := decodeObject(data, &file); err != nil || !validWrapper(data) {
		return file, payload, fmt.Errorf("invalid wrapper")
	}
	if file.SchemaVersion != 1 {
		return file, payload, fmt.Errorf("unsupported schemaVersion")
	}
	if err := decodeObject(file.Config, &payload); err != nil || !validPayload(file.Config) {
		return file, payload, fmt.Errorf("invalid config object")
	}
	return file, payload, nil
}

func matchImageReferences(imageName, fileImage string) error {
	requested, err := pinnedReference(imageName)
	if err != nil {
		return fmt.Errorf("requested image must be a fully qualified digest reference")
	}
	supplied, err := pinnedReference(fileImage)
	if err != nil {
		return fmt.Errorf("image must be a fully qualified digest reference")
	}
	if requested.Name() != supplied.Name() {
		return fmt.Errorf("image repository or digest does not match requested image")
	}
	return nil
}

func imageConfigPlatform(file imageConfigFile, architecture string) (ocispec.Platform, error) {
	platform := platforms.Normalize(
		ocispec.Platform{OS: file.OS, Architecture: file.Architecture, Variant: file.Variant},
	)
	target := platforms.Normalize(ocispec.Platform{OS: "linux", Architecture: architecture})
	if file.OS == "" || platform.OS != "linux" || platform.Architecture == "" ||
		platform.Architecture != target.Architecture || !validVariant(platform.Architecture, platform.Variant) {
		return platform, fmt.Errorf("image platform does not match Linux ARCHITECTURE")
	}
	return platform, nil
}

func pinnedReference(ref string) (name.Digest, error) {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 || (!strings.ContainsAny(parts[0], ".:") && parts[0] != "localhost") {
		return name.Digest{}, fmt.Errorf("missing registry")
	}
	return name.NewDigest(ref, name.StrictValidation)
}

func decodeObject(data []byte, out any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("expected object")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing data")
	}
	return nil
}

func validateImageMetadata(raw string) error {
	if raw == "" {
		return nil
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil || value == nil {
		return fmt.Errorf("invalid %s label", metadata.ImageMetadataLabel)
	}
	entries, ok := value.([]any)
	if !ok {
		entries = []any{value}
	}
	for _, entry := range entries {
		if !validMetadataEntry(entry) {
			return fmt.Errorf("invalid %s label", metadata.ImageMetadataLabel)
		}
	}
	return nil
}

func validMetadataEntry(entry any) bool {
	if _, ok := entry.(map[string]any); !ok {
		return false
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return false
	}
	var item config.ImageMetadata
	return json.Unmarshal(b, &item) == nil
}

func validWrapper(data []byte) bool {
	if !uniqueJSONKeys(data) {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return false
	}
	for key, value := range fields {
		if !validWrapperField(key, value) {
			return false
		}
	}
	for _, key := range []string{"schemaVersion", "image", "os", "architecture", "config"} {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	return true
}

func validWrapperField(key string, value json.RawMessage) bool {
	switch key {
	case "schemaVersion":
		var version int
		return !bytes.Equal(bytes.TrimSpace(value), []byte("null")) &&
			json.Unmarshal(value, &version) == nil
	case "image", "os", "architecture", "variant":
		return isJSONString(value)
	case "config":
		return startsWith(value, '{')
	default:
		return false
	}
}

func validPayload(data []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return false
	}
	for key, value := range fields {
		if !validPayloadField(key, value) {
			return false
		}
	}
	return true
}

func validPayloadField(key string, value json.RawMessage) bool {
	switch key {
	case "User":
		return isJSONString(value)
	case "Env", "Entrypoint", "Cmd":
		return isStringArray(value)
	case "Labels":
		return isStringMap(value)
	default:
		return false
	}
}

func startsWith(value json.RawMessage, prefix byte) bool {
	value = bytes.TrimSpace(value)
	return len(value) > 0 && value[0] == prefix
}

func isStringArray(value json.RawMessage) bool {
	var values []json.RawMessage
	if !startsWith(value, '[') || json.Unmarshal(value, &values) != nil {
		return false
	}
	for _, v := range values {
		if !isJSONString(v) {
			return false
		}
	}
	return true
}

func isStringMap(value json.RawMessage) bool {
	var values map[string]json.RawMessage
	if !startsWith(value, '{') || json.Unmarshal(value, &values) != nil {
		return false
	}
	for _, v := range values {
		if !isJSONString(v) {
			return false
		}
	}
	return true
}

func isJSONString(value json.RawMessage) bool {
	var s string
	return len(bytes.TrimSpace(value)) > 0 && bytes.TrimSpace(value)[0] == '"' &&
		json.Unmarshal(value, &s) == nil
}

func uniqueJSONKeys(data []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	return uniqueJSONValue(dec) == nil
}

func uniqueJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		err = uniqueJSONObject(dec)
	case '[':
		err = uniqueJSONArray(dec)
	default:
		return fmt.Errorf("invalid JSON")
	}
	if err != nil {
		return err
	}
	_, err = dec.Token()
	return err
}

func uniqueJSONObject(dec *json.Decoder) error {
	seen := map[string]bool{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return fmt.Errorf("duplicate key")
		}
		seen[key] = true
		if err := uniqueJSONValue(dec); err != nil {
			return err
		}
	}
	return nil
}

func uniqueJSONArray(dec *json.Decoder) error {
	for dec.More() {
		if err := uniqueJSONValue(dec); err != nil {
			return err
		}
	}
	return nil
}

func validVariant(arch, variant string) bool {
	if variant == "" {
		return true
	}
	switch arch {
	case "amd64":
		return variantIn(variant, "v2", "v3", "v4")
	case "arm":
		return variantIn(variant, "v5", "v6", "v7", "v8")
	case "arm64":
		return arm64VariantPattern.MatchString(variant)
	default:
		return true
	}
}

func variantIn(variant string, allowed ...string) bool {
	return slices.Contains(allowed, variant)
}
