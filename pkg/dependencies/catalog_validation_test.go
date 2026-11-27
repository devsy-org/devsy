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
