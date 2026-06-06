package helperimage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/spf13/cobra"
)

type GetImagePlatformsCommand struct {
	*flags.GlobalFlags
}

// NewGetImagePlatformsCmd creates a new command.
func NewGetImagePlatformsCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &GetImagePlatformsCommand{
		GlobalFlags: flags,
	}
	shellCmd := &cobra.Command{
		Use:   "get-image-platforms [image-name]",
		Short: "List the os/arch platforms an image supports",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	return shellCmd
}

func (cmd *GetImagePlatformsCommand) Run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("image name is missing")
	}

	platforms, err := image.GetImagePlatforms(ctx, args[0])
	if err != nil {
		return err
	}
	out, err := json.Marshal(map[string][]string{"platforms": platforms})
	if err != nil {
		return err
	}

	fmt.Println(string(out)) //nolint:forbidigo // helper prints to stdout

	return nil
}
