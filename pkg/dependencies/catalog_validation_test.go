package dependencies

import (
	"encoding/json"
	"strings"
)

func (s *catalogSuite) TestStrictCatalogJSON() {
	cases := map[string][2]string{
		"unknown top level":    {`"schemaVersion": 1`, `"schemaVersion": 1, "postinstall": "sh"`},
		"unknown nested":       {`"kind": "internal"`, `"kind": "internal", "shell": "curl"`},
		"duplicate key":        {`"schemaVersion": 1`, `"schemaVersion": 2, "schemaVersion": 1`},
		"duplicate nested key": {`"kind": "internal"`, `"kind": "file", "kind": "internal"`},
		"null metadata":        {`"adapterSemverRange": ""`, `"adapterSemverRange": null`},
	}
	for name, fields := range cases {
		s.Run(name, func() {
			data := strings.Replace(embeddedCatalog, fields[0], fields[1], 1)
			s.Require().True(json.Valid([]byte(data)))
			_, err := decodeCatalog([]byte(data))
			s.Error(err)
		})
	}
	for _, data := range []string{embeddedCatalog + `{}`, embeddedCatalog + `garbage`, `null`} {
		_, err := decodeCatalog([]byte(data))
		s.Error(err)
	}
}

func (s *catalogSuite) TestInvalidCatalogMetadata() {
	cases := map[string]func(*catalogDocument){
		"schema":              func(d *catalogDocument) { d.SchemaVersion = 2 },
		"identity":            func(d *catalogDocument) { d.ID = testCustom },
		"managed status":      func(d *catalogDocument) { d.ManagedStatus = "qualified" },
		"missing recipes":     func(d *catalogDocument) { d.Recipes = nil },
		"missing binding":     func(d *catalogDocument) { d.Bindings = d.Bindings[:1] },
		"duplicate binding":   func(d *catalogDocument) { d.Bindings[1] = d.Bindings[0] },
		"custom binding":      func(d *catalogDocument) { d.Bindings[0].ID = testCustom },
		"source kind":         func(d *catalogDocument) { d.Bindings[0].Source.Kind = SourceGitHub },
		"source spoof":        func(d *catalogDocument) { d.Bindings[4].Source.Repository += "-evil" },
		"adapter range":       func(d *catalogDocument) { d.Bindings[4].AdapterSemverRange = ">=0.1.11" },
		"runtime schema":      func(d *catalogDocument) { d.Bindings[4].RequiredRuntimeCheckSchema = 2 },
		"execution":           func(d *catalogDocument) { d.Bindings[4].ExecutionContract = testShell },
		"platform":            func(d *catalogDocument) { d.Bindings[0].Platforms[0] = "plan9/amd64" },
		"apple platform":      func(d *catalogDocument) { d.Bindings[2].Platforms = []string{platformLinuxAMD64} },
		"guidance":            func(d *catalogDocument) { d.Bindings[0].GuidanceID = "curl_install" },
		"missing requirement": func(d *catalogDocument) { d.Bindings[0].Requirements = nil },
		"option":              func(d *catalogDocument) { d.Bindings[0].Requirements[0].ExecutableOption = "SHELL" },
		"probe":               func(d *catalogDocument) { d.Bindings[0].Requirements[0].ProbeID = "docker_start" },
		"operation":           func(d *catalogDocument) { d.Bindings[0].Requirements[0].RequiredFor[0] = testShell },
		"scope":               func(d *catalogDocument) { d.Bindings[5].Requirements[0].Scope = "guest" },
		"purpose":             func(d *catalogDocument) { d.Bindings[5].Requirements[0].Purpose = "container_runtime" },
		"kubernetes exec":     func(d *catalogDocument) { d.Bindings[3].Requirements[0].ProbeID = "exec_auth" },
	}
	for name, mutate := range cases {
		s.Run(name, func() {
			document := s.catalogDocument()
			mutate(&document)
			data, err := json.Marshal(document)
			s.Require().NoError(err)
			_, err = decodeCatalog(data)
			s.Error(err)
		})
	}
}

func (s *catalogSuite) TestSourceRecordedRecipeIsDisabledAndCopied() {
	document := s.catalogDocument()
	document.Recipes = []Recipe{sourceRecordedCandidate()}
	data, err := json.Marshal(document)
	s.Require().NoError(err)
	catalog, err := decodeCatalog(data)
	s.Require().NoError(err)
	recipes := catalog.Recipes()
	s.Require().Len(recipes, 1)
	s.False(recipes[0].Enabled)
	s.Equal("v0.7.7", recipes[0].RuntimeVersion)
	s.Equal("source_recorded_unqualified", recipes[0].QualificationStatus)
	recipes[0].Enabled = true
	recipes[0].Members[0].Name = "../msb"
	recipes[0].Prerequisites[0] = imageBackendNone
	recipes[0].Actions[0] = testShell
	s.Equal(document.Recipes, catalog.Recipes())
	match, err := catalog.Match(ProviderContract{
		Source: externalSource(
			toolMicrosandbox,
		),
		ExecutionContract: executionMicrosandbox,
		AdapterVersion:    "v0.1.11",
	})
	s.Require().NoError(err)
	s.False(match.ManagedEligible)
}

func (s *catalogSuite) TestInvalidRecipes() {
	for name, mutate := range invalidRecipeCases() {
		s.Run(name, func() {
			document := s.catalogDocument()
			recipe := sourceRecordedCandidate()
			mutate(&recipe)
			document.Recipes = []Recipe{recipe}
			data, err := json.Marshal(document)
			s.Require().NoError(err)
			_, err = decodeCatalog(data)
			s.Error(err)
		})
	}
}

func (s *catalogSuite) TestRecipeCannotProvideCustomInstructions() {
	document := s.catalogDocument()
	document.Recipes = []Recipe{sourceRecordedCandidate()}
	data, err := json.Marshal(document)
	s.Require().NoError(err)
	modified := strings.Replace(
		string(data),
		`"enabled":false`,
		`"enabled":false,"postinstall":["sh"]`,
		1,
	)
	s.Require().NotEqual(string(data), modified)
	_, err = decodeCatalog([]byte(modified))
	s.ErrorContains(err, "unknown field")
}

func invalidRecipeCases() map[string]func(*Recipe) {
	return map[string]func(*Recipe){
		"enabled":                 func(r *Recipe) { r.Enabled = true },
		"identity":                func(r *Recipe) { r.ID = "../custom" },
		"tool":                    func(r *Recipe) { r.ToolID = toolDocker },
		"platform":                func(r *Recipe) { r.Platform = "linux/arm64" },
		"unknown platform":        func(r *Recipe) { r.Platform = "plan9/amd64" },
		"runtime version":         func(r *Recipe) { r.RuntimeVersion = "latest" },
		"digest absent":           func(r *Recipe) { r.SHA256 = "" },
		"digest short":            func(r *Recipe) { r.SHA256 = "b3cc4a5e" },
		"digest nonhex":           func(r *Recipe) { r.SHA256 = strings.Repeat("g", 64) },
		"digest prefixed":         func(r *Recipe) { r.SHA256 = "sha256:" + r.SHA256 },
		"archive":                 func(r *Recipe) { r.ArchiveFormat = "exe" },
		"layout":                  func(r *Recipe) { r.Layout = "bin_lib_links" },
		"zero archive size":       func(r *Recipe) { r.MaxArchiveBytes = 0 },
		"unbounded archive":       func(r *Recipe) { r.MaxArchiveBytes = maxCatalogArchiveBytes + 1 },
		"negative expanded size":  func(r *Recipe) { r.MaxExpandedBytes = -1 },
		"unbounded expanded size": func(r *Recipe) { r.MaxExpandedBytes = maxCatalogArchiveBytes + 1 },
		"missing member":          func(r *Recipe) { r.Members = r.Members[:1] },
		"duplicate member":        func(r *Recipe) { r.Members[1] = r.Members[0] },
		"traversal":               func(r *Recipe) { r.Members[0].Name = "../msb" },
		"absolute member":         func(r *Recipe) { r.Members[0].Name = "/msb" },
		"nested member":           func(r *Recipe) { r.Members[0].Name = "bin/msb" },
		"symlink":                 func(r *Recipe) { r.Members[0].Type = "symlink" },
		"hardlink":                func(r *Recipe) { r.Members[0].Type = "hardlink" },
		"zero member size":        func(r *Recipe) { r.Members[0].MaxBytes = 0 },
		"unbounded member":        func(r *Recipe) { r.Members[0].MaxBytes = maxCatalogArchiveBytes + 1 },
		"total member size":       func(r *Recipe) { r.Members[1].MaxBytes = r.MaxExpandedBytes },
		"firmware":                func(r *Recipe) { r.Firmware = "libkrunfw.so" },
		"executable":              func(r *Recipe) { r.Executable = "sh" },
		"probe":                   func(r *Recipe) { r.ProbeID = "runtime_start" },
		"prerequisites":           func(r *Recipe) { r.Prerequisites = []string{testShell} },
		"action":                  func(r *Recipe) { r.Actions = []string{"curl | sh"} },
		"qualification":           func(r *Recipe) { r.QualificationStatus = "qualified" },
		"qualification record":    func(r *Recipe) { r.QualificationRecord = "synthetic-evidence" },
	}
}

func (s *catalogSuite) TestUnsafeRecipeURLs() {
	base := sourceRecordedCandidate()
	urls := []string{
		strings.Replace(base.URL, "https:", "http:", 1),
		strings.Replace(base.URL, "github.com", "github.com.evil", 1),
		strings.Replace(base.URL, "github.com", "user:password@github.com", 1),
		strings.Replace(base.URL, "github.com", "github.com:443", 1),
		strings.Replace(base.URL, "superradcompany", "evil", 1),
		strings.Replace(base.URL, "/v0.7.7/", "/latest/", 1),
		strings.Replace(base.URL, "/v0.7.7/", "/main/", 1),
		strings.Replace(base.URL, "/v0.7.7/", "/v0.7.8/", 1),
		strings.Replace(base.URL, "microsandbox-linux", "microsandbox%2flinux", 1),
		base.URL + "?token=secret", base.URL + "?", base.URL + "#fragment", base.URL + "/suffix",
		"https://github.com/superradcompany/microsandbox/releases/download/v0.7.7/install.sh",
	}
	for _, assetURL := range urls {
		recipe := base
		recipe.URL = assetURL
		s.Error(validateRecipe(recipe), assetURL)
	}
}

func (s *catalogSuite) TestDuplicateRecipeIdentities() {
	for _, field := range []string{"id", "tuple", "url", "digest"} {
		a := sourceRecordedCandidate()
		b := sourceRecordedCandidate()
		b.ID = "second"
		b.RuntimeVersion = "v0.7.8"
		b.URL = strings.Replace(b.URL, "v0.7.7", b.RuntimeVersion, 1)
		b.SHA256 = strings.Repeat("a", 64)
		switch field {
		case "id":
			b.ID = a.ID
		case "tuple":
			b.RuntimeVersion = a.RuntimeVersion
		case "url":
			b.URL = a.URL
		case "digest":
			b.SHA256 = a.SHA256
		}
		s.Error(validateRecipes([]Recipe{a, b}), field)
	}
}

func (s *catalogSuite) catalogDocument() catalogDocument {
	var document catalogDocument
	s.Require().NoError(json.Unmarshal([]byte(embeddedCatalog), &document))
	return document
}

// Source-recorded SPEC 9.2 candidate only; these tests neither download nor qualify this artifact.
func sourceRecordedCandidate() Recipe {
	return Recipe{
		ID:             "microsandbox-0.7.7-linux-amd64",
		ToolID:         toolMicrosandbox,
		RuntimeVersion: "v0.7.7",
		Platform:       platformLinuxAMD64,
		URL: "https://github.com/superradcompany/microsandbox/releases/download/" +
			"v0.7.7/microsandbox-linux-x86_64.tar.gz",
		SHA256:           "b3cc4a5e3f52dfdd938a6f67ac4a9a959ddfe304bab56de4964044b8613f01bb",
		ArchiveFormat:    "tar.gz",
		Layout:           "flat_verbatim_no_links",
		MaxArchiveBytes:  1 << 29,
		MaxExpandedBytes: 1 << 30,
		Members: []ArchiveMember{
			{Name: "msb", Type: "regular", MaxBytes: 1 << 29},
			{Name: "libkrunfw.so.5.6.1", Type: "regular", MaxBytes: 1 << 29},
		},
		Executable:          "msb",
		Firmware:            "libkrunfw.so.5.6.1",
		Prerequisites:       []string{"kvm"},
		ProbeID:             "microsandbox_version",
		Actions:             []string{"install", "select", "remove"},
		QualificationStatus: "source_recorded_unqualified",
	}
}
