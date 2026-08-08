package form

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/devsy/cmd/pro/provider/list"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/project"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const parameterTypeBoolean = "boolean"

func CreateInstance(
	ctx context.Context,
	baseClient client.Client,
	id, uid, source, picture string,
) (*managementv1.DevsyWorkspaceInstance, error) {
	formCtx, cancelForm := context.WithCancel(ctx)
	defer cancelForm()

	selection, err := runCreateSelectionForm(ctx, baseClient, formCtx, cancelForm)
	if err != nil {
		return nil, err
	}

	renderedParameters, err := renderedParametersForCreate(
		formCtx,
		selection.template,
		selection.templateVersion,
	)
	if err != nil {
		return nil, err
	}

	return buildCreatedInstance(buildCreatedInstanceParams{
		id:                      id,
		uid:                     uid,
		source:                  source,
		picture:                 picture,
		selectedProject:         selection.project,
		selectedTemplate:        selection.template,
		selectedTemplateVersion: selection.templateVersion,
		renderedParameters:      renderedParameters,
	}), nil
}

type createSelection struct {
	project         *managementv1.Project
	template        *managementv1.DevsyWorkspaceTemplate
	templateVersion string
}

func runCreateSelectionForm(
	ctx context.Context,
	baseClient client.Client,
	formCtx context.Context,
	cancelForm CancelFunc,
) (createSelection, error) {
	var selectedCluster *managementv1.Cluster
	var selectedProject *managementv1.Project
	var selectedTemplate *managementv1.DevsyWorkspaceTemplate
	selectedTemplateVersion := ""
	projectOptions, err := projectOptions(ctx, baseClient)
	if err != nil {
		return createSelection{}, err
	}
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[*managementv1.Project]().
				Title("Project").
				Options(projectOptions...).
				Value(&selectedProject),
			huh.NewSelect[*managementv1.Cluster]().
				Title("Cluster").
				OptionsFunc(func() []huh.Option[*managementv1.Cluster] {
					return getClusterOptions(ctx, baseClient, selectedProject, cancelForm)
				}, &selectedProject).
				Value(&selectedCluster).
				WithHeight(5),
			huh.NewSelect[*managementv1.DevsyWorkspaceTemplate]().
				Title("Template").
				OptionsFunc(func() []huh.Option[*managementv1.DevsyWorkspaceTemplate] {
					return getTemplateOptions(ctx, baseClient, selectedProject, cancelForm)
				}, &selectedProject).
				Value(&selectedTemplate),
			huh.NewSelect[string]().
				Title("Template Version").
				OptionsFunc(func() []huh.Option[string] {
					return getTemplateVersionOptions(selectedTemplate)
				}, &selectedTemplate).
				Value(&selectedTemplateVersion).
				WithHeight(8),
		),
	).RunWithContext(formCtx)
	if err != nil {
		return createSelection{}, err
	}

	return createSelection{
		project:         selectedProject,
		template:        selectedTemplate,
		templateVersion: selectedTemplateVersion,
	}, nil
}

func renderedParametersForCreate(
	formCtx context.Context,
	selectedTemplate *managementv1.DevsyWorkspaceTemplate,
	selectedTemplateVersion string,
) (string, error) {
	parameters, err := resolveTemplateParameters(selectedTemplate, selectedTemplateVersion)
	if err != nil {
		return "", err
	}
	if len(parameters) == 0 {
		return "", nil
	}

	fieldParameters := prepareParameters(parameters)
	return runParameterForm(formCtx, fieldParameters)
}

type buildCreatedInstanceParams struct {
	id, uid, source, picture string
	selectedProject          *managementv1.Project
	selectedTemplate         *managementv1.DevsyWorkspaceTemplate
	selectedTemplateVersion  string
	renderedParameters       string
}

func buildCreatedInstance(p buildCreatedInstanceParams) *managementv1.DevsyWorkspaceInstance {
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
				Parameters: p.renderedParameters,
			},
		},
	}
}

func resolveTemplateParameters(
	selectedTemplate *managementv1.DevsyWorkspaceTemplate,
	selectedTemplateVersion string,
) ([]storagev1.AppParameter, error) {
	parameters := selectedTemplate.Spec.Parameters
	if len(selectedTemplate.GetVersions()) > 0 {
		var err error
		parameters, err = list.GetTemplateParameters(selectedTemplate, selectedTemplateVersion)
		if err != nil {
			return nil, err
		}
	}

	return parameters, nil
}

func runParameterForm(formCtx context.Context, fieldParameters []*FieldParameter) (string, error) {
	if err := huh.NewForm(
		huh.NewGroup(parameterFields(fieldParameters)...),
	).RunWithContext(formCtx); err != nil {
		return "", err
	}

	return renderParameters(fieldParameters)
}

func UpdateInstance(
	ctx context.Context,
	baseClient client.Client,
	instance *managementv1.DevsyWorkspaceInstance,
) (*managementv1.DevsyWorkspaceInstance, error) {
	formCtx, cancelForm := context.WithCancel(ctx)
	defer cancelForm()

	selectedTemplate, selectedTemplateVersion, err := selectUpdateTemplate(
		ctx,
		baseClient,
		formCtx,
		instance,
	)
	if err != nil {
		return nil, err
	}

	renderedParameters, err := renderedParametersForUpdate(
		formCtx,
		instance,
		selectedTemplate,
		selectedTemplateVersion,
	)
	if err != nil {
		return nil, err
	}

	newInstance := instance.DeepCopy()
	applyInstanceChanges(applyInstanceChangesParams{
		instance:                instance,
		newInstance:             newInstance,
		selectedTemplate:        selectedTemplate,
		selectedTemplateVersion: selectedTemplateVersion,
		renderedParameters:      renderedParameters,
	})

	return newInstance, nil
}

func selectUpdateTemplate(
	ctx context.Context,
	baseClient client.Client,
	formCtx context.Context,
	instance *managementv1.DevsyWorkspaceInstance,
) (*managementv1.DevsyWorkspaceTemplate, string, error) {
	projectName := project.ProjectFromNamespace(instance.GetNamespace())
	projectTemplates, err := list.Templates(ctx, baseClient, projectName)
	if err != nil {
		return nil, "", err
	}
	templateOptions, selectedTemplate := templateOptionsForInstance(
		projectTemplates.DevsyWorkspaceTemplates,
		instance,
	)
	if selectedTemplate == nil {
		return nil, "", fmt.Errorf("template not found: %#v", instance.Spec.TemplateRef)
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
					return getTemplateVersionOptions(selectedTemplate)
				}, &selectedTemplate).
				Value(&selectedTemplateVersion).
				WithHeight(8),
		),
	).RunWithContext(formCtx)
	if err != nil {
		return nil, "", err
	}

	return selectedTemplate, selectedTemplateVersion, nil
}

func templateOptionsForInstance(
	templates []managementv1.DevsyWorkspaceTemplate,
	instance *managementv1.DevsyWorkspaceInstance,
) ([]TemplateOption, *managementv1.DevsyWorkspaceTemplate) {
	var selectedTemplate *managementv1.DevsyWorkspaceTemplate
	templateOptions := []TemplateOption{}
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

func renderedParametersForUpdate(
	formCtx context.Context,
	instance *managementv1.DevsyWorkspaceInstance,
	selectedTemplate *managementv1.DevsyWorkspaceTemplate,
	selectedTemplateVersion string,
) (string, error) {
	parameters, err := resolveTemplateParameters(selectedTemplate, selectedTemplateVersion)
	if err != nil {
		return "", err
	}
	if len(parameters) == 0 {
		return "", nil
	}

	fieldParameters, err := buildFieldParameters(
		parameters,
		instance,
		selectedTemplate,
		selectedTemplateVersion,
	)
	if err != nil {
		return "", err
	}

	return runParameterForm(formCtx, fieldParameters)
}

func buildFieldParameters(
	parameters []storagev1.AppParameter,
	instance *managementv1.DevsyWorkspaceInstance,
	selectedTemplate *managementv1.DevsyWorkspaceTemplate,
	selectedTemplateVersion string,
) ([]*FieldParameter, error) {
	tRef := instance.Spec.TemplateRef
	var existingParameters map[string]any
	if tRef != nil && tRef.Name == selectedTemplate.GetName() &&
		tRef.Version == selectedTemplateVersion {
		existingParameters = map[string]any{}
		if err := yaml.Unmarshal(
			[]byte(instance.Spec.Parameters),
			&existingParameters,
		); err != nil {
			return nil, err
		}
	}

	fieldParameters := []*FieldParameter{}
	// reuse existing parameters as starting point
	for _, p := range parameters {
		var value any = p.DefaultValue
		if existingParameters != nil {
			value = getDeepValue(existingParameters, p.Variable)
		}
		fieldParameter := FieldParameter{AppParameter: p}
		assignFieldValue(&fieldParameter, value)
		fieldParameters = append(fieldParameters, &fieldParameter)
	}

	return fieldParameters, nil
}

func assignFieldValue(fieldParameter *FieldParameter, value any) {
	if fieldParameter.Type == parameterTypeBoolean {
		assignBoolFieldValue(fieldParameter, value)
		return
	}

	if s, ok := value.(string); ok {
		fieldParameter.StringValue = s
	} else if value != nil {
		fieldParameter.StringValue = fmt.Sprintf("%v", value)
	} else {
		fieldParameter.StringValue = fieldParameter.DefaultValue
	}
}

func assignBoolFieldValue(fieldParameter *FieldParameter, value any) {
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

type applyInstanceChangesParams struct {
	instance                *managementv1.DevsyWorkspaceInstance
	newInstance             *managementv1.DevsyWorkspaceInstance
	selectedTemplate        *managementv1.DevsyWorkspaceTemplate
	selectedTemplateVersion string
	renderedParameters      string
}

func applyInstanceChanges(params applyInstanceChangesParams) {
	instance := params.instance
	newInstance := params.newInstance
	// template
	if instance.Spec.TemplateRef != nil &&
		instance.Spec.TemplateRef.Name != params.selectedTemplate.GetName() {
		newInstance.Spec.TemplateRef.Name = params.selectedTemplate.GetName()
	}
	// version
	if instance.Spec.TemplateRef != nil &&
		instance.Spec.TemplateRef.Version != params.selectedTemplateVersion {
		newInstance.Spec.TemplateRef.Version = params.selectedTemplateVersion
	}
	// parameters
	if instance.Spec.Parameters != params.renderedParameters {
		newInstance.Spec.Parameters = params.renderedParameters
	}
}

type (
	ProjectOption  = huh.Option[*managementv1.Project]
	TemplateOption = huh.Option[*managementv1.DevsyWorkspaceTemplate]
	CancelFunc     = func()
)

var latestTemplateVersion = huh.Option[string]{
	Key:   "latest",
	Value: "",
}

func projectOptions(ctx context.Context, client client.Client) ([]ProjectOption, error) {
	projects, err := list.Projects(ctx, client)
	if err != nil {
		return nil, err
	}
	projectOptions := []ProjectOption{}
	for _, project := range projects.Items {
		p := &project
		projectOptions = append(projectOptions, ProjectOption{
			Key:   platform.DisplayName(project.GetName(), project.Spec.DisplayName),
			Value: p,
		})
	}

	return projectOptions, nil
}

func getClusterOptions(
	ctx context.Context,
	client client.Client,
	project *managementv1.Project,
	cancel CancelFunc,
) []huh.Option[*managementv1.Cluster] {
	opts := []huh.Option[*managementv1.Cluster]{}
	if project == nil {
		return opts
	}

	clusters, err := list.Clusters(ctx, client, project.GetName())
	if err != nil {
		log.Error(err)
		cancel()

		return nil
	}
	for _, cluster := range clusters.Clusters {
		r := &cluster
		opts = append(opts, huh.Option[*managementv1.Cluster]{
			Key:   platform.DisplayName(cluster.GetName(), cluster.Spec.DisplayName),
			Value: r,
		})
	}

	return opts
}

func getTemplateOptions(
	ctx context.Context,
	client client.Client,
	project *managementv1.Project,
	cancel CancelFunc,
) []huh.Option[*managementv1.DevsyWorkspaceTemplate] {
	opts := []huh.Option[*managementv1.DevsyWorkspaceTemplate]{}
	if project == nil {
		return opts
	}

	templates, err := list.Templates(ctx, client, project.GetName())
	if err != nil {
		log.Error(err)
		cancel()

		return nil
	}

	var defaultOpt huh.Option[*managementv1.DevsyWorkspaceTemplate]
	for _, template := range templates.DevsyWorkspaceTemplates {
		t := &template
		opt := huh.Option[*managementv1.DevsyWorkspaceTemplate]{
			Key:   platform.DisplayName(template.GetName(), template.Spec.DisplayName),
			Value: t,
		}
		if t.GetName() == templates.DefaultDevsyWorkspaceTemplate {
			defaultOpt = opt
			continue
		}
		opts = append(opts, opt)
	}
	if defaultOpt.Key != "" {
		// make sure the default template is the first
		opts = slices.Insert(opts, 0, defaultOpt)
	}

	return opts
}

func getTemplateVersionOptions(
	template *managementv1.DevsyWorkspaceTemplate,
) []huh.Option[string] {
	opts := []huh.Option[string]{latestTemplateVersion}
	if template == nil {
		return opts
	}

	for _, version := range template.GetVersions() {
		opts = append(opts, huh.Option[string]{
			Key:   version.GetVersion(),
			Value: version.GetVersion(),
		})
	}

	return opts
}

type FieldParameter struct {
	storagev1.AppParameter

	StringValue string
	BoolValue   bool
}

func prepareParameters(parameters []storagev1.AppParameter) []*FieldParameter {
	retParams := []*FieldParameter{}
	for _, p := range parameters {
		fieldParameter := FieldParameter{AppParameter: p}
		if p.Type == parameterTypeBoolean {
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
		title := param.Label
		if title == "" {
			title = param.Variable
		}

		var field huh.Field
		switch param.Type {
		case "multiline":
			field = huh.NewText().
				Title(title).
				Description(param.Description).
				Value(&param.StringValue)
		case "password", "number", "string":
			field = stringParameterField(param, title)
		case parameterTypeBoolean:
			field = huh.NewConfirm().
				Title(title).
				Description(param.Description).
				Value(&param.BoolValue)
		default:
			field = stringParameterField(param, title)
		}

		fields = append(fields, field)
	}

	return fields
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

func getDeepValue(parameters any, path string) any {
	if parameters == nil {
		return nil
	}

	pathSegments := strings.Split(path, ".")
	switch t := parameters.(type) {
	case map[string]any:
		return getDeepValueFromMap(t, pathSegments)
	case []any:
		return getDeepValueFromSlice(t, pathSegments)
	}

	return nil
}

func getDeepValueFromMap(t map[string]any, pathSegments []string) any {
	val, ok := t[pathSegments[0]]
	if !ok {
		return nil
	} else if len(pathSegments) == 1 {
		return val
	}

	return getDeepValue(val, strings.Join(pathSegments[1:], "."))
}

func getDeepValueFromSlice(t []any, pathSegments []string) any {
	index, err := strconv.Atoi(pathSegments[0])
	if err != nil {
		return nil
	} else if index < 0 || index >= len(t) {
		return nil
	}

	val := t[index]
	if len(pathSegments) == 1 {
		return val
	}

	return getDeepValue(val, strings.Join(pathSegments[1:], "."))
}
