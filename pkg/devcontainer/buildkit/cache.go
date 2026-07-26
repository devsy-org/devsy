package buildkit

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"

	"github.com/moby/buildkit/client"
)

func ParseCacheEntry(in []string) ([]client.CacheOptionsEntry, error) {
	imports := make([]client.CacheOptionsEntry, 0, len(in))
	for _, in := range in {
		csvReader := csv.NewReader(strings.NewReader(in))
		fields, err := csvReader.Read()
		if err != nil {
			return nil, err
		}
		if isRefOnlyFormat(fields) {
			imports = append(imports, refOnlyEntries(fields)...)
			continue
		}
		im, err := parseCacheFields(fields)
		if err != nil {
			return nil, err
		}
		if im.Type == "" {
			return nil, fmt.Errorf("type required from: %q", in)
		}
		if !addGithubToken(&im) {
			continue
		}
		imports = append(imports, im)
	}
	return imports, nil
}

func refOnlyEntries(fields []string) []client.CacheOptionsEntry {
	entries := make([]client.CacheOptionsEntry, 0, len(fields))
	for _, field := range fields {
		entries = append(entries, client.CacheOptionsEntry{
			Type:  "registry",
			Attrs: map[string]string{"ref": field},
		})
	}
	return entries
}

func parseCacheFields(fields []string) (client.CacheOptionsEntry, error) {
	im := client.CacheOptionsEntry{
		Attrs: map[string]string{},
	}
	for _, field := range fields {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			return client.CacheOptionsEntry{}, fmt.Errorf("invalid value %s", field)
		}
		key := strings.ToLower(parts[0])
		value := parts[1]
		switch key {
		case "type":
			im.Type = value
		default:
			im.Attrs[key] = value
		}
	}
	return im, nil
}

func isRefOnlyFormat(in []string) bool {
	for _, v := range in {
		if strings.Contains(v, "=") {
			return false
		}
	}
	return true
}

func addGithubToken(ci *client.CacheOptionsEntry) bool {
	if ci.Type != "gha" {
		return true
	}
	if _, ok := ci.Attrs["token"]; !ok {
		if v, ok := os.LookupEnv("ACTIONS_RUNTIME_TOKEN"); ok {
			ci.Attrs["token"] = v
		}
	}
	if _, ok := ci.Attrs["url"]; !ok {
		if v, ok := os.LookupEnv("ACTIONS_CACHE_URL"); ok {
			ci.Attrs["url"] = v
		}
	}
	return ci.Attrs["token"] != "" && ci.Attrs["url"] != ""
}
