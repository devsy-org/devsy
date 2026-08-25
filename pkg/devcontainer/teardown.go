package devcontainer

import (
	"context"
	"errors"
	"fmt"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

// Required steps fail teardown when they error; best-effort steps only warn.
type teardownStep struct {
	name     string
	required bool
	run      func(ctx context.Context) error
}

// Every step runs independently: one failure never skips later steps.
type teardownPlan struct {
	steps []teardownStep
}

func (t *teardownPlan) add(name string, required bool, run func(ctx context.Context) error) {
	t.steps = append(t.steps, teardownStep{name: name, required: required, run: run})
}

func (t *teardownPlan) execute(ctx context.Context) error {
	var errs []error
	for _, step := range t.steps {
		err := step.run(ctx)
		if err == nil {
			continue
		}
		if !step.required {
			log.Warnf("%s: %v", step.name, err)
			continue
		}
		errs = append(errs, fmt.Errorf("%s: %w", step.name, err))
	}
	return errors.Join(errs...)
}

// Nil details means the container is foreign or already gone.
func (r *runner) buildTeardownPlan(
	details *config.ContainerDetails,
	options DeleteOptions,
) *teardownPlan {
	plan := &teardownPlan{}

	if r.isImportedWorkspace() {
		log.Info("skipping container deletion, since it was not created by Devsy")
	} else if details != nil {
		r.addContainerTeardown(plan, details, options)
	}
	r.addLeftoverTeardown(plan)

	return plan
}

// Stopped first since runtimes refuse to rm running containers.
func (r *runner) addContainerTeardown(
	plan *teardownPlan,
	details *config.ContainerDetails,
	options DeleteOptions,
) {
	if isCompose, projectName := getDockerComposeProject(details); isCompose {
		plan.add("docker compose project", true, func(ctx context.Context) error {
			return r.deleteDockerCompose(ctx, projectName, options.RemoveVolumes)
		})
		return
	}

	if options.RemoveVolumes {
		log.Infof("--remove-volumes only removes declared volumes for docker compose workspaces")
	}
	if details.State.Status == config.ContainerStatusRunning {
		plan.add("stop devcontainer", true, func(ctx context.Context) error {
			return r.driver.StopDevContainer(ctx, r.id)
		})
	}
	plan.add("delete devcontainer", true, func(ctx context.Context) error {
		return r.driver.DeleteDevContainer(ctx, r.id)
	})
}

func (r *runner) addLeftoverTeardown(plan *teardownPlan) {
	plan.add("delivery volume cleanup", false, func(ctx context.Context) error {
		return r.newAgentDelivery().Cleanup(ctx, r.id)
	})
	plan.add("imported devcontainer cleanup", false, func(ctx context.Context) error {
		return r.cleanupImportedDevContainer()
	})
}

func (r *runner) findTeardownContainer(
	ctx context.Context,
) (*config.ContainerDetails, error) {
	if r.isImportedWorkspace() {
		return nil, nil
	}
	return r.driver.FindDevContainer(ctx, r.id)
}

func (r *runner) isImportedWorkspace() bool {
	return r.workspaceConfig != nil &&
		r.workspaceConfig.Workspace != nil &&
		r.workspaceConfig.Workspace.Source.Container != ""
}
