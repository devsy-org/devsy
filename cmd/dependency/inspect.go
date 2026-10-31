package dependency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

const policyBlocked clierr.Code = "dependency_policy_blocked"

type inspectOptions struct {
	globalFlags *flags.GlobalFlags
	forProvider string
	asJSON      bool
}

func newInspectCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	options := &inspectOptions{globalFlags: globalFlags}
	command := &cobra.Command{
		Use:          "inspect <tool>",
		Short:        "Inspect a saved provider dependency without executing it",
		SilenceUsage: true,
		Long: "Inspect docker, podman, apple-container, or kubernetes-config for an installed provider. " +
			"Reads configuration and filesystem metadata without executing tools, contacting endpoints, or changing selection.",
		Args: inspectArgs,
		RunE: options.run,
	}
	command.Flags().
		StringVar(&options.forProvider, "for-provider", "", "Inspect this installed provider without changing the default")
	command.Flags().BoolVar(&options.asJSON, "json", false, "Print one dependency report as JSON")
	flags.MarkPassive(command)
	flags.MarkDependencyInspection(command)
	command.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
		return errors.New("invalid dependency inspection flags")
	})
	return command
}

func inspectArgs(_ *cobra.Command, args []string) error {
	if len(args) != 1 {
		return errors.New("dependency inspect requires exactly one tool")
	}
	return nil
}

func (options *inspectOptions) run(command *cobra.Command, args []string) error {
	captured := os.Environ()
	cwd, err := os.Getwd()
	if err != nil {
		return inspectionError(command.Context(), "cannot capture dependency inspection directory")
	}
	request := inspectionRequest{
		Tool: args[0], ForProvider: options.forProvider,
		ForProviderPresent: command.Flags().Changed("for-provider"),
		Environment:        inspectionEnvironment(captured), WorkingDirectory: cwd,
		Metadata: dependencies.PassiveReportMetadata{
			EvaluatedAt: time.Now(), VirtualizationCapability: "not_required",
			Redact: secrets.NewEnvironmentRedactor(captured).Redact,
		},
	}
	mode := output.ModeJSON
	if !options.asJSON {
		mode, err = output.ResolveMode(options.globalFlags.ResultFormat)
		if err != nil {
			return errors.New("invalid dependency inspection result format")
		}
	}
	report, err := inspectSavedProvider(command.Context(), request)
	if err != nil {
		return err
	}
	if err := command.Context().Err(); err != nil {
		return err
	}
	if mode == output.ModeJSON {
		return json.NewEncoder(command.OutOrStdout()).Encode(report)
	}
	_, err = fmt.Fprintf(
		command.OutOrStdout(),
		"%s: %s; compatibility: %s; reachability: %s\n%s\n",
		report.Items[0].DisplayName,
		report.Availability,
		report.Compatibility,
		report.Reachability,
		report.Message,
	)
	return err
}

type inspectionRequest struct {
	Tool               string
	ForProvider        string
	ForProviderPresent bool
	Environment        map[string]string
	WorkingDirectory   string
	Metadata           dependencies.PassiveReportMetadata
}

func inspectionError(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &clierr.CLIError{Code: policyBlocked, Message: message}
}

func inspectSavedProvider(
	ctx context.Context,
	request inspectionRequest,
) (dependencies.Report, error) {
	if err := ctx.Err(); err != nil {
		return dependencies.Report{}, err
	}
	prepared, err := prepareSavedInspection(ctx, request)
	if err != nil {
		return dependencies.Report{}, err
	}
	catalog, err := dependencies.LoadCatalog()
	if err != nil {
		return dependencies.Report{}, inspectionError(
			ctx,
			"cannot load the dependency inspection catalog",
		)
	}
	if !inspectionToolMatches(catalog, prepared.Provider.Source, request.Tool) {
		return dependencies.Report{}, inspectionError(
			ctx,
			"tool does not match the installed provider dependency",
		)
	}
	result, err := workspace.SurveyProviderDependencies(ctx, *prepared)
	if err != nil {
		return dependencies.Report{}, inspectionError(
			ctx,
			"cannot survey the installed provider dependency",
		)
	}
	report, err := catalog.FormatPassiveReport(result, request.Metadata)
	if err != nil {
		return dependencies.Report{}, inspectionError(
			ctx,
			"cannot format the dependency inspection report",
		)
	}
	if err := ctx.Err(); err != nil {
		return dependencies.Report{}, err
	}
	return report, nil
}

func prepareSavedInspection(
	ctx context.Context,
	request inspectionRequest,
) (*workspace.ProviderDependencySurveyRequest, error) {
	inputs, err := preparedInspectionInputs(ctx)
	if err != nil {
		return nil, err
	}
	name, err := selectedProvider(
		ctx,
		inputs.Config,
		request.ForProvider,
		request.ForProviderPresent,
	)
	if err != nil {
		return nil, err
	}
	installed, err := loadInspectionProvider(ctx, inputs, name)
	if err != nil {
		return nil, err
	}
	manifest, stored := installed.manifest, installed.stored
	target, err := inspectionTarget(inputs.Config, name, *manifest, stored)
	if err != nil {
		return nil, inspectionError(ctx, "cannot fingerprint dependency inspection snapshots")
	}
	prepared, err := workspace.PrepareProviderDependencySurvey(
		ctx,
		workspace.ProviderDependencyPreparationInput{
			Basis:               workspace.ProviderDependencySaved,
			Provider:            *manifest,
			StoredOptions:       stored.Options,
			DynamicOptions:      stored.DynamicOptions,
			ExplicitAssignments: map[string]string{},
			Environment:         request.Environment,
			WorkingDirectory:    request.WorkingDirectory,
			Target:              target,
			Host: dependencies.SurveyHost{
				GOOS:   runtime.GOOS,
				GOARCH: runtime.GOARCH,
			},
		},
	)
	if err != nil || prepared.Request == nil {
		return nil, inspectionError(
			ctx,
			"saved provider dependency inspection is blocked by policy",
		)
	}
	return prepared.Request, nil
}

type installedInspectionProvider struct {
	manifest *provider.ProviderConfig
	stored   *config.ProviderConfig
}

func preparedInspectionInputs(ctx context.Context) (flags.PassiveInputs, error) {
	inputs, ok := flags.PassiveInputsFromContext(ctx)
	if !ok || inputs.Paths == nil || inputs.Config == nil || inputs.Config.Current() == nil {
		return flags.PassiveInputs{}, inspectionError(
			ctx,
			"dependency inspection requires prepared passive inputs",
		)
	}
	return inputs, nil
}

func loadInspectionProvider(
	ctx context.Context,
	inputs flags.PassiveInputs,
	name string,
) (installedInspectionProvider, error) {
	stored := inputs.Config.Current().Providers[name]
	if stored == nil {
		return installedInspectionProvider{}, inspectionError(
			ctx,
			"dependency inspection requires an installed provider record",
		)
	}
	manifest, err := provider.LoadProviderConfigWithPathManager(
		inputs.Paths,
		inputs.Config.DefaultContext,
		name,
	)
	if err != nil || manifest == nil || manifest.Name != name {
		return installedInspectionProvider{}, inspectionError(
			ctx,
			"cannot load the installed dependency inspection provider",
		)
	}
	if err := ctx.Err(); err != nil {
		return installedInspectionProvider{}, err
	}
	return installedInspectionProvider{manifest: manifest, stored: stored}, nil
}

func selectedProvider(
	ctx context.Context,
	snapshot *config.Config,
	override string,
	present bool,
) (string, error) {
	name := snapshot.Current().DefaultProvider
	if present {
		if override == "" {
			return "", errors.New("dependency inspect requires a nonempty --for-provider")
		}
		name = override
	}
	invocation := flags.InspectionInvocationFromContext(ctx)
	if invocation.ProviderPresent && invocation.Provider != name {
		return "", errors.New("--provider conflicts with the dependency inspection target")
	}
	if name == "" {
		return "", inspectionError(
			ctx,
			"dependency inspection requires an installed provider selection",
		)
	}
	return name, nil
}

func inspectionTarget(
	snapshot *config.Config,
	name string,
	manifest provider.ProviderConfig,
	stored *config.ProviderConfig,
) (dependencies.SurveyTarget, error) {
	configRevision, err := snapshotRevision(snapshot)
	if err != nil {
		return dependencies.SurveyTarget{}, err
	}
	providerRevision, err := snapshotRevision(providerSnapshotV1{
		Version:        1,
		Manifest:       manifest,
		StoredOptions:  stored.Options,
		DynamicOptions: stored.DynamicOptions,
	})
	return dependencies.SurveyTarget{
		ContextName: snapshot.DefaultContext, ProviderName: name,
		ConfigRevision: configRevision, ProviderConfigRevision: providerRevision,
	}, err
}

// Preparation already verified the complete internal manifest. Match its source
// against the catalog before any executable filesystem lookup can occur.
func inspectionToolMatches(
	catalog *dependencies.Catalog,
	source provider.ProviderSource,
	tool string,
) bool {
	if !source.Internal {
		return false
	}
	for _, binding := range catalog.Bindings() {
		if binding.Source.Kind == dependencies.SourceInternal &&
			binding.Source.InternalID == source.Raw {
			return len(binding.Requirements) == 1 && binding.Requirements[0].ToolID == tool
		}
	}
	return false
}
