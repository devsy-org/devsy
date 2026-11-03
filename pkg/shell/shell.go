package shell

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

type CommandRunner struct {
	Command         string
	Environ         []string
	ExecutablePaths map[string]string
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
}

func RunEmulatedShell(
	ctx context.Context,
	runner *CommandRunner,
) error {
	runner.Command = strings.ReplaceAll(runner.Command, "\r", "")
	parsed, err := syntax.NewParser().Parse(strings.NewReader(runner.Command), "")
	if err != nil {
		return fmt.Errorf("parse shell command: %w", err)
	}

	if runner.Environ == nil {
		runner.Environ = []string{}
		runner.Environ = append(runner.Environ, os.Environ()...)
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	var commandVars map[syntax.Pos]executableCommand
	marker := ""
	if len(runner.ExecutablePaths) > 0 {
		marker = rand.Text()
		commandVars = commandVariables(parsed, runner.ExecutablePaths, marker)
	}
	r, err := interp.New(buildRunnerOptions(shellRunnerOptions{
		stdin:       runner.Stdin,
		stdout:      runner.Stdout,
		stderr:      runner.Stderr,
		env:         runner.Environ,
		dir:         dir,
		commandVars: commandVars,
		marker:      marker,
	})...)
	if err != nil {
		return fmt.Errorf("create shell runner: %w", err)
	}

	err = r.Run(ctx, parsed)
	if err != nil {
		var exitStatus interp.ExitStatus
		if errors.As(err, &exitStatus) && exitStatus == 0 {
			return nil
		}

		return err
	}

	return nil
}

func commandVariables(
	file *syntax.File,
	paths map[string]string,
	marker string,
) map[syntax.Pos]executableCommand {
	printer := syntax.NewPrinter()
	names := map[syntax.Pos]executableCommand{}
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		name := executableCommandVariable(call, paths, printer)
		if name != "" {
			word := call.Args[0]
			path, _ := configuredExecutablePath(paths, name)
			call.Args = append(call.Args, &syntax.Word{
				Parts: []syntax.WordPart{&syntax.Lit{Value: marker}},
			})
			names[word.Pos()] = executableCommand{name: name, word: word, path: path}
		}
		return true
	})
	return names
}

type executableCommand struct {
	name string
	word *syntax.Word
	path string
}

func configuredExecutablePath(paths map[string]string, name string) (string, bool) {
	if path, ok := paths[name]; ok {
		return path, true
	}
	if runtime.GOOS == "windows" {
		for configuredName, path := range paths {
			if strings.EqualFold(configuredName, name) {
				return path, true
			}
		}
	}
	return "", false
}

func executableCommandVariable(
	call *syntax.CallExpr,
	paths map[string]string,
	printer *syntax.Printer,
) string {
	if len(call.Args) == 0 || len(call.Assigns) != 0 {
		return ""
	}
	word := call.Args[0]
	if len(word.Parts) != 1 {
		return ""
	}
	exp, ok := word.Parts[0].(*syntax.ParamExp)
	if !ok || exp.Param == nil {
		return ""
	}
	name := exp.Param.Value
	if _, enabled := configuredExecutablePath(paths, name); enabled &&
		isPlainExpansion(printer, word, name) {
		return name
	}
	return ""
}

func isPlainExpansion(printer *syntax.Printer, word *syntax.Word, name string) bool {
	var sb strings.Builder
	if err := printer.Print(&sb, word); err != nil {
		return false
	}
	return sb.String() == "$"+name || sb.String() == "${"+name+"}"
}

func joinExecutablePath(
	args []string,
	env expand.Environ,
	dir string,
	command executableCommand,
) []string {
	fields := strings.FieldsFunc(command.path, isIFSDelimiter)
	if ifs := env.Get("IFS"); ifs.IsSet() && ifs.String() != " \t\n" {
		var err error
		fields, err = expand.Fields(&expand.Config{Env: env}, command.word)
		if err != nil {
			return args
		}
	}
	if len(fields) < 2 || len(args) < len(fields) ||
		!slices.Equal(args[:len(fields)], fields) || !isExistingFile(dir, command.path) {
		return args
	}
	return append([]string{command.path}, args[len(fields):]...)
}

func isIFSDelimiter(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

func isExistingFile(dir, path string) bool {
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

type shellRunnerOptions struct {
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	env         []string
	dir         string
	commandVars map[syntax.Pos]executableCommand
	marker      string
}

func buildRunnerOptions(p shellRunnerOptions) []interp.RunnerOption {
	defaultOpenHandler := interp.DefaultOpenHandler()
	defaultExecHandler := interp.DefaultExecHandler(2 * time.Second)
	return []interp.RunnerOption{
		interp.StdIO(p.stdin, p.stdout, p.stderr),
		interp.Env(expand.ListEnviron(p.env...)),
		interp.Dir(p.dir),
		interp.ExecHandlers(func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
			return func(ctx context.Context, args []string) error {
				return defaultExecHandler(ctx, args)
			}
		}),
		// CallHandler intercepts builtin calls. mvdan.cc/sh/v3 v3.13.1 marks
		// "kill" as a builtin but does not implement it, causing `kill` to
		// silently fail instead of executing the system binary. Rewrite the
		// command to an absolute path so IsBuiltin returns false and the
		// real binary is executed via the exec handler.
		interp.CallHandler(func(ctx context.Context, args []string) ([]string, error) {
			hc := interp.HandlerCtx(ctx)
			if p.marker != "" && args[len(args)-1] == p.marker {
				args = args[:len(args)-1]
				if len(args) == 0 {
					return []string{":"}, nil
				}
				if command, ok := p.commandVars[hc.Pos]; ok &&
					hc.Env.Get(command.name).String() == command.path {
					args = joinExecutablePath(args, hc.Env, hc.Dir, command)
				}
			}
			if args[0] == "kill" {
				args[0] = "/bin/kill"
			}
			return args, nil
		}),
		interp.OpenHandler(
			func(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
				if path == "/dev/null" {
					return devNull{}, nil
				}

				return defaultOpenHandler(ctx, path, flag, perm)
			},
		),
	}
}

var _ io.ReadWriteCloser = devNull{}

type devNull struct{}

func (devNull) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (devNull) Write(p []byte) (int, error) {
	return len(p), nil
}

func (devNull) Close() error {
	return nil
}

func GetShell(userName string) ([]string, error) {
	if runtime.GOOS != "windows" {
		shell, err := getUserShell(userName)
		if err == nil {
			return []string{shell}, nil
		}

		shell, ok := os.LookupEnv("SHELL")
		if ok {
			return []string{shell}, nil
		}

		_, err = exec.LookPath("bash")
		if err == nil {
			return []string{"bash"}, nil
		}

		_, err = exec.LookPath("sh")
		if err == nil {
			return []string{"sh"}, nil
		}
	}

	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}

	return []string{executable, "internal", "sh"}, nil
}

func getUserShell(userName string) (string, error) {
	currentUser, err := findUser(userName)
	if err != nil {
		return "", err
	}
	//nolint:gosec // fixed binary; username resolved from the local user database
	output, err := exec.Command("getent", "passwd", currentUser.Username).Output()
	if err != nil {
		return "", err
	}

	shell := strings.Split(string(output), ":")
	if len(shell) != 7 {
		return "", fmt.Errorf("unexpected getent format: %s", string(output))
	}

	loginShell := strings.TrimSpace(filepath.Base(shell[6]))
	if loginShell == "nologin" {
		return "", fmt.Errorf("no login shell configured")
	}

	return loginShell, nil
}

func findUser(userName string) (*user.User, error) {
	if userName != "" {
		u, err := user.Lookup(userName)
		if err != nil {
			return nil, err
		}
		return u, nil
	}

	return user.Current()
}
