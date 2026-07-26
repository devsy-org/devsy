package list

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/blang/semver/v4"
	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/kube"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TemplatesCmd holds the cmd flags.
type TemplatesCmd struct {
	*flags.GlobalFlags
}

// NewTemplatesCmd creates a new command.
func NewTemplatesCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &TemplatesCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:   "templates",
		Short: "Lists templates for the provider",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	return c
}

func (cmd *TemplatesCmd) Run(ctx context.Context) error {
	projectName := os.Getenv(platform.ProjectEnv)
	if projectName == "" {
		return fmt.Errorf("%s environment variable is empty", platform.ProjectEnv)
	}

	baseClient, err := client.InitClientFromPath(ctx, cmd.Config)
	if err != nil {
		return err
	}

	templates, err := Templates(ctx, baseClient, projectName)
	if err != nil {
		return err
	}

	out, err := json.Marshal(templates)
	if err != nil {
		return err
	}
	fmt.Println(string(out))

	return nil
}

func Templates(
	ctx context.Context,
	client client.Client,
	projectName string,
) (*managementv1.ProjectTemplates, error) {
	managementClient, err := client.Management()
	if err != nil {
		return nil, err
	}

	templateList, err := managementClient.Loft().
		ManagementV1().
		Projects().
		ListTemplates(ctx, projectName, metav1.GetOptions{})
	if err != nil {
		return templateList, fmt.Errorf("list templates: %w", err)
	} else if len(templateList.DevsyWorkspaceTemplates) == 0 {
		return templateList, fmt.Errorf(
			"seems like there is no template allowed in project %s, "+
				"make sure to at least have a single template available",
			projectName,
		)
	}

	return templateList, nil
}

func FindTemplate(
	ctx context.Context,
	managementClient kube.Interface,
	projectName, templateName string,
) (*managementv1.DevsyWorkspaceTemplate, error) {
	templateList, err := managementClient.Loft().
		ManagementV1().
		Projects().
		ListTemplates(ctx, projectName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	} else if len(templateList.DevsyWorkspaceTemplates) == 0 {
		return nil, fmt.Errorf(
			"seems like there is no Devsy template allowed in project %s, "+
				"make sure to at least have a single template available",
			projectName,
		)
	}

	// find template
	var template *managementv1.DevsyWorkspaceTemplate
	for _, workspaceTemplate := range templateList.DevsyWorkspaceTemplates {
		if workspaceTemplate.Name == templateName {
			t := workspaceTemplate
			template = &t
			break
		}
	}
	if template == nil {
		return nil, fmt.Errorf("couldn't find template %s", templateName)
	}

	return template, nil
}

func GetTemplateParameters(
	template *managementv1.DevsyWorkspaceTemplate,
	templateVersion string,
) ([]storagev1.AppParameter, error) {
	if templateVersion == "latest" {
		templateVersion = ""
	}

	if templateVersion == "" {
		latestVersion := GetLatestVersion(template)
		if latestVersion == nil {
			return nil, fmt.Errorf("couldn't find any version in template")
		}

		return latestVersion.(*storagev1.DevsyWorkspaceTemplateVersion).Parameters, nil
	}

	_, latestMatched, err := GetLatestMatchedVersion(template, templateVersion)
	if err != nil {
		return nil, err
	} else if latestMatched == nil {
		return nil, fmt.Errorf("couldn't find any matching version to %s", templateVersion)
	}

	return latestMatched.(*storagev1.DevsyWorkspaceTemplateVersion).Parameters, nil
}

type matchedVersion struct {
	Object  storagev1.VersionAccessor
	Version semver.Version
}

func GetLatestVersion(versions storagev1.VersionsAccessor) storagev1.VersionAccessor {
	// find the latest version
	var latestVersion *matchedVersion
	for _, version := range versions.GetVersions() {
		parsedVersion, err := semver.Parse(strings.TrimPrefix(version.GetVersion(), "v"))
		if err != nil {
			continue
		}

		// latest available version
		if latestVersion == nil || latestVersion.Version.LT(parsedVersion) {
			latestVersion = &matchedVersion{
				Object:  version,
				Version: parsedVersion,
			}
		}
	}
	if latestVersion == nil {
		return nil
	}

	return latestVersion.Object
}

func GetLatestMatchedVersion(
	versions storagev1.VersionsAccessor,
	versionPattern string,
) (latestVersion storagev1.VersionAccessor, latestMatchedVersion storagev1.VersionAccessor, err error) {
	// parse version
	splittedVersion := strings.Split(strings.ToLower(strings.TrimPrefix(versionPattern, "v")), ".")
	if len(splittedVersion) != 3 {
		return nil, nil, fmt.Errorf(
			"couldn't parse version %s, expected version in format: 0.0.0",
			versionPattern,
		)
	}

	// find latest version that matches our defined version
	var latestVersionObj *matchedVersion
	var latestMatchedVersionObj *matchedVersion
	for _, version := range versions.GetVersions() {
		parsedVersion, err := semver.Parse(strings.TrimPrefix(version.GetVersion(), "v"))
		if err != nil {
			continue
		}

		// does the version match our restrictions?
		if versionMatchesPattern(parsedVersion, splittedVersion) {
			latestMatchedVersionObj = maxVersion(latestMatchedVersionObj, version, parsedVersion)
		}

		// latest available version
		latestVersionObj = maxVersion(latestVersionObj, version, parsedVersion)
	}

	if latestVersionObj != nil {
		latestVersion = latestVersionObj.Object
	}
	if latestMatchedVersionObj != nil {
		latestMatchedVersion = latestMatchedVersionObj.Object
	}

	return latestVersion, latestMatchedVersion, nil
}

func versionMatchesPattern(version semver.Version, pattern []string) bool {
	return versionSegmentMatches(pattern[0], version.Major) &&
		versionSegmentMatches(pattern[1], version.Minor) &&
		versionSegmentMatches(pattern[2], version.Patch)
}

func versionSegmentMatches(segment string, value uint64) bool {
	return segment == "x" || segment == "X" || strconv.FormatUint(value, 10) == segment
}

func maxVersion(
	best *matchedVersion,
	candidate storagev1.VersionAccessor,
	parsed semver.Version,
) *matchedVersion {
	if best == nil || best.Version.LT(parsed) {
		return &matchedVersion{
			Object:  candidate,
			Version: parsed,
		}
	}

	return best
}

var replaceRegEx = regexp.MustCompile("[^a-zA-Z0-9]+")

func VariableToEnvironmentVariable(variable string) string {
	return "TEMPLATE_OPTION_" + strings.ToUpper(replaceRegEx.ReplaceAllString(variable, "_"))
}
