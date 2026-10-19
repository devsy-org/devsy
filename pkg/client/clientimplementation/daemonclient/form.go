package daemonclient

import (
	"context"

	"charm.land/huh/v2"
	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/devsy/cmd/pro/provider/list"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/encoding"
	platformclient "github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/form"
	"github.com/devsy-org/devsy/pkg/platform/project"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	fieldParameters := form.PrepareParameters(parameters)
	if err := huh.NewForm(
		huh.NewGroup(form.ParameterFields(fieldParameters)...),
	).RunWithContext(formCtx); err != nil {
		return "", err
	}

	return form.RenderParameters(fieldParameters)
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
