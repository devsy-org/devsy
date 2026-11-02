package inject

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/pkg/template"
)

func GenerateScript(script string, params *Params) (string, error) {
	values := map[string]string{
		"Command":         params.Command,
		"ExistsCheck":     params.ExistsCheck,
		"InstallDir":      params.InstallDir(),
		"InstallFilename": params.InstallFilename(),
		"PreferDownload":  params.PreferDownload(),
		"ChmodPath":       params.ChmodPath(),
		"DownloadBase":    params.DownloadURLs.Base,
		"DownloadAmd":     params.DownloadURLs.Amd,
		"DownloadArm":     params.DownloadURLs.Arm,
	}
	for _, name := range []string{"Command", "ExistsCheck"} {
		if strings.ContainsRune(values[name], 0) {
			return "", fmt.Errorf("%s contains NUL", name)
		}
	}
	for _, name := range []string{"InstallDir", "InstallFilename", "DownloadBase", "DownloadAmd", "DownloadArm"} {
		quoted, err := QuotePOSIXLiteral(values[name])
		if err != nil {
			return "", fmt.Errorf("quote %s: %w", name, err)
		}
		values[name] = quoted
	}
	rawCode, err := template.FillTemplate(stripCarriageReturns(script), values)
	if err != nil {
		return "", err
	}

	return rawCode, nil
}

// QuotePOSIXLiteral encodes one literal word for a POSIX shell, not a login shell.
func QuotePOSIXLiteral(value string) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("shell literal contains NUL")
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'", nil
}

type Params struct {
	Command         string
	AgentRemotePath string
	DownloadURLs    *DownloadURLs

	ExistsCheck         string
	PreferAgentDownload bool
	ShouldChmodPath     bool
}

func (p *Params) InstallDir() string {
	return path.Dir(p.AgentRemotePath)
}

func (p *Params) InstallFilename() string {
	return path.Base(p.AgentRemotePath)
}

func (p *Params) PreferDownload() string {
	return strconv.FormatBool(p.PreferAgentDownload)
}

func (p *Params) ChmodPath() string {
	return strconv.FormatBool(p.ShouldChmodPath)
}

func stripCarriageReturns(script string) string {
	return strings.ReplaceAll(script, "\r", "")
}
