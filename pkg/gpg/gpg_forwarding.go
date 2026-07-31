package gpg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/log"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"golang.org/x/crypto/ssh"
)

type GPGConf struct {
	PublicKey  []byte
	OwnerTrust []byte
	SocketPath string
	GitKey     string
}

func IsGpgTunnelRunning(
	ctx context.Context,
	user string,
	client *ssh.Client,
) bool {
	writer := log.PassthroughWriter()
	defer func() { _ = writer.Close() }()

	command := "gpg -K"
	if user != "" && user != "root" {
		command = shellescape.QuoteCommand([]string{"su", "-c", command, user})
	}

	// empty output means the forwarded agent exposes no secret keys
	var out bytes.Buffer
	err := devssh.Run(ctx, devssh.RunOptions{
		Client:  client,
		Command: command,
		Stdout:  &out,
		Stderr:  writer,
	})

	return err == nil && strings.TrimSpace(out.String()) != ""
}

func GetHostPubKey() ([]byte, error) {
	return exec.Command("gpg", "--armor", "--export").Output()
}

func GetHostOwnerTrust() ([]byte, error) {
	return exec.Command("gpg", "--export-ownertrust").Output()
}

func StopGpgAgent() error {
	return exec.Command("gpgconf", "--kill", "gpg-agent").Run()
}

func (g *GPGConf) ImportGpgKey() error {
	return runGpgWithStdin(g.PublicKey, "--import")
}

func (g *GPGConf) ImportOwnerTrust() error {
	return runGpgWithStdin(g.OwnerTrust, "--import-ownertrust")
}

func runGpgWithStdin(input []byte, args ...string) error {
	//nolint:gosec // args are internal gpg directive literals
	cmd := exec.Command("gpg", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	go func() {
		defer func() { _ = stdin.Close() }()
		_, _ = stdin.Write(input)
	}()

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gpg %s: %s: %w", strings.Join(args, " "), out, err)
	}
	return nil
}

var gpgConfDirectives = []string{"use-agent", "no-autostart"}

func SetupGpgConf() error {
	existing, err := os.ReadFile(gpgConfigPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	f, err := os.OpenFile(gpgConfigPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	return appendMissingDirectives(f, string(existing))
}

func appendMissingDirectives(f *os.File, existing string) error {
	needsLeadingNewline := existing != "" && !strings.HasSuffix(existing, "\n")

	for _, directive := range gpgConfDirectives {
		if containsDirective(existing, directive) {
			continue
		}
		line := directive + "\n"
		if needsLeadingNewline {
			line = "\n" + line
			needsLeadingNewline = false
		}
		if _, err := f.WriteString(line); err != nil {
			return err
		}
	}

	return nil
}

func containsDirective(config, directive string) bool {
	for line := range strings.SplitSeq(config, "\n") {
		if strings.TrimSpace(line) == directive {
			return true
		}
	}
	return false
}

// ContainerSocketPath is the container-local path the host gpg-agent socket is
// forwarded to; any workspace user can reach it.
const ContainerSocketPath = "/tmp/S.gpg-agent"

func (g *GPGConf) SetupRemoteSocketDirTree() error {
	runUserDir := filepath.Join("/run/user", strconv.Itoa(os.Getuid()))

	//nolint:gosec // runUserDir is a fixed per-uid runtime path, not user input
	if err := exec.Command("sudo", "mkdir", "-p", runUserDir).Run(); err != nil {
		return err
	}

	//nolint:gosec // runUserDir is a fixed per-uid runtime path, not user input
	return exec.Command("sudo",
		"chown", "-R",
		strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()),
		runUserDir,
	).Run()
}

func (g *GPGConf) SetupRemoteSocketLink() error {
	links := []string{
		filepath.Join(os.Getenv("HOME"), ".gnupg", "S.gpg-agent"),
		filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "gnupg", "S.gpg-agent"),
	}

	for _, link := range links {
		//nolint:gosec // link derives from $HOME and the current uid, not user input
		if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
			return err
		}
		_ = os.Remove(link)
		if err := os.Symlink(g.SocketPath, link); err != nil {
			return err
		}
	}

	return g.claimForwardedSocket()
}

// claimForwardedSocket takes ownership of the socket, which the ssh server
// binds as root; a non-root user needs write access to connect. It is bound
// asynchronously, so wait briefly for it to appear.
func (g *GPGConf) claimForwardedSocket() error {
	owner := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	for range 30 {
		if _, err := os.Stat(g.SocketPath); err == nil {
			//nolint:gosec // g.SocketPath is the fixed forwarded socket path
			return exec.Command("sudo", "chown", owner, g.SocketPath).Run()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("forwarded gpg socket %q did not appear", g.SocketPath)
}

func gpgConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), ".gnupg", "gpg.conf")
}
