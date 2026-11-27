package dependencies

import (
	"encoding/json"
	"strings"
)

const (
	wantIdentifiers    = "plain identifiers"
	wantInternalSource = "internal source"
	schemaOne          = `"schemaVersion": 1`
	kindInternal       = `"kind": "internal"`
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

type bindingCase struct {
	mutate func(*Binding)
	want   string
}

func requirementEdit(change func(*Requirement)) func(*Binding) {
	return func(b *Binding) { change(&b.Requirements[0]) }
}

func (s *catalogSuite) TestBindingFieldRulesRejectMalformedBindings() {
	s.assertCasesRejected(map[string]bindingCase{
		"binding id shape": {func(b *Binding) { b.ID = "Docker Desktop" }, "binding id"},
		"execution shape": {
			func(b *Binding) { b.ExecutionContract = "run; sh" },
			"execution contract",
		},
		"execution empty": {func(b *Binding) { b.ExecutionContract = "" }, "execution contract"},
		"adapter range": {
			func(b *Binding) { b.AdapterSemverRange = ">=0.1.11" },
			"adapter range",
		},
		"runtime schema high": {
			func(b *Binding) { b.RequiredRuntimeCheckSchema = 2 }, "runtime-check schema",
		},
		"runtime schema below": {
			func(b *Binding) { b.RequiredRuntimeCheckSchema = -1 }, "runtime-check schema",
		},
		"source kind":         {func(b *Binding) { b.Source.Kind = SourceURL }, "not allowed"},
		"internal without id": {func(b *Binding) { b.Source.InternalID = "" }, wantInternalSource},
		"internal location": {
			func(b *Binding) { b.Source.Location = "/tmp/provider" }, wantInternalSource,
		},
		"internal repository": {
			func(b *Binding) { b.Source.Repository = "github.com/a/b" }, wantInternalSource,
		},
		"no platforms": {
			func(b *Binding) { b.Platforms = []string{} },
			"at least one platform",
		},
		"unknown platform": {
			func(b *Binding) { b.Platforms[0] = "plan9/amd64" },
			"unknown platform",
		},
		"duplicate platform": {
			func(b *Binding) { b.Platforms[1] = b.Platforms[0] },
			"duplicate platform",
		},
	})
}

func (s *catalogSuite) TestRequirementListRulesRejectMalformedBindings() {
	s.assertCasesRejected(map[string]bindingCase{
		"no requirements": {
			func(b *Binding) { b.Requirements = []Requirement{} }, "at least one requirement",
		},
		"duplicate requirement": {
			func(b *Binding) { b.Requirements = append(b.Requirements, b.Requirements[0]) },
			"duplicate requirement",
		},
		"guidance mismatch": {
			func(b *Binding) { b.GuidanceID = "other_setup" },
			"binding guidance",
		},
		"guidance shape": {func(b *Binding) {
			b.GuidanceID, b.Requirements[0].GuidanceID = "bad guide", "bad guide"
		}, wantIdentifiers},
	})
}

func (s *catalogSuite) TestRequirementRulesRejectMalformedRequirements() {
	s.assertCasesRejected(map[string]bindingCase{
		"tool shape": {
			requirementEdit(func(r *Requirement) { r.ToolID = "Docker" }),
			wantIdentifiers,
		},
		"purpose empty": {
			requirementEdit(func(r *Requirement) { r.Purpose = "" }),
			wantIdentifiers,
		},
		"probe shape": {
			requirementEdit(func(r *Requirement) { r.ProbeID = "version; sh" }),
			"probe",
		},
		"scope": {requirementEdit(func(r *Requirement) { r.Scope = "guest" }), "scope"},
		"image backend": {
			requirementEdit(
				func(r *Requirement) { r.WhenImageBackend = toolLima },
			),
			"image backend",
		},
		"option shape": {
			requirementEdit(func(r *Requirement) { r.ExecutableOption = "docker_path" }), "option",
		},
		"configuration option shape": {requirementEdit(func(r *Requirement) {
			r.ExecutableOption, r.ConfigurationOption = "", "kube config"
		}), "option"},
		"both options": {
			requirementEdit(func(r *Requirement) { r.ConfigurationOption = "KUBECONFIG" }), "both",
		},
		"empty operations": {
			requirementEdit(
				func(r *Requirement) { r.RequiredFor = []string{} },
			),
			"operations are required",
		},
		"unknown operation": {
			requirementEdit(
				func(r *Requirement) { r.RequiredFor[0] = "format" },
			),
			"unrecognized operation",
		},
		"duplicate operation": {
			requirementEdit(func(r *Requirement) { r.RequiredFor[1] = r.RequiredFor[0] }),
			"duplicate operation",
		},
	})
}

func (s *catalogSuite) assertCasesRejected(cases map[string]bindingCase) {
	for name, test := range cases {
		s.Run(name, func() {
			s.assertBindingRejected(0, test.mutate, test.want)
		})
	}
}

func (s *catalogSuite) TestGithubSourceRulesRejectMalformedSources() {
	for name, mutate := range map[string]func(*Binding){
		"host":        func(b *Binding) { b.Source.Repository = "example.com/a/b" },
		"internal id": func(b *Binding) { b.Source.InternalID = toolDocker },
		"location":    func(b *Binding) { b.Source.Location = "/tmp/provider" },
		"no repo":     func(b *Binding) { b.Source.Repository = "" },
	} {
		s.Run(name, func() {
			s.assertBindingRejected(4, mutate, "github source")
		})
	}
}

func (s *catalogSuite) assertBindingRejected(index int, mutate func(*Binding), want string) {
	document := s.catalogDocument()
	mutate(&document.Bindings[index])
	data, err := json.Marshal(document)
	s.Require().NoError(err)

	_, err = decodeCatalog(data)
	s.ErrorContains(err, want)
}

func (s *catalogSuite) TestBindingRulesAcceptBoundaryShapes() {
	document := s.catalogDocument()
	binding := &document.Bindings[0]
	binding.Platforms = binding.Platforms[:1]
	binding.Requirements = binding.Requirements[:1]
	binding.Requirements[0].RequiredFor = binding.Requirements[0].RequiredFor[:1]
	data, err := json.Marshal(document)
	s.Require().NoError(err)

	_, err = decodeCatalog(data)
	s.NoError(err)
}

func (s *catalogSuite) catalogDocument() catalogDocument {
	var document catalogDocument
	s.Require().NoError(json.Unmarshal([]byte(embeddedCatalog), &document))
	return document
}
