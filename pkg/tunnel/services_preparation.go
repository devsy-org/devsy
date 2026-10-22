package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/log"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
)

func hostCredentialOwner() string {
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	digest := sha256.Sum256([]byte(host + "\x00" + home))
	return hex.EncodeToString(digest[:16])
}

func StartPreparedServices(ctx context.Context, opts RunServicesOptions) credentials.Readiness {
	return prepareServices(ctx, opts).start(ctx, opts.ConfigureGitHubCredentials)
}

func RunPreparedServices(
	ctx context.Context,
	opts RunServicesOptions,
	prepared func(credentials.Readiness),
) error {
	return prepareServices(ctx, opts).runPrepared(ctx, opts.ConfigureGitHubCredentials, prepared)
}

func prepareServices(ctx context.Context, opts RunServicesOptions) servicePreparation {
	opts.HostOwner = hostCredentialOwner()
	return servicePreparation{
		run:   func() error { return RunServices(ctx, opts) },
		probe: func(ctx context.Context) credentials.Readiness { return probePreparedServices(ctx, opts) },
	}
}

type servicePreparation struct {
	run   func() error
	probe func(context.Context) credentials.Readiness
}

func (p servicePreparation) start(ctx context.Context, enabled bool) credentials.Readiness {
	status, _ := p.startWithLifetime(ctx, enabled)
	return status
}

type serviceCompletion struct {
	done chan struct{}
	err  error
}

func (p servicePreparation) startWithLifetime(
	ctx context.Context,
	enabled bool,
) (credentials.Readiness, *serviceCompletion) {
	completion := &serviceCompletion{done: make(chan struct{})}
	go func() {
		completion.err = p.run()
		if completion.err != nil && ctx.Err() == nil {
			log.Warn("credential services stopped unexpectedly")
		}
		close(completion.done)
	}()
	if !enabled {
		return credentials.Readiness{GitHubState: "disabled"}, completion
	}
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	ready := make(chan credentials.Readiness, 1)
	go func() { ready <- p.probe(probeCtx) }()
	return p.await(probeCtx, completion, ready), completion
}

func (p servicePreparation) runPrepared(
	ctx context.Context,
	enabled bool,
	prepared func(credentials.Readiness),
) error {
	status, completion := p.startWithLifetime(ctx, enabled)
	select {
	case <-completion.done:
		if completion.err != nil {
			return completion.result(ctx)
		}
	default:
	}
	if ctx.Err() == nil {
		prepared(status)
	}
	select {
	case <-completion.done:
		if err := completion.result(ctx); err != nil {
			return err
		}
		<-ctx.Done()
	case <-ctx.Done():
	}
	return nil
}

func (c *serviceCompletion) result(ctx context.Context) error {
	if c.err != nil && ctx.Err() == nil {
		return errors.New("credential services stopped unexpectedly")
	}
	return nil
}

func (p servicePreparation) await(
	ctx context.Context,
	completion *serviceCompletion,
	ready <-chan credentials.Readiness,
) credentials.Readiness {
	done := completion.done
	for {
		select {
		case status := <-ready:
			return status
		case <-done:
			if completion.err != nil {
				return credentials.Readiness{GitHubState: credentialsUnavailable}
			}
			done = nil
		case <-ctx.Done():
			return credentials.Readiness{GitHubState: credentialsUnavailable}
		}
	}
}

func probePreparedServices(ctx context.Context, opts RunServicesOptions) credentials.Readiness {
	command := shellescape.Quote(
		config.ContainerDevsyHelperLocation,
	) + " internal agent container credentials-status --github-enabled --user " + shellescape.Quote(
		opts.User,
	) + " --host-owner " + shellescape.Quote(
		opts.HostOwner,
	)
	out := &boundedPreparationOutput{}
	err := devssh.Run(
		ctx,
		devssh.RunOptions{
			Client:  opts.ContainerClient,
			Command: command,
			Stdout:  out,
			Stderr:  io.Discard,
		},
	)
	var status credentials.Readiness
	if err != nil || out.overflow || json.Unmarshal(out.data, &status) != nil {
		return credentials.Readiness{GitHubState: credentialsUnavailable}
	}
	expected := credentials.Readiness{
		Owner:         opts.User,
		HostOwner:     opts.HostOwner,
		GitHubAllowed: true,
	}
	if !status.Matches(expected) {
		return credentials.Readiness{GitHubState: credentialsUnavailable}
	}
	return status
}

type boundedPreparationOutput struct {
	data     []byte
	overflow bool
}

func (b *boundedPreparationOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 4096 - len(b.data); n > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

func GitHubSessionEnvironment(status credentials.Readiness) map[string]string {
	state := status.GitHubState
	if state != "installed" && state != "already_installed" {
		if state != "disabled" {
			state = credentialsUnavailable
		}
	}
	return map[string]string{
		"DEVSY_GH_FORWARDING": state,
		"DEVSY_GH_HOST_OWNER": status.HostOwner,
		"DEVSY_GH_INSTANCE":   status.Instance,
	}
}

const credentialsUnavailable = "unavailable"
