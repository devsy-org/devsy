package cmd

import (
	"context"
	"errors"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// readInspectionInvocation observes metadata and argv without setters or a second
// Cobra parse. Invalid syntax remains the responsibility of normal execution.
func readInspectionInvocation(command *cobra.Command, args []string) flags.InspectionInvocation {
	reader := inspectionReader{command: command}
	if !flags.IsDependencyInspection(command) {
		return reader.invocation
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}
		if name, long := strings.CutPrefix(arg, "--"); long {
			index += reader.longFlag(name, args[index+1:])
		} else if cluster, short := strings.CutPrefix(arg, "-"); short {
			index += reader.shortFlags(cluster, args[index+1:])
		}
	}
	reader.invocation.JSON = reader.invocation.JSON || reader.invalidJSON
	return reader.invocation
}

type inspectionReader struct {
	command     *cobra.Command
	invocation  flags.InspectionInvocation
	invalidJSON bool
}

func (reader *inspectionReader) longFlag(argument string, remaining []string) int {
	name, value, assigned := strings.Cut(argument, "=")
	flag := inspectionFlag(reader.command, name, false)
	if flag == nil {
		return 0
	}
	value, consumed := inspectionFlagValue(flag, value, assigned, remaining)
	reader.record(flag, value)
	return consumed
}

func (reader *inspectionReader) shortFlags(cluster string, remaining []string) int {
	for len(cluster) > 0 {
		flag := inspectionFlag(reader.command, cluster[:1], true)
		cluster = cluster[1:]
		if flag == nil {
			// pflag rejects the entire cluster at the first unknown shorthand.
			return 0
		}
		if strings.HasPrefix(cluster, "=") || flag.NoOptDefVal == "" {
			value, consumed := inspectionFlagValue(
				flag,
				strings.TrimPrefix(cluster, "="),
				len(cluster) > 0,
				remaining,
			)
			reader.record(flag, value)
			return consumed
		}
		reader.record(flag, flag.NoOptDefVal)
	}
	return 0
}

func inspectionFlag(command *cobra.Command, name string, shorthand bool) *pflag.Flag {
	lookup := func(set *pflag.FlagSet) *pflag.Flag {
		if shorthand {
			return set.ShorthandLookup(name)
		}
		return set.Lookup(name)
	}
	if flag := lookup(command.Flags()); flag != nil {
		return flag
	}
	for current := command; current != nil; current = current.Parent() {
		if flag := lookup(current.PersistentFlags()); flag != nil {
			return flag
		}
	}
	return nil
}

func inspectionFlagValue(
	flag *pflag.Flag,
	value string,
	assigned bool,
	remaining []string,
) (string, int) {
	if assigned {
		return value, 0
	}
	if flag.NoOptDefVal != "" {
		return flag.NoOptDefVal, 0
	}
	if len(remaining) > 0 {
		return remaining[0], 1
	}
	return "", 0
}

func (reader *inspectionReader) record(flag *pflag.Flag, value string) {
	switch flag.Name {
	case "json":
		parsed, err := strconv.ParseBool(value)
		reader.invocation.JSON = parsed
		reader.invalidJSON = reader.invalidJSON || err != nil
	case "provider":
		reader.invocation.ProviderPresent, reader.invocation.Provider = true, value
	}
}

func invocationPanic(recovered any, inspection bool) *clierr.CLIError {
	if inspection {
		return &clierr.CLIError{
			Code:    clierr.CodePanic,
			Message: "internal dependency inspection error",
		}
	}
	log.Errorf("panic: %v\n%s", recovered, debug.Stack())
	return clierr.NewPanic(recovered)
}

func inspectionStartupError(command *cobra.Command, err error) error {
	if !flags.IsDependencyInspection(command) {
		return err
	}
	if canceled := command.Context().Err(); canceled != nil {
		return canceled
	}
	if err == nil ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &clierr.CLIError{
		Code:    clierr.Code("dependency_policy_blocked"),
		Message: "cannot prepare passive dependency inspection inputs",
	}
}
