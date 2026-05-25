package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"math/big"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

const (
	devContainerIDLength    = 20
	specDevContainerIDWidth = 52
	containerEnvField       = "containerEnv"
	remoteEnvField          = "remoteEnv"

	LabelLocalFolder = "devcontainer.local_folder"
	LabelConfigFile  = "devcontainer.config_file"

	varLocalEnv             = "localEnv"
	varLocalWorkspaceFolder = "localWorkspaceFolder"
)

type ReplaceFunction func(match, variable string, args []string) string

var VariableRegExp = regexp.MustCompile(`\${(.*?)}`)

type SubstitutedConfig struct {
	Config *DevContainerConfig
	Raw    *DevContainerConfig
}

type SubstitutionContext struct {
	DevContainerID           string            `json:"DevContainerID,omitempty"`
	LocalWorkspaceFolder     string            `json:"LocalWorkspaceFolder,omitempty"`
	ContainerWorkspaceFolder string            `json:"ContainerWorkspaceFolder,omitempty"`
	Env                      map[string]string `json:"Env,omitempty"`
	WorkspaceMount           string            `json:"WorkspaceMount,omitempty"`
	Userns                   string            `json:"Userns,omitempty"`
	UidMap                   []string          `json:"UidMap,omitempty"`
	GidMap                   []string          `json:"GidMap,omitempty"`
}

// preContainerFields lists devcontainer.json keys whose values reference
// the running container's environment and are resolved through the
// restricted replacer. Workspace-folder variables resolve normally (the
// host computes ContainerWorkspaceFolder via getWorkspace), but
// ${containerEnv:VAR} references stay literal so they can be resolved
// later: host-side from the image's inspected env for containerEnv (see
// ResolveContainerEnvFromImage), or inside the container by the agent's
// SubstituteContainerEnv pass for remoteEnv.
//
// remoteEnv is included here for parity with containerEnv per the
// devcontainers spec — the reference implementation
// (devcontainers/cli src/spec-common/variableSubstitution.ts) treats
// ${containerWorkspaceFolder} the same in both fields, while
// ${containerEnv:VAR} stays unresolved until the container env is known.
var preContainerFields = []string{containerEnvField, remoteEnvField}

func Substitute(substitutionCtx *SubstitutionContext, config any, out any) error {
	newVal := map[string]any{}
	err := Convert(config, &newVal)
	if err != nil {
		return err
	}

	// if windows adjust env
	isWindows := runtime.GOOS == "windows"
	if isWindows {
		newEnv := map[string]string{}
		for k, v := range substitutionCtx.Env {
			newEnv[strings.ToLower(k)] = v
		}
		substitutionCtx.Env = newEnv
	}

	if substitutionCtx.ContainerWorkspaceFolder != "" {
		substitutionCtx.ContainerWorkspaceFolder = ResolveString(
			substitutionCtx.ContainerWorkspaceFolder,
			func(match, variable string, args []string) string {
				return replaceWithContext(isWindows, substitutionCtx, match, variable, args)
			},
		)
	}

	// Two-pass substitution: pre-container fields get a restricted replacer
	// that preserves container-scoped variables as literals.
	fullReplace := func(match, variable string, args []string) string {
		return replaceWithContext(isWindows, substitutionCtx, match, variable, args)
	}
	preFieldValues := map[string]any{}
	for _, key := range preContainerFields {
		if fieldVal, ok := newVal[key]; ok {
			preFieldValues[key] = substitute0(fieldVal, restrictedReplace(fullReplace))
			delete(newVal, key)
		}
	}

	// Full substitution for remaining fields.
	retVal := substitute0(newVal, fullReplace)

	// Merge pre-container fields back into the result.
	if retMap, ok := retVal.(map[string]any); ok {
		maps.Copy(retMap, preFieldValues)
	}

	err = Convert(retVal, out)
	if err != nil {
		return err
	}

	return nil
}

func SubstituteContainerEnv(containerEnv map[string]string, config any, out any) error {
	newVal := map[string]any{}
	err := Convert(config, &newVal)
	if err != nil {
		return err
	}

	// if windows adjust env
	retVal := substitute0(newVal, func(match, variable string, args []string) string {
		return replaceWithContainerEnv(containerEnv, match, variable, args)
	})

	err = Convert(retVal, out)
	if err != nil {
		return err
	}

	return nil
}

func replaceWithContainerEnv(
	containerEnv map[string]string,
	match, variable string,
	args []string,
) string {
	switch variable {
	case containerEnvField:
		return lookupValue(false, containerEnv, args, match)
	default:
		return match
	}
}

func replaceWithContext(
	isWindows bool,
	substitutionCtx *SubstitutionContext,
	match, variable string,
	args []string,
) string {
	switch variable {
	case "devcontainerId":
		if substitutionCtx.DevContainerID != "" {
			return substitutionCtx.DevContainerID
		}
		return match
	case varLocalEnv:
		return lookupValue(isWindows, substitutionCtx.Env, args, match)
	case varLocalWorkspaceFolder:
		if substitutionCtx.LocalWorkspaceFolder != "" {
			return substitutionCtx.LocalWorkspaceFolder
		}
		return match
	case "localWorkspaceFolderBasename":
		if substitutionCtx.LocalWorkspaceFolder != "" {
			return filepath.Base(substitutionCtx.LocalWorkspaceFolder)
		}
		return match
	case "containerWorkspaceFolder":
		if substitutionCtx.ContainerWorkspaceFolder != "" {
			return substitutionCtx.ContainerWorkspaceFolder
		}
		return match
	case "containerWorkspaceFolderBasename":
		if substitutionCtx.ContainerWorkspaceFolder != "" {
			return filepath.Base(substitutionCtx.ContainerWorkspaceFolder)
		}
		return match
	case containerEnvField:
		return match
	default:
		return match
	}
}

// restrictedReplace wraps a ReplaceFunction to preserve container-scoped
// variables that cannot be resolved at host substitution time as literals.
//
// containerWorkspaceFolder and containerWorkspaceFolderBasename are NOT
// restricted: the host knows their values (derived from getWorkspace) and
// must resolve them before passing containerEnv to `docker run -e`, since
// shells and the container runtime do not perform devcontainer variable
// expansion on env-var values.
//
// Only ${containerEnv:VAR} refs remain literal here — those depend on the
// running container's environment and are resolved later, either host-side
// from the image's inspected env (see ResolveContainerEnvFromImage) or
// inside the container via SubstituteContainerEnv.
func restrictedReplace(fallback ReplaceFunction) ReplaceFunction {
	return func(match, variable string, args []string) string {
		switch variable {
		case containerEnvField:
			return match
		default:
			return fallback(match, variable, args)
		}
	}
}

// ResolveContainerEnvFromImage substitutes ${containerEnv:VAR} references in
// the given env map using the image's environment (as returned by image
// inspect, in KEY=VALUE form). The returned map preserves all other entries
// unchanged. Use this host-side to resolve containerEnv values referencing
// the image PATH (or similar) before passing them to `docker run -e`.
func ResolveContainerEnvFromImage(
	containerEnv map[string]string,
	imageEnv []string,
) (map[string]string, error) {
	if len(containerEnv) == 0 {
		return containerEnv, nil
	}
	imageEnvMap := ListToObject(imageEnv)
	out := map[string]string{}
	if err := SubstituteContainerEnv(imageEnvMap, containerEnv, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func lookupValue(isWindows bool, env map[string]string, args []string, match string) string {
	if len(args) > 0 {
		envVariableName := args[0]
		if isWindows {
			envVariableName = strings.ToLower(envVariableName)
		}

		foundEnv, ok := env[envVariableName]
		if ok {
			return foundEnv
		}

		if len(args) > 1 {
			defaultValue := strings.Join(args[1:], ":")
			return defaultValue
		}

		// For `env` we should do the same as a normal shell does - evaluates missing envs to an empty string #46436
		return ""
	}

	return match
}

func substitute0(val any, replace ReplaceFunction) any {
	switch t := val.(type) {
	case string:
		return ResolveString(t, replace)
	case []any:
		for i, v := range t {
			t[i] = substitute0(v, replace)
		}
		return t
	case map[string]any:
		for k, v := range t {
			t[k] = substitute0(v, replace)
		}
		return t
	default:
		return t
	}
}

func ResolveString(val string, replace ReplaceFunction) string {
	return string(VariableRegExp.ReplaceAllFunc([]byte(val), func(match []byte) []byte {
		variable := string(match[2 : len(match)-1])

		// try to separate variable arguments from variable name
		args := []string{}
		parts := strings.Split(variable, ":")
		if len(parts) > 1 {
			variable = parts[0]
			args = parts[1:]
		}

		return []byte(replace(string(match), variable, args))
	}))
}

func ObjectToList(object map[string]string) []string {
	ret := []string{}
	for k, v := range object {
		ret = append(ret, k+"="+v)
	}

	return ret
}

func ListToObject(list []string) map[string]string {
	ret := map[string]string{}
	for _, l := range list {
		splitted := strings.Split(l, "=")
		if len(splitted) == 1 {
			continue
		}

		ret[splitted[0]] = strings.Join(splitted[1:], "=")
	}

	return ret
}

// ComputeDevContainerID implements the official devcontainer CLI algorithm:
// SHA-256(JSON.stringify(labels, sorted keys)) → BigInt → base-32 (0-9a-v) → left-pad to 52 chars.
func ComputeDevContainerID(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var buf strings.Builder
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, _ := json.Marshal(k)
		valJSON, _ := json.Marshal(labels[k])
		buf.Write(keyJSON)
		buf.WriteByte(':')
		buf.Write(valJSON)
	}
	buf.WriteByte('}')

	h := sha256.Sum256([]byte(buf.String()))
	bigInt := new(big.Int).SetBytes(h[:])
	encoded := bigInt.Text(32)

	if len(encoded) >= specDevContainerIDWidth {
		return encoded[:specDevContainerIDWidth]
	}
	return strings.Repeat("0", specDevContainerIDWidth-len(encoded)) + encoded
}

// DefaultIDLabels returns the default labels used for devcontainerId derivation.
func DefaultIDLabels(localWorkspaceFolder, configFilePath string) map[string]string {
	return map[string]string{
		LabelLocalFolder: localWorkspaceFolder,
		LabelConfigFile:  configFilePath,
	}
}

// DeriveDevContainerID computes the spec-compliant devcontainerId from workspace and config paths.
func DeriveDevContainerID(localWorkspaceFolder, configFilePath string) string {
	return ComputeDevContainerID(DefaultIDLabels(localWorkspaceFolder, configFilePath))
}

// LegacyDeriveDevContainerID is the old derivation (SHA-256 hex prefix of folder path).
func LegacyDeriveDevContainerID(localWorkspaceFolder string) string {
	h := sha256.Sum256([]byte(localWorkspaceFolder))
	return hex.EncodeToString(h[:])[:devContainerIDLength]
}
