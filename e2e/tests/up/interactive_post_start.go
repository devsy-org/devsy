package up

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/creack/pty"
	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const interactivePostStartFlag = "--interactive-post-start"

type terminalOutputBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *terminalOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *terminalOutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type interactiveUpOptions struct {
	prompt bool
	args   []string
}

func interactiveUp(
	ctx context.Context,
	f *framework.Framework,
	dir string,
	opts interactiveUpOptions,
) string {
	argv := append(
		[]string{cmdWorkspace, "up", dir, flagIDEOption, kubernetesIgnoreIDE},
		opts.args...)
	path := filepath.Clean(filepath.Join(f.DevsyBinDir, f.DevsyBinName))
	cmd := exec.CommandContext(ctx, "/dev/null")
	cmd.Path = path
	cmd.Args = append([]string{path}, argv...)
	terminal, err := pty.Start(cmd)
	framework.ExpectNoError(err)
	defer func() { _ = terminal.Close() }()
	defer func() { _ = cmd.Process.Kill() }()
	var output terminalOutputBuffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, terminal)
		close(done)
	}()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	if opts.prompt {
		gomega.Eventually(func() bool {
			select {
			case err := <-waited:
				<-done
				ginkgo.Fail(
					fmt.Sprintf("up exited before prompt: %v; output: %s", err, output.String()),
				)
			default:
			}
			return strings.Contains(output.String(), "Name:")
		}).WithContext(ctx).WithTimeout(framework.TimeoutModerate()).Should(gomega.BeTrue(), "up output: %s", output.String())
		_, err = terminal.Write([]byte("Devsy\n"))
		framework.ExpectNoError(err)
	}
	err = <-waited
	<-done
	framework.ExpectNoError(err, "up output: %s", output.String())
	return output.String()
}

var _ = ginkgo.Describe("interactive post start", ginkgo.Label("up-provider-docker"), func() {
	var f *framework.Framework
	var initialDir string
	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
		f, err = framework.SetupDockerProvider(filepath.Join(initialDir, "bin"), dockerValue)
		framework.ExpectNoError(err)
	})

	ginkgo.It("accepts input and prompts once per start", func(ctx context.Context) {
		dir, err := setupWorkspace("tests/up/testdata/interactive-post-start", initialDir, f)
		framework.ExpectNoError(err)
		out := interactiveUp(
			ctx,
			f,
			dir,
			interactiveUpOptions{prompt: true, args: []string{interactivePostStartFlag}},
		)
		gomega.Expect(strings.Count(out, "Name:")).To(gomega.Equal(1))
		result, err := f.DevsySSH(ctx, dir, "cat /tmp/interactive-post-start-result")
		framework.ExpectNoError(err)
		gomega.Expect(strings.TrimSpace(result)).To(gomega.Equal("Hello, Devsy"))
		out = interactiveUp(ctx, f, dir, interactiveUpOptions{})
		gomega.Expect(out).NotTo(gomega.ContainSubstring("Name:"))
		framework.ExpectNoError(f.DevsyWorkspaceStop(ctx, dir))
		out = interactiveUp(
			ctx,
			f,
			dir,
			interactiveUpOptions{prompt: true, args: []string{interactivePostStartFlag}},
		)
		gomega.Expect(strings.Count(out, "Name:")).To(gomega.Equal(1))
	}, ginkgo.SpecTimeout(framework.TimeoutLong()))

	ginkgo.It("provides terminal stdin and stdout", func(ctx context.Context) {
		dir, err := setupWorkspace("tests/up/testdata/interactive-post-start", initialDir, f)
		framework.ExpectNoError(err)
		config := `{"image":"mcr.microsoft.com/devcontainers/base:debian",` +
			`"postStartCommand":["bash","-c",` +
			`"if test -t 0 && test -t 1; then echo yes > /tmp/interactive-tty; else echo no > /tmp/interactive-tty; fi"]}`
		framework.ExpectNoError(
			os.WriteFile(
				filepath.Join(dir, ".devcontainer", "devcontainer.json"),
				[]byte(config),
				0o600,
			),
		)
		interactiveUp(ctx, f, dir, interactiveUpOptions{args: []string{interactivePostStartFlag}})
		result, err := f.DevsySSH(ctx, dir, "cat /tmp/interactive-tty")
		framework.ExpectNoError(err)
		gomega.Expect(strings.TrimSpace(result)).To(gomega.Equal("yes"))
	}, ginkgo.SpecTimeout(framework.TimeoutLong()))

	ginkgo.It(
		"rejects detach and missing terminals before creating a workspace",
		func(ctx context.Context) {
			dir := framework.CopyTestdata("tests/up/testdata/interactive-post-start", initialDir)
			for _, tc := range []struct {
				args    []string
				message string
			}{
				{[]string{interactivePostStartFlag, "--detach"}, "--interactive-post-start cannot be combined with --detach"},
				{[]string{interactivePostStartFlag}, "--interactive-post-start requires attached stdin and stdout terminals"},
			} {
				args := append(
					[]string{
						cmdWorkspace,
						"up",
						dir,
						flagIDEOption,
						kubernetesIgnoreIDE,
						"--result-format",
						"plain",
					},
					tc.args...)
				stdout, stderr, err := f.ExecCommandCapture(ctx, args)
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(stdout + stderr).To(gomega.ContainSubstring(tc.message))
				workspaces, err := f.DevsyListParsed(ctx)
				framework.ExpectNoError(err)
				for _, w := range workspaces {
					gomega.Expect(w.ID).NotTo(gomega.Equal(workspace.ToID(dir)))
				}
			}
		},
		ginkgo.SpecTimeout(framework.TimeoutShort()),
	)
})
