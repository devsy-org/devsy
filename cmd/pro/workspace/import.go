package workspace

import (
	"context"
	"fmt"
	"strconv"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/provider/list"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/options"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/parameters"
	"github.com/devsy-org/devsy/pkg/platform/project"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/random"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type ImportCmd struct {
	*proflags.GlobalFlags

	WorkspaceId      string
	WorkspaceUid     string
	WorkspaceProject string

	Own bool
}

// NewImportCmd creates a new command.
func NewImportCmd(globalFlags *proflags.GlobalFlags) *cobra.Command {
	cmd := &ImportCmd{
		GlobalFlags: globalFlags,
	}

	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Imports a workspace",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	cliflags.Add(
		importCmd,
		cliflags.String(&cmd.WorkspaceId, names.WorkspaceID, "", "ID of a workspace to import"),
		cliflags.String(&cmd.WorkspaceUid, names.WorkspaceUID, "", "UID of a workspace to import"),
		cliflags.String(
			&cmd.WorkspaceProject,
			names.WorkspaceProject,
			"",
			"Project of the workspace to import",
		),
		cliflags.Bool(
			&cmd.Own,
			names.Own,
			false,
			"If true, will behave as if workspace was not imported",
		),
	)
	_ = importCmd.MarkFlagRequired(names.WorkspaceUID)
	return importCmd
}

func (cmd *ImportCmd) Run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: devsy pro workspace import <devsy-pro-host>")
	}

	devsyProHost := args[0]
	devsyConfig, err := config.LoadConfig(cmd.Context, "")
	if err != nil {
		return err
	}

	done, err := cmd.resolveWorkspaceID(devsyConfig)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	ref, err := cmd.findWorkspaceInstance(ctx, devsyConfig, devsyProHost)
	if err != nil {
		return err
	}

	return cmd.importInstance(ctx, devsyConfig, ref)
}

// resolveWorkspaceID sets the target workspace ID and reports whether the
// workspace has already been imported (done == true).
func (cmd *ImportCmd) resolveWorkspaceID(devsyConfig *config.Config) (bool, error) {
	// set uid as id
	if cmd.WorkspaceId == "" {
		cmd.WorkspaceId = cmd.WorkspaceUid
	}

	// check if workspace already exists
	if !provider2.WorkspaceExists(devsyConfig.DefaultContext, cmd.WorkspaceId) {
		return false, nil
	}

	workspaceConfig, err := provider2.LoadWorkspaceConfig(
		devsyConfig.DefaultContext,
		cmd.WorkspaceId,
	)
	if err != nil {
		return false, fmt.Errorf("load workspace: %w", err)
	} else if workspaceConfig.UID == cmd.WorkspaceUid {
		log.Infof("Workspace %s already imported", cmd.WorkspaceId)
		return true, nil
	}

	newWorkspaceId := cmd.WorkspaceId + "-" + random.String(5)
	if provider2.WorkspaceExists(devsyConfig.DefaultContext, newWorkspaceId) {
		return false, fmt.Errorf("workspace %s already exists", newWorkspaceId)
	}

	log.Infof(
		"workspace ID conflict, will import workspace with new ID: "+
			"existingWorkspaceId=%s, existingWorkspaceUid=%s, newWorkspaceId=%s",
		cmd.WorkspaceId,
		workspaceConfig.UID,
		newWorkspaceId,
	)
	cmd.WorkspaceId = newWorkspaceId

	return false, nil
}

type workspaceInstanceRef struct {
	provider   *provider2.ProviderConfig
	baseClient client.Client
	instance   *managementv1.DevsyWorkspaceInstance
}

func (cmd *ImportCmd) findWorkspaceInstance(
	ctx context.Context,
	devsyConfig *config.Config,
	devsyProHost string,
) (workspaceInstanceRef, error) {
	provider, err := workspace.ProviderFromHost(ctx, devsyConfig, devsyProHost)
	if err != nil {
		return workspaceInstanceRef{}, fmt.Errorf("resolve provider: %w", err)
	}

	baseClient, err := platform.InitClientFromProvider(
		ctx,
		devsyConfig,
		provider.Name,
	)
	if err != nil {
		return workspaceInstanceRef{}, fmt.Errorf("base client: %w", err)
	}
	opts := platform.FindInstanceOptions{UID: cmd.WorkspaceUid, ProjectName: cmd.WorkspaceProject}
	instance, err := platform.FindInstance(ctx, baseClient, opts)
	if err != nil {
		return workspaceInstanceRef{}, fmt.Errorf("find workspace instance: %w", err)
	}
	if instance == nil {
		return workspaceInstanceRef{}, fmt.Errorf(
			"workspace instance with UID %s not found",
			cmd.WorkspaceUid,
		)
	}

	return workspaceInstanceRef{
		provider:   provider,
		baseClient: baseClient,
		instance:   instance,
	}, nil
}

func (cmd *ImportCmd) importInstance(
	ctx context.Context,
	devsyConfig *config.Config,
	ref workspaceInstanceRef,
) error {
	// old pro provider
	if !ref.provider.HasHealthCheck() {
		instanceOpts, err := resolveInstanceOptions(ctx, ref.instance, ref.baseClient)
		if err != nil {
			return fmt.Errorf("resolve instance options: %w", err)
		}

		err = cmd.writeWorkspaceDefinition(devsyConfig, ref.provider, instanceOpts, ref.instance)
		if err != nil {
			return fmt.Errorf("prepare workspace to import definition: %w", err)
		}
		log.Infof("imported workspace: workspaceId=%s", cmd.WorkspaceId)
		return nil
	}

	// new pro provider
	err := cmd.writeNewWorkspaceDefinition(devsyConfig, ref.instance, ref.provider.Name)
	if err != nil {
		return fmt.Errorf("prepare workspace to import definition: %w", err)
	}

	log.Infof("imported workspace: workspaceId=%s", cmd.WorkspaceId)

	return nil
}

func (cmd *ImportCmd) writeNewWorkspaceDefinition(
	devsyConfig *config.Config,
	instance *managementv1.DevsyWorkspaceInstance,
	providerName string,
) error {
	workspaceObj := &provider2.Workspace{
		ID:       cmd.WorkspaceId,
		UID:      cmd.WorkspaceUid,
		Provider: provider2.WorkspaceProviderConfig{Name: providerName},
		Context:  devsyConfig.DefaultContext,
		Imported: !cmd.Own,
		Pro: &provider2.ProMetadata{
			InstanceName: instance.GetName(),
			Project:      project.ProjectFromNamespace(instance.Namespace),
			DisplayName:  instance.Spec.DisplayName,
		},
	}

	return provider2.SaveWorkspaceConfig(workspaceObj)
}

func (cmd *ImportCmd) writeWorkspaceDefinition(
	devsyConfig *config.Config,
	provider *provider2.ProviderConfig,
	instanceOpts map[string]string,
	instance *managementv1.DevsyWorkspaceInstance,
) error {
	workspaceObj := &provider2.Workspace{
		ID:  cmd.WorkspaceId,
		UID: cmd.WorkspaceUid,
		Provider: provider2.WorkspaceProviderConfig{
			Name:    provider.Name,
			Options: map[string]config.OptionValue{},
		},
		Context:  devsyConfig.DefaultContext,
		Imported: !cmd.Own,
		Pro: &provider2.ProMetadata{
			InstanceName: instance.GetName(),
			Project:      instanceOpts[platform.ProjectEnv],
			DisplayName:  instance.Spec.DisplayName,
		},
	}

	devsyConfig, err := options.ResolveOptions(
		context.Background(),
		devsyConfig,
		provider,
		instanceOpts,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("resolve options: %w", err)
	}
	if devsyConfig.Current() == nil || devsyConfig.Current().Providers[provider.Name] == nil {
		return fmt.Errorf("unable to resolve provider config for provider %s", provider.Name)
	}
	workspaceObj.Provider.Options = devsyConfig.Current().Providers[provider.Name].Options

	err = provider2.SaveWorkspaceConfig(workspaceObj)
	if err != nil {
		return err
	}

	return nil
}

func resolveInstanceOptions(
	ctx context.Context,
	instance *managementv1.DevsyWorkspaceInstance,
	baseClient client.Client,
) (map[string]string, error) {
	opts := map[string]string{}
	projectName := project.ProjectFromNamespace(instance.Namespace)

	opts[platform.ProjectEnv] = projectName
	if instance.Spec.TemplateRef == nil {
		return opts, nil
	}
	//nolint:all
	if instance.Spec.RunnerRef.Runner != "" {
		opts[platform.RunnerEnv] = instance.Spec.RunnerRef.Runner //nolint:all
	}
	opts[platform.TemplateOptionEnv] = instance.Spec.TemplateRef.Name

	if instance.Spec.TemplateRef.Version != "" {
		opts[platform.TemplateVersionOptionEnv] = instance.Spec.TemplateRef.Version
	}

	if instance.Spec.Parameters == "" {
		return opts, nil
	}

	err := resolveTemplateParameters(ctx, resolveTemplateParametersParams{
		instance:    instance,
		baseClient:  baseClient,
		projectName: projectName,
		opts:        opts,
	})
	if err != nil {
		return nil, err
	}

	return opts, nil
}

type resolveTemplateParametersParams struct {
	instance    *managementv1.DevsyWorkspaceInstance
	baseClient  client.Client
	projectName string
	opts        map[string]string
}

func resolveTemplateParameters(
	ctx context.Context,
	params resolveTemplateParametersParams,
) error {
	managementClient, err := params.baseClient.Management()
	if err != nil {
		return fmt.Errorf("get management client: %w", err)
	}
	template, err := list.FindTemplate(
		ctx,
		managementClient,
		params.projectName,
		params.instance.Spec.TemplateRef.Name,
	)
	if err != nil {
		return fmt.Errorf("find template: %w", err)
	}
	templateParameters := template.Spec.Parameters
	if len(template.Spec.Versions) > 0 {
		templateParameters, err = list.GetTemplateParameters(
			template,
			params.instance.Spec.TemplateRef.Version,
		)
		if err != nil {
			return fmt.Errorf("get template parameters: %w", err)
		}
	}
	err = fillParameterOptions(params.opts, templateParameters, params.instance.Spec.Parameters)
	if err != nil {
		return fmt.Errorf("fill parameter options: %w", err)
	}

	return nil
}

func fillParameterOptions(
	opts map[string]string,
	parameterDefinitions []storagev1.AppParameter,
	instanceParameters string,
) error {
	parametersMap := map[string]any{}
	err := yaml.Unmarshal([]byte(instanceParameters), &parametersMap)
	if err != nil {
		return fmt.Errorf("unmarshal parameters: %w", err)
	}

	for _, parameter := range parameterDefinitions {
		val := parameters.GetDeepValue(parametersMap, parameter.Variable)
		strVal, err := parameterValueString(val, parameter)
		if err != nil {
			return err
		}

		_, err = parameters.VerifyValue(strVal, parameter)
		if err != nil {
			return err
		}

		optionName := list.VariableToEnvironmentVariable(parameter.Variable)
		opts[optionName] = strVal
	}

	return nil
}

func parameterValueString(val any, parameter storagev1.AppParameter) (string, error) {
	switch t := val.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case int:
		return strconv.Itoa(t), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(t), nil
	default:
		return "", fmt.Errorf(
			"unrecognized type for parameter %s (%s) in file: %v",
			parameter.Label,
			parameter.Variable,
			t,
		)
	}
}
