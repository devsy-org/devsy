package ssh

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/scanner"
	"github.com/devsy-org/devsy/pkg/util"
)

var (
	MarkerStartPrefix = "# Devsy Start "
	MarkerEndPrefix   = "# Devsy End "
)

const windowsGOOS = "windows"

type SSHConfigParams struct {
	SSHConfigPath        string
	SSHConfigIncludePath string
	Context              string
	Workspace            string
	User                 string
	Workdir              string
	Command              string
	GPGAgent             bool
	AgentForwarding      bool
	DevsyHome            string
	Provider             string
	TunnelPort           int // If > 0, use TCP tunnel mode instead of ProxyCommand
	PreferredHost        string
	UID                  string
}

func ConfigureSSHConfig(params SSHConfigParams) error {
	targetPath := params.SSHConfigPath
	if params.SSHConfigIncludePath != "" {
		targetPath = params.SSHConfigIncludePath
	}

	targetPath, err := resolveSymlinkTarget(targetPath)
	if err != nil {
		return err
	}

	unlock, err := lockConfigFile(targetPath)
	if err != nil {
		return err
	}
	defer unlock()

	newFile, err := addHost(addHostParams{
		path:            targetPath,
		host:            params.Workspace + config.SSHHostSuffix,
		preferredHost:   params.PreferredHost,
		user:            params.User,
		context:         params.Context,
		workspace:       params.Workspace,
		workdir:         params.Workdir,
		command:         params.Command,
		gpgagent:        params.GPGAgent,
		agentForwarding: params.AgentForwarding,
		devsyHome:       params.DevsyHome,
		provider:        params.Provider,
		tunnelPort:      params.TunnelPort,
	})
	if err != nil {
		return fmt.Errorf("parse ssh config: %w", err)
	}

	return writeSSHConfig(targetPath, newFile)
}

type DevsySSHEntry struct {
	Host      string
	User      string
	Workspace string
}

type addHostParams struct {
	path            string
	host            string
	preferredHost   string
	user            string
	context         string
	workspace       string
	workdir         string
	command         string
	gpgagent        bool
	agentForwarding bool
	devsyHome       string
	provider        string
	tunnelPort      int
}

func addHost(params addHostParams) (string, error) {
	newConfig, err := removeFromConfig(params.path, params.host)
	if err != nil {
		return "", err
	}

	// get path to executable
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}

	return addHostSection(newConfig, execPath, params)
}

// proxyCommandBuilder builds SSH ProxyCommand strings.
type proxyCommandBuilder struct {
	baseCommand string
	options     []string
}

func normalizeSSHExecPath(execPath string) string {
	return normalizeSSHExecPathForOS(execPath, runtime.GOOS)
}

func normalizeSSHExecPathForOS(execPath, goos string) string {
	if goos == windowsGOOS {
		return strings.ReplaceAll(execPath, `\`, "/")
	}

	return execPath
}

func newProxyCommandBuilder(execPath, context, user, workspace string) *proxyCommandBuilder {
	normalizedExecPath := normalizeSSHExecPath(execPath)
	log.Debugw(
		"ssh proxy command config",
		"os", runtime.GOOS,
		"executable_raw", execPath,
		"executable_normalized", normalizedExecPath,
		"workspace", workspace,
	)

	return &proxyCommandBuilder{
		baseCommand: fmt.Sprintf(
			"\"%s\" workspace ssh %s %s %s %s %s %s",
			normalizedExecPath,
			names.Flag(names.Stdio),
			names.Flag(names.Context),
			context,
			names.Flag(names.User),
			user,
			workspace,
		),
	}
}

func (b *proxyCommandBuilder) withDevsyHome(home string) *proxyCommandBuilder {
	if home != "" {
		b.options = append(
			b.options,
			fmt.Sprintf("%s \"%s\"", names.Flag(names.Home), home),
		)
	}
	return b
}

func (b *proxyCommandBuilder) withWorkdir(workdir string) *proxyCommandBuilder {
	if workdir != "" {
		b.options = append(
			b.options,
			fmt.Sprintf("%s \"%s\"", names.Flag(names.Workdir), workdir),
		)
	}
	return b
}

func (b *proxyCommandBuilder) withGPGAgent(enabled bool) *proxyCommandBuilder {
	if enabled {
		b.options = append(b.options, names.Flag(names.SSHGPGForwarding))
	}
	return b
}

func (b *proxyCommandBuilder) build() string {
	if len(b.options) == 0 {
		return "  ProxyCommand " + b.baseCommand
	}
	return fmt.Sprintf("  ProxyCommand %s %s", b.baseCommand, strings.Join(b.options, " "))
}

// sshConfigBuilder builds SSH config entries.
type sshConfigBuilder struct {
	lines []string
}

func newSSHConfigBuilder(host, preferredHost string) *sshConfigBuilder {
	return &sshConfigBuilder{
		lines: []string{
			MarkerStartPrefix + host,
			hostLine(host, preferredHost),
		},
	}
}

func hostLine(canonicalHost, preferredHost string) string {
	if preferredHost == "" || strings.EqualFold(preferredHost, canonicalHost) {
		return "Host " + canonicalHost
	}

	return "Host " + preferredHost + " " + canonicalHost
}

func (b *sshConfigBuilder) addSSHOptions(provider string, agentForwarding bool) *sshConfigBuilder {
	forwardAgent := "no"
	if agentForwarding {
		forwardAgent = "yes"
	}
	b.lines = append(b.lines,
		"  ForwardAgent "+forwardAgent,
		"  LogLevel error",
		"  StrictHostKeyChecking no",
		"  UserKnownHostsFile /dev/null",
		"  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa",
	)

	// TODO: Make SSH timeout configurable per provider via provider options
	// The ms-vscode-remote.remote-ssh extension times out after 15s by default
	// This is insufficient for the aws AWS provider as it needs additional time to
	// connect to the instance
	//
	// The SSH config ConnectTimeout overrides the VSCode Remote-SSH remote.SSH.connectTimeout setting
	// https://github.com/microsoft/vscode-remote-release/issues/8519
	if strings.Contains(provider, "aws") {
		b.lines = append(b.lines, "  ConnectTimeout 60")
	}

	return b
}

func (b *sshConfigBuilder) addProxyCommand(proxyCmd string) *sshConfigBuilder {
	b.lines = append(b.lines, proxyCmd)
	return b
}

func (b *sshConfigBuilder) addTunnelConnection(port int) *sshConfigBuilder {
	b.lines = append(b.lines,
		"  Hostname 127.0.0.1",
		fmt.Sprintf("  Port %d", port),
	)
	return b
}

func (b *sshConfigBuilder) addUser(user, host string) *sshConfigBuilder {
	b.lines = append(b.lines, "  User "+user, MarkerEndPrefix+host)
	return b
}

func (b *sshConfigBuilder) build() []string {
	return b.lines
}

// buildProxyCommand creates the ProxyCommand string.
func buildProxyCommand(execPath string, params addHostParams) string {
	if params.command != "" {
		return fmt.Sprintf("  ProxyCommand \"%s\"", params.command)
	}

	return newProxyCommandBuilder(execPath, params.context, params.user, params.workspace).
		withDevsyHome(params.devsyHome).
		withWorkdir(params.workdir).
		withGPGAgent(params.gpgagent).
		build()
}

// buildSSHConfigLines creates the SSH config entry lines.
func buildSSHConfigLines(params addHostParams, proxyCmd string) []string {
	return newSSHConfigBuilder(params.host, params.preferredHost).
		addSSHOptions(params.provider, params.agentForwarding).
		addProxyCommand(proxyCmd).
		addUser(params.user, params.host).
		build()
}

// buildTunnelConfigLines creates the SSH config entry lines for TCP tunnel mode.
func buildTunnelConfigLines(params addHostParams) []string {
	return newSSHConfigBuilder(params.host, params.preferredHost).
		addSSHOptions(params.provider, params.agentForwarding).
		addTunnelConnection(params.tunnelPort).
		addUser(params.user, params.host).
		build()
}

func sshConfigKeyword(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}

	if separator := strings.IndexAny(trimmed, " \t="); separator >= 0 {
		return trimmed[:separator]
	}

	return trimmed
}

func isSSHSectionStart(line string) bool {
	keyword := sshConfigKeyword(line)

	return strings.EqualFold(keyword, "host") || strings.EqualFold(keyword, "match")
}

// findInsertPosition finds where to insert new SSH config entry.
func findInsertPosition(config string) (int, []string, error) {
	lines := make([]string, 0)
	scanner := bufio.NewScanner(strings.NewReader(config))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return 0, nil, err
	}

	position := len(lines)
	for i, line := range lines {
		if !isSSHSectionStart(line) {
			continue
		}

		position = i
		for position > 0 && strings.HasPrefix(strings.TrimSpace(lines[position-1]), "#") {
			position--
		}
		break
	}

	return position, lines, nil
}

// mergeSSHConfig inserts new lines into existing config.
func mergeSSHConfig(lines, newLines []string, position int) string {
	merged := slices.Insert(lines, position, newLines...)

	newLineSep := "\n"
	if runtime.GOOS == windowsGOOS {
		newLineSep = "\r\n"
	}

	return strings.Join(merged, newLineSep)
}

func addHostSection(config, execPath string, params addHostParams) (string, error) {
	var newLines []string
	if params.tunnelPort > 0 {
		newLines = buildTunnelConfigLines(params)
	} else {
		proxyCmd := buildProxyCommand(execPath, params)
		newLines = buildSSHConfigLines(params, proxyCmd)
	}

	position, lines, err := findInsertPosition(config)
	if err != nil {
		return config, err
	}

	return mergeSSHConfig(lines, newLines, position), nil
}

func GetUser(
	workspaceID string,
	sshConfigPath string,
	sshConfigIncludePath string,
) (string, error) {
	path, err := ResolveSSHConfigPath(sshConfigPath)
	if err != nil {
		return "", fmt.Errorf("invalid ssh config path: %w", err)
	}
	sshConfigPath = path

	targetPath := sshConfigPath
	if sshConfigIncludePath != "" {
		includePath, err := ResolveSSHConfigPath(sshConfigIncludePath)
		if err != nil {
			return "", fmt.Errorf("invalid ssh config include path: %w", err)
		}
		targetPath = includePath
	}

	user := "root"
	_, err = transformHostSection(
		targetPath,
		workspaceID+config.SSHHostSuffix,
		func(line string) string {
			splitted := strings.Split(strings.ToLower(strings.TrimSpace(line)), " ")
			if len(splitted) == 2 && splitted[0] == "user" {
				user = strings.Trim(splitted[1], "\"")
			}

			return line
		},
	)
	if err != nil {
		return "", err
	}

	return user, nil
}

func RemoveFromConfig(
	workspaceID string,
	sshConfigPath string,
	sshConfigIncludePath string,
) error {
	targetPath := sshConfigPath
	if sshConfigIncludePath != "" {
		targetPath = sshConfigIncludePath
	}

	targetPath, err := resolveSymlinkTarget(targetPath)
	if err != nil {
		return err
	}

	unlock, err := lockConfigFile(targetPath)
	if err != nil {
		return err
	}
	defer unlock()

	newFile, err := removeFromConfig(targetPath, workspaceID+config.SSHHostSuffix)
	if err != nil {
		return fmt.Errorf("parse ssh config: %w", err)
	}

	return writeSSHConfig(targetPath, newFile)
}

func writeSSHConfig(path, content string) error {
	err := writeFileAtomic(path, []byte(content))
	if err != nil {
		return fmt.Errorf("write ssh config: %w", err)
	}

	return nil
}

func ResolveSSHConfigPath(sshConfigPath string) (string, error) {
	homeDir, err := util.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}

	if sshConfigPath == "" {
		return filepath.Join(homeDir, ".ssh", "config"), nil
	}

	if strings.HasPrefix(sshConfigPath, "~/") {
		sshConfigPath = strings.Replace(sshConfigPath, "~", homeDir, 1)
	}

	return filepath.Abs(sshConfigPath)
}

func removeFromConfig(path, host string) (string, error) {
	return transformHostSection(path, host, func(line string) string {
		return ""
	})
}

func transformHostSection(path, host string, transform func(line string) string) (string, error) {
	var reader io.Reader
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}

		reader = strings.NewReader("")
	} else {
		reader = f
		defer func() { _ = f.Close() }()
	}

	newLines, scanErr := scanHostSection(reader, host, transform)
	if scanErr != nil {
		return "", fmt.Errorf("parse ssh config: %w", scanErr)
	}

	// remove residual empty line at start file
	if len(newLines) > 0 && newLines[0] == "" {
		newLines = newLines[1:]
	}

	return strings.Join(newLines, "\n"), nil
}

func scanHostSection(
	reader io.Reader,
	host string,
	transform func(line string) string,
) ([]string, error) {
	configScanner := scanner.NewScanner(reader)
	newLines := []string{}
	inSection := false
	startMarker := MarkerStartPrefix + host
	endMarker := MarkerEndPrefix + host
	for configScanner.Scan() {
		text := configScanner.Text()
		switch {
		case isMarkerLine(text, startMarker):
			inSection = true
		case isMarkerLine(text, endMarker):
			inSection = false
		case !inSection:
			newLines = append(newLines, text)
		case inSection:
			text = transform(text)
			if text != "" {
				newLines = append(newLines, text)
			}
		}
	}

	return newLines, configScanner.Err()
}

func isMarkerLine(text, marker string) bool {
	return strings.TrimRight(text, " \t\r") == marker
}
