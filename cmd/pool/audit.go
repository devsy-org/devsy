package pool

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/pool/audit"
	"github.com/spf13/cobra"
)

const (
	auditDirFlag     = "dir"
	auditSessionFlag = "session"
	auditSinceFlag   = "since"
)

var errAuditSince = errors.New("--since must be an RFC3339 timestamp or a YYYY-MM-DD UTC date")

type auditCmd struct {
	dir     string
	session string
	since   string
	output  string
}

func NewAuditCmd() *cobra.Command {
	cmd := &auditCmd{}
	command := &cobra.Command{
		Use:   "audit",
		Short: "Read local pool audit records as newline-delimited JSON",
		Long: `Read local pool audit records as newline-delimited JSON.

Warnings go to stderr. Do not merge stderr into stdout.
The directory must be owned by the current user and must not be group or world writable.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(
				cobraCmd.OutOrStdout(),
				cobraCmd.ErrOrStderr(),
				cobraCmd.Flags().Changed(auditSinceFlag),
			)
		},
	}
	f := command.Flags()
	f.StringVar(&cmd.dir, auditDirFlag, "", "Path to the local audit directory (required)")
	f.StringVar(&cmd.session, auditSessionFlag, "", "Only include records for this session ID")
	f.StringVar(
		&cmd.since,
		auditSinceFlag,
		"",
		"Include records at or after an RFC3339 time or UTC date",
	)
	f.StringVar(
		&cmd.output,
		names.Output,
		output.ModeJSON,
		"Output format (only json is supported)",
	)
	_ = command.MarkFlagRequired(auditDirFlag)
	return command
}

func (cmd *auditCmd) Run(w, stderr io.Writer, sinceSet bool) error {
	if cmd.output != output.ModeJSON {
		return errors.New("pool audit only supports --output json")
	}
	if cmd.dir == "" {
		return errors.New("--dir must not be empty")
	}
	filter := audit.ReaderFilter{Session: cmd.session}
	if sinceSet {
		since, err := parseAuditSince(cmd.since)
		if err != nil {
			return err
		}
		filter.Since = since
	}
	reader, err := audit.OpenReader(cmd.dir)
	if err != nil {
		return err
	}
	return reader.Scan(
		filter,
		func(line []byte) error {
			return writeAuditRecord(w, line)
		},
		func(warning error) {
			_, _ = fmt.Fprintf(stderr, "warning: %v\n", warning)
		},
	)
}

func writeAuditRecord(w io.Writer, record []byte) error {
	if _, err := w.Write(record); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func parseAuditSince(value string) (*time.Time, error) {
	if t, err := time.Parse(time.DateOnly, value); err == nil {
		return &t, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errAuditSince
	}
	utc := t.UTC()
	return &utc, nil
}
