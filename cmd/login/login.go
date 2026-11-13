// Package login implements `devsy login`, which signs in to a platform gateway
// and stores an org-scoped API key in the devsy config.
package login

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platformclient"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	// KeyName names the API key that login creates, so users can tell it apart
	// from other keys in the org.
	KeyName = "devsy-cli"

	sessionCleanupTimeout = 5 * time.Second
)

var keyScopes = []string{"read", "write"}

// Cmd holds the state of one `devsy login` run.
type Cmd struct {
	// Org selects the org by ID or name. Empty means: use the only org, or ask.
	Org string

	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	// HTTPClient is optional. Nil selects the platform client default.
	HTTPClient *http.Client

	stdinIsTerminal bool
	readSecret      func() (string, error)

	reader *bufio.Reader
}

func newCmd() *Cmd {
	return &Cmd{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}
}

// NewLoginCmd creates the `devsy login` command.
func NewLoginCmd() *cobra.Command {
	c := newCmd()
	loginCmd := &cobra.Command{
		Use:   "login GATEWAY_URL",
		Short: "Log in to a Devsy platform gateway",
		Long: "Log in to a Devsy platform gateway with your email and password.\n\n" +
			"Login creates an API key named " + KeyName + " for one of your organizations " +
			"and stores it in the devsy config file with mode 0600. " +
			"Your password and session token are never stored.",
		Example: `  devsy login https://gateway.example.com
  devsy login https://gateway.example.com --org "Acme Labs"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			c.In = cobraCmd.InOrStdin()
			c.Out = cobraCmd.OutOrStdout()
			c.ErrOut = cobraCmd.ErrOrStderr()
			c.useTerminalInput()
			return c.Run(cobraCmd.Context(), args[0])
		},
	}
	loginCmd.Flags().StringVar(&c.Org, "org", "", "Organization ID or name to log in to")
	return loginCmd
}

// Run signs in to the gateway and stores the resulting API key. The config is
// written only after every gateway call has succeeded.
func (c *Cmd) Run(ctx context.Context, gatewayURL string) error {
	baseURL, err := platformclient.NormalizeURL(gatewayURL)
	if err != nil {
		return err
	}
	c.reader = bufio.NewReader(c.In)

	email, password, err := c.promptCredentials()
	if err != nil {
		return err
	}

	client := platformclient.New(baseURL, c.HTTPClient)
	session, err := client.Login(ctx, email, password)
	if err != nil {
		return fmt.Errorf("logging in: %w", err)
	}
	defer logoutSession(client, session.Token)

	selected, err := c.selectOrg(ctx, client, session.Token)
	if err != nil {
		return err
	}

	key, err := client.CreateAPIKey(ctx, session.Token, platformclient.CreateKeyRequest{
		OrgID:  selected.ID,
		Name:   KeyName,
		Scopes: keyScopes,
	})
	if err != nil {
		return fmt.Errorf("creating API key for org %q: %w", selected.Name, err)
	}

	credentials := &config.GatewayCredentials{
		URL:         baseURL,
		OrgID:       selected.ID,
		OrgName:     selected.Name,
		KeyID:       key.KeyID,
		KeyRecordID: key.ID,
		APIKey:      key.Key,
	}
	if err := storeCredentials(credentials); err != nil {
		revokeUnstoredKey(client, session.Token, credentials)
		return fmt.Errorf("saving login to config: %w", err)
	}

	_, _ = fmt.Fprintf(c.Out, "Logged in as %s (org: %s)\n", email, selected.Name)
	return nil
}

// useTerminalInput reads the password without echo when stdin is a terminal.
func (c *Cmd) useTerminalInput() {
	file, ok := c.In.(*os.File)
	if !ok {
		return
	}
	fd := int(file.Fd()) //nolint:gosec // fd fits in int
	if !term.IsTerminal(fd) {
		return
	}
	c.stdinIsTerminal = true
	c.readSecret = func() (string, error) {
		secret, err := term.ReadPassword(fd)
		return string(secret), err
	}
}

func storeCredentials(credentials *config.GatewayCredentials) error {
	return config.UpdateConfig("", "", func(cfg *config.Config) error {
		cfg.Gateway = credentials
		return nil
	})
}

// logoutSession ends the session login used to mint the key. It is best effort:
// the session expires on its own if the gateway cannot be reached.
func logoutSession(client *platformclient.Client, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionCleanupTimeout)
	defer cancel()
	_ = client.Logout(ctx, token)
}

// revokeUnstoredKey revokes a key that could not be saved, since nothing else
// holds its secret and it would otherwise stay active unused.
func revokeUnstoredKey(
	client *platformclient.Client,
	token string,
	credentials *config.GatewayCredentials,
) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionCleanupTimeout)
	defer cancel()
	_ = client.RevokeAPIKey(ctx, token, credentials.OrgID, credentials.KeyRecordID)
}

func (c *Cmd) promptCredentials() (email, password string, err error) {
	email, err = c.promptLine("Email: ")
	if err != nil {
		return "", "", fmt.Errorf("reading email: %w", err)
	}
	if email == "" {
		return "", "", errors.New("email is required")
	}
	password, err = c.promptPassword("Password: ")
	if err != nil {
		return "", "", fmt.Errorf("reading password: %w", err)
	}
	if password == "" {
		return "", "", errors.New("password is required")
	}
	return email, password, nil
}

func (c *Cmd) promptLine(label string) (string, error) {
	_, _ = fmt.Fprint(c.ErrOut, label)
	line, err := c.reader.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (c *Cmd) promptPassword(label string) (string, error) {
	if !c.stdinIsTerminal || c.readSecret == nil {
		_, _ = fmt.Fprint(c.ErrOut, label)
		line, err := c.reader.ReadString('\n')
		if err != nil && (!errors.Is(err, io.EOF) || line == "") {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	_, _ = fmt.Fprint(c.ErrOut, label)
	secret, err := c.readSecret()
	_, _ = fmt.Fprintln(c.ErrOut)
	return secret, err
}

func (c *Cmd) selectOrg(
	ctx context.Context,
	client *platformclient.Client,
	token string,
) (platformclient.Org, error) {
	orgs, err := client.ListOrgs(ctx, token)
	if err != nil {
		return platformclient.Org{}, fmt.Errorf("listing organizations: %w", err)
	}
	if len(orgs) == 0 {
		return platformclient.Org{}, errors.New(
			"your account belongs to no organizations; create or join one first",
		)
	}
	if c.Org != "" {
		return matchOrg(orgs, c.Org)
	}
	if len(orgs) == 1 {
		return orgs[0], nil
	}
	return c.promptOrg(orgs)
}

func matchOrg(orgs []platformclient.Org, selector string) (platformclient.Org, error) {
	for _, o := range orgs {
		if o.ID == selector {
			return o, nil
		}
	}
	var matches []platformclient.Org
	for _, o := range orgs {
		if o.Name == selector {
			matches = append(matches, o)
		}
	}
	switch len(matches) {
	case 0:
		return platformclient.Org{}, fmt.Errorf(
			"organization %q not found among your organizations", selector,
		)
	case 1:
		return matches[0], nil
	default:
		return platformclient.Org{}, fmt.Errorf(
			"organization name %q matches %d organizations; use the organization ID",
			selector, len(matches),
		)
	}
}

func (c *Cmd) promptOrg(orgs []platformclient.Org) (platformclient.Org, error) {
	_, _ = fmt.Fprintln(c.ErrOut, "Select an organization:")
	for i, o := range orgs {
		_, _ = fmt.Fprintf(c.ErrOut, "  %d) %s (%s)\n", i+1, o.Name, o.Role)
	}
	answer, err := c.promptLine(fmt.Sprintf("Organization [1-%d]: ", len(orgs)))
	if err != nil {
		return platformclient.Org{}, fmt.Errorf("reading organization choice: %w", err)
	}
	choice, err := strconv.Atoi(answer)
	if err != nil || choice < 1 || choice > len(orgs) {
		return platformclient.Org{}, fmt.Errorf(
			"invalid organization choice %q, expected a number from 1 to %d", answer, len(orgs),
		)
	}
	return orgs[choice-1], nil
}
