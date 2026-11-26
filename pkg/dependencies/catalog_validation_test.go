package dependencies

import (
	"encoding/json"
	"strings"
)

const (
	schemaOne    = `"schemaVersion": 1`
	kindInternal = `"kind": "internal"`
)

func (s *catalogSuite) TestStrictCatalogJSON() {
	cases := map[string][2]string{
		"unknown top level":    {schemaOne, `"schemaVersion": 1, "postinstall": "sh"`},
		"unknown nested":       {kindInternal, `"kind": "internal", "shell": "curl"`},
		"duplicate key":        {schemaOne, `"schemaVersion": 2, "schemaVersion": 1`},
		"duplicate nested key": {kindInternal, `"kind": "file", "kind": "internal"`},
		"null metadata":        {`"adapterSemverRange": ""`, `"adapterSemverRange": null`},
		"case duplicate":       {schemaOne, `"schemaVersion": 2, "SCHEMAVERSION": 1`},
		"case duplicate nested": {
			kindInternal, `"kind": "file", "Kind": "internal"`,
		},
		"wrong case top level": {schemaOne, `"SchemaVersion": 1`},
		"wrong case nested":    {kindInternal, `"KIND": "internal"`},
		"wrong case in list":   {`"toolId": "docker"`, `"toolID": "docker"`},
		"folded letter":        {schemaOne, `"\u017fchemaVersion": 1`},
		"kelvin sign":          {kindInternal, `"\u212aind": "internal"`},
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
		"schema":            func(d *catalogDocument) { d.SchemaVersion = 2 },
		"identity":          func(d *catalogDocument) { d.ID = testCustom },
		"managed status":    func(d *catalogDocument) { d.ManagedStatus = "qualified" },
		"missing binding":   func(d *catalogDocument) { d.Bindings = d.Bindings[:1] },
		"duplicate binding": func(d *catalogDocument) { d.Bindings[1] = d.Bindings[0] },
		"custom binding":    func(d *catalogDocument) { d.Bindings[0].ID = testCustom },
		"source kind":       func(d *catalogDocument) { d.Bindings[0].Source.Kind = SourceGitHub },
		"source spoof":      func(d *catalogDocument) { d.Bindings[4].Source.Repository += "-evil" },
		"adapter range":     func(d *catalogDocument) { d.Bindings[4].AdapterSemverRange = ">=0.1.11" },
		"runtime schema":    func(d *catalogDocument) { d.Bindings[4].RequiredRuntimeCheckSchema = 2 },
		"execution":         func(d *catalogDocument) { d.Bindings[4].ExecutionContract = testShell },
		"platform":          func(d *catalogDocument) { d.Bindings[0].Platforms[0] = "plan9/amd64" },
		"apple platform":    func(d *catalogDocument) { d.Bindings[2].Platforms = []string{platformLinuxAMD64} },
		"microsandbox without windows": func(d *catalogDocument) {
			d.Bindings[4].Platforms = d.Bindings[4].Platforms[:3]
		},
		"microsandbox extra platform": func(d *catalogDocument) {
			d.Bindings[4].Platforms = append(d.Bindings[4].Platforms, "windows/arm64")
		},
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

func (s *catalogSuite) catalogDocument() catalogDocument {
	var document catalogDocument
	s.Require().NoError(json.Unmarshal([]byte(embeddedCatalog), &document))
	return document
}
