package daemonclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	_ "github.com/devsy-org/api/pkg/apis/management/install" // Install the management group to ensure the option types are registered
	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/apiserver/pkg/builders"
	clientpkg "github.com/devsy-org/devsy/pkg/client"
	devsyconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	platformclient "github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/kube"
	"github.com/devsy-org/devsy/pkg/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *client) Up(ctx context.Context, opt clientpkg.UpOptions) (*config.Result, error) {
	baseClient, err := c.initPlatformClient(ctx)
	if err != nil {
		return nil, err
	}

	instance, err := platform.FindInstance(
		ctx,
		baseClient,
		platform.FindInstanceOptions{UID: c.workspace.UID},
	)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, fmt.Errorf(
			"workspace %s not found. Looks like it does not exist anymore and you can delete it",
			c.workspace.ID,
		)
	}

	// check if the workspace is migrated and we need to force recreate or reset
	if err := migratedRebuildError(instance, c.workspace.ID, opt); err != nil {
		return nil, err
	}

	// Log current workspace information. This is both useful to the user to understand the workspace configuration
	// and to us when we receive troubleshooting logs
	printInstanceInfo(instance)

	instance, err = syncTemplateIfRequired(ctx, baseClient, instance)
	if err != nil {
		return nil, err
	}

	managementClient, taskID, err := startUpTask(ctx, baseClient, instance, opt)
	if err != nil {
		return nil, err
	}

	return waitTaskDone(ctx, managementClient, instance, taskID, reporterOrNop(opt.Reporter))
}

func migratedRebuildError(
	instance *managementv1.DevsyWorkspaceInstance,
	workspaceID string,
	opt clientpkg.UpOptions,
) error {
	if instance.Annotations["devsy.sh/migrated"] != devsyconfig.BoolTrue || opt.Recreate ||
		opt.Reset {
		return nil
	}

	if os.Getenv(devsyconfig.EnvUI) == devsyconfig.BoolTrue {
		return fmt.Errorf(
			"workspace %s is migrated and needs to be rebuild or reset. "+
				"Click on rebuild or reset on the workspace to do this",
			workspaceID,
		)
	}

	return fmt.Errorf(
		"workspace %s is migrated and needs to be recreated or reset. Use the recreate or reset flag to do this",
		workspaceID,
	)
}

func syncTemplateIfRequired(
	ctx context.Context,
	baseClient platformclient.Client,
	instance *managementv1.DevsyWorkspaceInstance,
) (*managementv1.DevsyWorkspaceInstance, error) {
	if instance.Spec.TemplateRef == nil || !templateUpdateRequired(instance) {
		return instance, nil
	}

	log.Info("Template update required")
	oldInstance := instance.DeepCopy()
	instance.Spec.TemplateRef.SyncOnce = true

	updated, err := platform.UpdateInstance(ctx, baseClient, oldInstance, instance)
	if err != nil {
		return nil, fmt.Errorf("update instance: %w", err)
	}
	log.Info("updated template")

	return updated, nil
}

func startUpTask(
	ctx context.Context,
	baseClient platformclient.Client,
	instance *managementv1.DevsyWorkspaceInstance,
	opt clientpkg.UpOptions,
) (kube.Interface, string, error) {
	// encode options
	rawOptions, _ := json.Marshal(opt)
	managementClient, err := baseClient.Management()
	if err != nil {
		return nil, "", fmt.Errorf("error getting management client: %w", err)
	}

	// prompt user to attach to active task or start new one
	log.Debug("Check active up task")
	activeUpTask, err := findActiveUpTask(ctx, managementClient, instance)
	if err != nil {
		return nil, "", fmt.Errorf("find active up task: %w", err)
	}

	// if we have an active up task, cancel it before creating a new one
	if err := cancelActiveUpTask(ctx, managementClient, instance, activeUpTask); err != nil {
		return nil, "", err
	}

	taskID, err := createUpTask(ctx, createUpTaskParams{
		managementClient: managementClient,
		instance:         instance,
		opt:              opt,
		rawOptions:       string(rawOptions),
	})
	if err != nil {
		return nil, "", err
	}

	return managementClient, taskID, nil
}

func reporterOrNop(r status.Reporter) status.Reporter {
	if r == nil {
		return status.Nop()
	}
	return r
}

type createUpTaskParams struct {
	managementClient kube.Interface
	instance         *managementv1.DevsyWorkspaceInstance
	opt              clientpkg.UpOptions
	rawOptions       string
}

func cancelActiveUpTask(
	ctx context.Context,
	managementClient kube.Interface,
	instance *managementv1.DevsyWorkspaceInstance,
	activeUpTask *managementv1.DevsyWorkspaceInstanceTask,
) error {
	if activeUpTask == nil {
		return nil
	}

	log.Warnf("Found active up task %s, attempting to cancel it", activeUpTask.ID)
	_, err := managementClient.Loft().
		ManagementV1().
		DevsyWorkspaceInstances(instance.Namespace).
		Cancel(ctx, instance.Name, &managementv1.DevsyWorkspaceInstanceCancel{
			TaskID: activeUpTask.ID,
		}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("cancel task: %w", err)
	}

	return nil
}

func createUpTask(ctx context.Context, params createUpTaskParams) (string, error) {
	task, err := params.managementClient.Loft().
		ManagementV1().
		DevsyWorkspaceInstances(params.instance.Namespace).
		Up(ctx, params.instance.Name, &managementv1.DevsyWorkspaceInstanceUp{
			Spec: managementv1.DevsyWorkspaceInstanceUpSpec{
				Debug:   params.opt.Debug,
				Options: params.rawOptions,
			},
		}, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("error creating up: %w", err)
	}
	if task.Status.TaskID == "" {
		return "", fmt.Errorf("no up task id returned from server")
	}

	return task.Status.TaskID, nil
}

func waitTaskDone(
	ctx context.Context,
	managementClient kube.Interface,
	instance *managementv1.DevsyWorkspaceInstance,
	taskID string,
	reporter status.Reporter,
) (*config.Result, error) {
	exitCode, err := observeTask(ctx, managementClient, instance, taskID, reporter)
	if err != nil {
		return nil, fmt.Errorf("up: %w", err)
	} else if exitCode != 0 {
		return nil, fmt.Errorf("up failed with exit code %d", exitCode)
	}

	// get result
	tasks := &managementv1.DevsyWorkspaceInstanceTasks{}
	err = managementClient.Loft().ManagementV1().RESTClient().Get().
		Namespace(instance.Namespace).
		Resource("devsyworkspaceinstances").
		Name(instance.Name).
		SubResource("tasks").
		VersionedParams(&managementv1.DevsyWorkspaceInstanceTasksOptions{
			TaskID: taskID,
		}, builders.ParameterCodec).
		Do(ctx).
		Into(tasks)
	switch {
	case err != nil:
		return nil, fmt.Errorf("error getting up result: %w", err)
	case len(tasks.Tasks) == 0 || tasks.Tasks[0].Result == nil:
		return nil, fmt.Errorf("up result not found")
	case len(tasks.Tasks) > 1:
		return nil, fmt.Errorf("multiple up results found")
	}

	// unmarshal result
	result := &config.Result{}
	err = json.Unmarshal(tasks.Tasks[0].Result, result)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling up result: %w", err)
	}

	// return result
	return result, nil
}

func templateUpdateRequired(instance *managementv1.DevsyWorkspaceInstance) bool {
	var templateResolved, templateChangesAvailable bool
	for _, condition := range instance.Status.Conditions {
		if condition.Type == storagev1.InstanceTemplateResolved {
			templateResolved = condition.Status == corev1.ConditionTrue
			continue
		}

		if condition.Type == storagev1.InstanceTemplateSynced {
			templateChangesAvailable = condition.Status == corev1.ConditionFalse &&
				condition.Reason == "TemplateChangesAvailable"
			continue
		}
	}

	return !templateResolved || templateChangesAvailable
}

func printInstanceInfo(instance *managementv1.DevsyWorkspaceInstance) {
	workspaceConfig, _ := json.Marshal(struct {
		Target     storagev1.WorkspaceTarget
		Template   *storagev1.TemplateRef
		Parameters string
	}{
		Target:     instance.Spec.Target,
		Template:   instance.Spec.TemplateRef,
		Parameters: instance.Spec.Parameters,
	})
	log.Debug("Starting pro workspace with configuration", string(workspaceConfig))
}

func observeTask(
	ctx context.Context,
	managementClient kube.Interface,
	instance *managementv1.DevsyWorkspaceInstance,
	taskID string,
	reporter status.Reporter,
) (int, error) {
	var (
		exitCode int
		err      error
	)
	errChan := make(chan error, 1)

	printCtx, cancelPrintCtx := context.WithCancel(context.Background())
	defer cancelPrintCtx()

	go func() {
		// cancel ongoing task if context is done
		select {
		case <-ctx.Done():
			timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			defer cancelPrintCtx()

			_, err := managementClient.Loft().
				ManagementV1().
				DevsyWorkspaceInstances(instance.Namespace).
				Cancel(timeoutCtx, instance.Name, &managementv1.DevsyWorkspaceInstanceCancel{
					TaskID: taskID,
				}, metav1.CreateOptions{})
			if err != nil {
				errChan <- err
			} else {
				errChan <- errors.New("canceled")
			}
		case <-errChan:
		case <-printCtx.Done():
		}
	}()
	go func() {
		exitCode, err = printLogs(printCtx, managementClient, instance, taskID, reporter)
		errChan <- err
	}()

	return exitCode, <-errChan
}

type MessageType byte

const (
	StdoutData MessageType = 0
	StderrData MessageType = 2
	ExitCode   MessageType = 6
)

type Message struct {
	Type     MessageType `json:"type"`
	ExitCode int         `json:"exitCode,omitempty"`
	Bytes    []byte      `json:"bytes,omitempty"`
}

func printLogs(
	ctx context.Context,
	managementClient kube.Interface,
	workspace *managementv1.DevsyWorkspaceInstance,
	taskID string,
	reporter status.Reporter,
) (int, error) {
	logsReader, err := openTaskLogsStream(ctx, managementClient, workspace, taskID)
	if err != nil {
		return -1, err
	}
	defer func() { _ = logsReader.Close() }()

	scanner := newLogScanner(logsReader)

	streams := newLogOutputStreams(reporter)
	defer streams.Close()

	exitCode, done, err := streamLogMessages(scanner, streams.stdout(), streams.stderrStreamer)
	if done {
		return exitCode, err
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return 0, nil
		}
		return -1, fmt.Errorf("logs reader error: %w", err)
	}

	return 0, nil
}

func openTaskLogsStream(
	ctx context.Context,
	managementClient kube.Interface,
	workspace *managementv1.DevsyWorkspaceInstance,
	taskID string,
) (io.ReadCloser, error) {
	log.Debugf("printing logs of task: %s", taskID)
	logsReader, err := managementClient.Loft().ManagementV1().RESTClient().Get().
		Namespace(workspace.Namespace).
		Resource("devsyworkspaceinstances").
		Name(workspace.Name).
		SubResource("log").
		VersionedParams(&managementv1.DevsyWorkspaceInstanceLogOptions{
			TaskID: taskID,
			Follow: true,
		}, builders.ParameterCodec).
		Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting task logs: %w", err)
	}
	return logsReader, nil
}

func newLogScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)

	// Increase the maximum token size to handle very long lines.
	// Here, we set a maximum capacity of 1MB.
	const maxCapacity = 1024 * 1024 // 1MB
	buf := make([]byte, 1024)       // starting buffer size of 1KB
	scanner.Buffer(buf, maxCapacity)

	return scanner
}

// logOutputStreams bundles the stdout/stderr JSON streamers used to print
// task logs, plus the status-sniffing writer wrapped around stdout.
type logOutputStreams struct {
	stdoutStreamer io.WriteCloser
	stdoutDone     chan struct{}
	stderrStreamer io.WriteCloser
	stderrDone     chan struct{}
	statusWriter   *statusSniffingWriter
}

func newLogOutputStreams(reporter status.Reporter) *logOutputStreams {
	stdoutStreamer, stdoutDone := log.PipeJSONStream()
	stderrStreamer, stderrDone := log.PipeJSONStream()

	// The remote task runs the same devsy CLI, so its stdout carries the
	// same NDJSON status lines a local `up` does; sniff them out here.
	statusWriter := newStatusSniffingWriter(stdoutStreamer, reporter)

	return &logOutputStreams{
		stdoutStreamer: stdoutStreamer,
		stdoutDone:     stdoutDone,
		stderrStreamer: stderrStreamer,
		stderrDone:     stderrDone,
		statusWriter:   statusWriter,
	}
}

func (s *logOutputStreams) Close() {
	_ = s.statusWriter.Close()
	_ = s.stdoutStreamer.Close()
	_ = s.stderrStreamer.Close()
	<-s.stdoutDone
	<-s.stderrDone
}

func (s *logOutputStreams) stdout() io.Writer {
	return s.statusWriter
}

// streamLogMessages reads NDJSON-encoded Message lines from scanner and
// writes their payloads to stdout/stderr until an ExitCode message, a write
// error, or EOF is reached. done reports whether the caller should return
// immediately with (exitCode, err) rather than falling through to
// scanner.Err().
func streamLogMessages(scanner *bufio.Scanner, stdout, stderr io.Writer) (int, bool, error) {
	for scanner.Scan() {
		line := scanner.Text()

		message := &Message{}
		if err := json.Unmarshal([]byte(line), message); err != nil {
			return -1, true, fmt.Errorf(
				"error parsing JSON from logs reader: %w, line: %s",
				err,
				line,
			)
		}

		exitCode, done, err := writeMessage(stdout, stderr, message)
		if done {
			return exitCode, true, err
		}
	}

	return 0, false, nil
}

func writeMessage(stdout, stderr io.Writer, message *Message) (int, bool, error) {
	switch message.Type {
	case StdoutData:
		if _, err := stdout.Write(message.Bytes); err != nil {
			log.Debugf("error read stdout: %v", err)
			return 1, true, err
		}
	case StderrData:
		if _, err := stderr.Write(message.Bytes); err != nil {
			log.Debugf("error read stderr: %v", err)
			return 1, true, err
		}
	case ExitCode:
		log.Debugf("exit code: %d", message.ExitCode)
		return message.ExitCode, true, nil
	}

	return 0, false, nil
}

// statusSniffingWriter splits a byte stream into lines, forwards each
// status NDJSON envelope line to reporter, and passes every other line
// through to next unchanged.
type statusSniffingWriter struct {
	next     io.Writer
	reporter status.Reporter
	buf      bytes.Buffer
}

func newStatusSniffingWriter(next io.Writer, reporter status.Reporter) *statusSniffingWriter {
	return &statusSniffingWriter{next: next, reporter: reporter}
}

func (w *statusSniffingWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// Incomplete line: put it back for the next Write/Close.
			w.buf.Reset()
			w.buf.WriteString(line)
			break
		}
		if event, ok := config.ParseStatusLine(line); ok {
			w.reporter.Report(event)
			continue
		}
		if _, err := w.next.Write([]byte(line)); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

// Close flushes any trailing bytes left without a newline, sniffing them the
// same way Write does so a final status envelope on an unterminated line is
// reported rather than leaking into the caller's stream as raw JSON.
func (w *statusSniffingWriter) Close() error {
	if w.buf.Len() == 0 {
		return nil
	}
	trailing := w.buf.String()
	w.buf.Reset()
	if event, ok := config.ParseStatusLine(trailing); ok {
		w.reporter.Report(event)
		return nil
	}
	_, err := w.next.Write([]byte(trailing))
	return err
}

const (
	TaskStatusRunning = "Running"
	TaskStatusSucceed = "Succeeded"
	TaskStatusFailed  = "Failed"
)

const (
	TaskTypeUp     = "up"
	TaskTypeStop   = "stop"
	TaskTypeDelete = "delete"
)

func findActiveUpTask(
	ctx context.Context,
	managementClient kube.Interface,
	instance *managementv1.DevsyWorkspaceInstance,
) (*managementv1.DevsyWorkspaceInstanceTask, error) {
	tasks := &managementv1.DevsyWorkspaceInstanceTasks{}
	err := managementClient.Loft().ManagementV1().RESTClient().Get().
		Namespace(instance.Namespace).
		Resource("devsyworkspaceinstances").
		Name(instance.Name).
		SubResource("tasks").
		Do(ctx).
		Into(tasks)

	for _, task := range tasks.Tasks {
		if task.Status == TaskStatusRunning && task.Type == TaskTypeUp {
			return &task, nil
		}
	}

	return nil, err
}
