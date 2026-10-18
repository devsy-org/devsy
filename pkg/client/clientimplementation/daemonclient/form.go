package daemonclient

import (
	"context"
	"fmt"
	"strconv"

	"charm.land/huh/v2"
	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/devsy/cmd/pro/provider/list"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/platform"
	platformclient "github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/form"
	"github.com/devsy-org/devsy/pkg/platform/parameters"
	"github.com/devsy-org/devsy/pkg/platform/project"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func createInstanceInteractive(
	ctx context.Context,
	baseClient platformclient.Client,
	id, uid, source, picture string,
) (*managementv1.DevsyWorkspaceInstance, error) {
	formCtx, cancelForm := context.WithCancel(ctx)
	defer cancelForm()

	selection, err := selectProjectClusterTemplate(ctx, formCtx, baseClient, cancelForm)
	if err != nil {
		return nil, err
	}

	renderedParameters, err := resolveNewInstanceParameters(
		formCtx,
		selection.template,
		selection.templateVersion,
	)
	if err != nil {
		return nil, err
	}

	return buildNewInstance(buildNewInstanceParams{
		id:                      id,
		uid:                     uid,
		source:                  source,
		picture:                 picture,
		selectedProject:         selection.project,
		selectedCluster:         selection.cluster,
		selectedTemplate:        selection.template,
		selectedTemplateVersion: selection.templateVersion,
		renderedParameters:      renderedParameters,
	}), nil
}

type projectClusterTemplateSelection struct {
	project         *managementv1.Project
	cluster         *managementv1.Cluster
	template        *managementv1.DevsyWorkspaceTemplate
	templateVersion string
}

func selectProjectClusterTemplate(
	ctx, formCtx context.Context,
	baseClient platformclient.Client,
	cancelForm form.CancelFunc,
) (projectClusterTemplateSelection, error) {
	var selectedCluster *managementv1.Cluster
	var selectedProject *managementv1.Project
	var selectedTemplate *managementv1.DevsyWorkspaceTemplate
	selectedTemplateVersion := ""

	options, err := form.ProjectOptions(ctx, baseClient)
	if err != nil {
		return projectClusterTemplateSelection{}, err
	}

	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[*managementv1.Project]().
				Title("Project").
				Options(options...).
				Value(&selectedProject),
			huh.NewSelect[*managementv1.Cluster]().
				Title("Cluster").
				OptionsFunc(func() []huh.Option[*managementv1.Cluster] {
					return form.ClusterOptions(ctx, baseClient, selectedProject, cancelForm)
				}, &selectedProject).
				Value(&selectedCluster).
				WithHeight(5),
			huh.NewSelect[*managementv1.DevsyWorkspaceTemplate]().
				Title("Template").
				OptionsFunc(func() []huh.Option[*managementv1.DevsyWorkspaceTemplate] {
					return form.TemplateOptions(ctx, baseClient, selectedProject, cancelForm)
				}, &selectedProject).
				Value(&selectedTemplate),
			huh.NewSelect[string]().
				Title("Template Version").
				OptionsFunc(func() []huh.Option[string] {
					return form.TemplateVersionOptions(selectedTemplate)
				}, &selectedTemplate).
				Value(&selectedTemplateVersion).
				WithHeight(8),
		),
	).RunWithContext(formCtx)
	if err != nil {
		return projectClusterTemplateSelection{}, err
	}

	return projectClusterTemplateSelection{
		project:         selectedProject,
		cluster:         selectedCluster,
		template:        selectedTemplate,
		templateVersion: selectedTemplateVersion,
	}, nil
}

func resolveNewInstanceParameters(
	formCtx context.Context,
	selectedTemplate *managementv1.DevsyWorkspaceTemplate,
	selectedTemplateVersion string,
) (string, error) {
	parameters := selectedTemplate.Spec.Parameters
	if len(selectedTemplate.GetVersions()) > 0 {
		var err error
		parameters, err = list.GetTemplateParameters(selectedTemplate, selectedTemplateVersion)
		if err != nil {
			return "", err
		}
	}
	if len(parameters) == 0 {
		return "", nil
	}

	fieldParameters := prepareParameters(parameters)
	if err := huh.NewForm(
		huh.NewGroup(parameterFields(fieldParameters)...),
	).RunWithContext(formCtx); err != nil {
		return "", err
	}

	return renderParameters(fieldParameters)
}

type buildNewInstanceParams struct {
	id, uid, source, picture string
	selectedProject          *managementv1.Project
	selectedCluster          *managementv1.Cluster
	selectedTemplate         *managementv1.DevsyWorkspaceTemplate
	selectedTemplateVersion  string
	renderedParameters       string
}

func buildNewInstance(p buildNewInstanceParams) *managementv1.DevsyWorkspaceInstance {
	return &managementv1.DevsyWorkspaceInstance{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: encoding.SafeConcatNameMax([]string{p.id}, 53) + "-",
			Namespace:    project.ProjectNamespace(p.selectedProject.GetName()),
			Labels: map[string]string{
				storagev1.DevsyWorkspaceIDLabel:  p.id,
				storagev1.DevsyWorkspaceUIDLabel: p.uid,
				config.K8sProjectLabel:           p.selectedProject.GetName(),
			},
			Annotations: map[string]string{
				storagev1.DevsyWorkspacePictureAnnotation: p.picture,
				storagev1.DevsyWorkspaceSourceAnnotation:  p.source,
			},
		},
		Spec: managementv1.DevsyWorkspaceInstanceSpec{
			DevsyWorkspaceInstanceSpec: storagev1.DevsyWorkspaceInstanceSpec{
				DisplayName: p.id,
				TemplateRef: &storagev1.TemplateRef{
					Name:    p.selectedTemplate.GetName(),
					Version: p.selectedTemplateVersion,
				},
				Target: storagev1.WorkspaceTarget{
					Cluster: &storagev1.WorkspaceTargetName{
						Name: p.selectedCluster.GetName(),
					},
				},
				Parameters: p.renderedParameters,
			},
		},
	}
}

func updateInstanceInteractive(
	ctx context.Context,
	baseClient platformclient.Client,
	instance *managementv1.DevsyWorkspaceInstance,
) (*managementv1.DevsyWorkspaceInstance, error) {
	formCtx, cancelForm := context.WithCancel(ctx)
	defer cancelForm()

	projectName := project.ProjectFromNamespace(instance.GetNamespace())
	projectTemplates, err := list.Templates(ctx, baseClient, projectName)
	if err != nil {
		return nil, err
	}
	templateOptions, selectedTemplate := buildTemplateOptions(
		projectTemplates.DevsyWorkspaceTemplates,
		instance,
	)
	if selectedTemplate == nil {
		return nil, fmt.Errorf("template not found: %#v", instance.Spec.TemplateRef)
	}

	var selectedTemplateVersion string
	if instance.Spec.TemplateRef != nil {
		selectedTemplateVersion = instance.Spec.TemplateRef.Version
	}

	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[*managementv1.DevsyWorkspaceTemplate]().
				Title("Template").
				Options(templateOptions...).
				Value(&selectedTemplate),
			huh.NewSelect[string]().
				Title("Template Version").
				OptionsFunc(func() []huh.Option[string] {
					return form.TemplateVersionOptions(selectedTemplate)
				}, &selectedTemplate).
				Value(&selectedTemplateVersion).
				WithHeight(8),
		),
	).RunWithContext(formCtx)
	if err != nil {
		return nil, err
	}

	renderedParameters, err := resolveInstanceParameters(
		formCtx,
		instance,
		selectedTemplate,
		selectedTemplateVersion,
	)
	if err != nil {
		return nil, err
	}

	return applyInstanceChanges(
		instance,
		selectedTemplate,
		selectedTemplateVersion,
		renderedParameters,
	), nil
}

func buildTemplateOptions(
	templates []managementv1.DevsyWorkspaceTemplate,
	instance *managementv1.DevsyWorkspaceInstance,
) ([]form.TemplateOption, *managementv1.DevsyWorkspaceTemplate) {
	templateOptions := []form.TemplateOption{}
	var selectedTemplate *managementv1.DevsyWorkspaceTemplate
	for _, template := range templates {
		t := &template
		templateOptions = append(templateOptions, huh.Option[*managementv1.DevsyWorkspaceTemplate]{
			Key:   platform.DisplayName(template.GetName(), template.Spec.DisplayName),
			Value: t,
		})

		if instance.Spec.TemplateRef != nil &&
			instance.Spec.TemplateRef.Name == template.GetName() {
			selectedTemplate = t
		}
	}

	return templateOptions, selectedTemplate
}

func resolveInstanceParameters(
	formCtx context.Context,
	instance *managementv1.DevsyWorkspaceInstance,
	selectedTemplate *managementv1.DevsyWorkspaceTemplate,
	selectedTemplateVersion string,
) (string, error) {
	parameters := selectedTemplate.Spec.Parameters
	if len(selectedTemplate.GetVersions()) > 0 {
		var err error
		parameters, err = list.GetTemplateParameters(selectedTemplate, selectedTemplateVersion)
		if err != nil {
			return "", err
		}
	}
	if len(parameters) == 0 {
		return "", nil
	}

	existingParameters, err := existingParametersFor(
		instance,
		selectedTemplate,
		selectedTemplateVersion,
	)
	if err != nil {
		return "", err
	}

	fieldParameters := fieldParametersWithValues(parameters, existingParameters)

	err = huh.NewForm(
		huh.NewGroup(parameterFields(fieldParameters)...),
	).RunWithContext(formCtx)
	if err != nil {
		return "", err
	}

	return renderParameters(fieldParameters)
}

func existingParametersFor(
	instance *managementv1.DevsyWorkspaceInstance,
	selectedTemplate *managementv1.DevsyWorkspaceTemplate,
	selectedTemplateVersion string,
) (map[string]any, error) {
	tRef := instance.Spec.TemplateRef
	if tRef == nil || tRef.Name != selectedTemplate.GetName() ||
		tRef.Version != selectedTemplateVersion {
		return nil, nil
	}

	existingParameters := map[string]any{}
	if err := yaml.Unmarshal([]byte(instance.Spec.Parameters), &existingParameters); err != nil {
		return nil, err
	}

	return existingParameters, nil
}

func fieldParametersWithValues(
	appParameters []storagev1.AppParameter,
	existingParameters map[string]any,
) []*FieldParameter {
	fieldParameters := []*FieldParameter{}
	for _, p := range appParameters {
		var value any = p.DefaultValue
		if existingParameters != nil {
			value = parameters.GetDeepValue(existingParameters, p.Variable)
		}
		fieldParameter := FieldParameter{AppParameter: p}
		assignParameterValue(&fieldParameter, value)
		fieldParameters = append(fieldParameters, &fieldParameter)
	}

	return fieldParameters
}

func assignParameterValue(fieldParameter *FieldParameter, value any) {
	if fieldParameter.Type == paramTypeBoolean {
		assignBoolValue(fieldParameter, value)
		return
	}
	if value == nil {
		fieldParameter.StringValue = fieldParameter.DefaultValue
		return
	}
	if s, ok := value.(string); ok {
		fieldParameter.StringValue = s
	} else {
		fieldParameter.StringValue = fmt.Sprintf("%v", value)
	}
}

func assignBoolValue(fieldParameter *FieldParameter, value any) {
	switch v := value.(type) {
	case bool:
		fieldParameter.BoolValue = v
	case string:
		if b, err := strconv.ParseBool(v); err == nil {
			fieldParameter.BoolValue = b
		}
	default:
		if b, err := strconv.ParseBool(fieldParameter.DefaultValue); err == nil {
			fieldParameter.BoolValue = b
		}
	}
}

func applyInstanceChanges(
	instance *managementv1.DevsyWorkspaceInstance,
	selectedTemplate *managementv1.DevsyWorkspaceTemplate,
	selectedTemplateVersion, renderedParameters string,
) *managementv1.DevsyWorkspaceInstance {
	newInstance := instance.DeepCopy()
	// template
	if instance.Spec.TemplateRef != nil &&
		instance.Spec.TemplateRef.Name != selectedTemplate.GetName() {
		newInstance.Spec.TemplateRef.Name = selectedTemplate.GetName()
	}
	// version
	if instance.Spec.TemplateRef != nil &&
		instance.Spec.TemplateRef.Version != selectedTemplateVersion {
		newInstance.Spec.TemplateRef.Version = selectedTemplateVersion
	}
	// parameters
	if instance.Spec.Parameters != renderedParameters {
		newInstance.Spec.Parameters = renderedParameters
	}

	return newInstance
}

const paramTypeBoolean = "boolean"

type FieldParameter struct {
	storagev1.AppParameter

	StringValue string
	BoolValue   bool
}

func prepareParameters(parameters []storagev1.AppParameter) []*FieldParameter {
	retParams := []*FieldParameter{}
	for _, p := range parameters {
		fieldParameter := FieldParameter{AppParameter: p}
		if p.Type == paramTypeBoolean {
			v, err := strconv.ParseBool(p.DefaultValue)
			if err == nil {
				fieldParameter.BoolValue = v
			}
		} else {
			fieldParameter.StringValue = p.DefaultValue
		}

		retParams = append(retParams, &fieldParameter)
	}

	return retParams
}

func parameterFields(fieldParameters []*FieldParameter) []huh.Field {
	fields := []huh.Field{}
	for _, param := range fieldParameters {
		fields = append(fields, parameterField(param))
	}

	return fields
}

func parameterField(param *FieldParameter) huh.Field {
	title := param.Label
	if title == "" {
		title = param.Variable
	}
	paramType := param.Type
	if paramType == "" {
		paramType = "string"
	}

	switch paramType {
	case "multiline":
		return huh.NewText().
			Title(title).
			Description(param.Description).
			Value(&param.StringValue)
	case "password", "number", "string":
		return stringParameterField(param, title)
	case paramTypeBoolean:
		return huh.NewConfirm().
			Title(title).
			Description(param.Description).
			Value(&param.BoolValue)
	}

	return stringParameterField(param, title)
}

func stringParameterField(param *FieldParameter, title string) huh.Field {
	// display a select field if param has options
	if len(param.Options) > 0 {
		opts := []huh.Option[string]{}
		for _, o := range param.Options {
			huhOption := huh.Option[string]{
				Key:   o,
				Value: o,
			}
			if o == param.DefaultValue {
				huhOption = huhOption.Selected(true)
			}
			opts = append(opts, huhOption)
		}

		return huh.NewSelect[string]().
			Title(title).
			Options(opts...).
			Value(&param.StringValue)
	}

	input := huh.NewInput().Title(title).
		Description(param.Description).
		Value(&param.StringValue)

	if param.Type == "password" {
		input.EchoMode(huh.EchoModePassword)
	}
	if param.Type == "number" {
		input.Validate(func(s string) error {
			_, err := strconv.ParseFloat(s, 64)
			return err
		})
	}

	return input
}

func renderParameters(fieldParameters []*FieldParameter) (string, error) {
	p := map[string]any{}
	for _, fp := range fieldParameters {
		if fp.StringValue != "" {
			p[fp.Variable] = fp.StringValue
		} else if fp.BoolValue {
			p[fp.Variable] = strconv.FormatBool(fp.BoolValue)
		}
	}

	rawParameters, err := yaml.Marshal(p)
	if err != nil {
		return "", err
	}

	return string(rawParameters), nil
}
