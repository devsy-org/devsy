package gpg

import (
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
	"k8s.io/apimachinery/pkg/util/wait"
)

type GPGConf struct {
	PublicKey  []byte
	OwnerTrust []byte
	SocketPath string
	GitKey     string
}

// IsGpgTunnelRunning reports whether a live gpg-agent forward already
// reaches the container by pinging the agent rather than scanning its
// keyring. gpg-connect-agent always exits 0 even when unreachable, so
// liveness is read from stdout: a live agent answers with a trailing "OK".
func IsGpgTunnelRunning(
	ctx context.Context,
	user string,
	client *ssh.Client,
) bool {
	writer := log.PassthroughWriter()
	defer func() { _ = writer.Close() }()

	command := `echo "GETINFO version" | timeout 5 gpg-connect-agent --no-autostart`
	if user != "" && user != "root" {
		command = shellescape.QuoteCommand([]string{"su", "-c", command, user})
	}

	var out strings.Builder
	err := devssh.Run(ctx, devssh.RunOptions{
		Client:  client,
		Command: command,
		Stdout:  &out,
		Stderr:  writer,
	})

	return err == nil && strings.HasSuffix(strings.TrimSpace(out.String()), "OK")
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

func (g *GPGConf) SetupRemoteSocketLink(ctx context.Context) error {
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

	return g.claimForwardedSocket(ctx)
}

// claimForwardedSocket takes ownership of the socket, which the ssh server
// binds as root; a non-root user needs write access to connect. The socket
// is bound asynchronously by the ssh server, so this waits for it to appear.
func (g *GPGConf) claimForwardedSocket(ctx context.Context) error {
	owner := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())

	backoff := wait.Backoff{
		Duration: 200 * time.Millisecond,
		Factor:   1.5,
		Jitter:   0.1,
		Steps:    15,
		Cap:      2 * time.Second,
	}

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(_ context.Context) (bool, error) {
		info, err := os.Stat(g.SocketPath)
		if err != nil {
			return false, nil // Retry
		}
		if info.Mode()&os.ModeSocket == 0 {
			return false, fmt.Errorf("path %q exists but is not a unix socket", g.SocketPath)
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("forwarded gpg socket %q did not appear as expected: %w", g.SocketPath, err)
	}

	//nolint:gosec // g.SocketPath is the fixed forwarded socket path
	return exec.Command("sudo", "chown", owner, g.SocketPath).Run()
}

func gpgConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), ".gnupg", "gpg.conf")
}
