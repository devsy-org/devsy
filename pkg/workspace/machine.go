package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/file"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/skevetter/log"
	"github.com/skevetter/log/survey"
	"github.com/skevetter/log/terminal"
)

// ListMachines returns all machines configured in the given Devsy context.
func ListMachines(devsyConfig *config.Config, log log.Logger) ([]*providerpkg.Machine, error) {
	return listMachines(devsyConfig, log)
}

func listMachines(devsyConfig *config.Config, log log.Logger) ([]*providerpkg.Machine, error) {
	machineDir, err := providerpkg.GetMachinesDir(devsyConfig.DefaultContext)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(machineDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	retMachines := []*providerpkg.Machine{}
	for _, entry := range entries {
		machineConfig, err := providerpkg.LoadMachineConfig(
			devsyConfig.DefaultContext,
			entry.Name(),
		)
		if err != nil {
			log.Warnf("could not load machine: machine=%s, error=%v", entry.Name(), err)
			continue
		}

		retMachines = append(retMachines, machineConfig)
	}

	return retMachines, nil
}

func ResolveMachine(
	devsyConfig *config.Config,
	args []string,
	userOptions []string,
	log log.Logger,
) (client.Client, error) {
	machineClient, err := resolveMachine(devsyConfig, args, log)
	if err != nil {
		return nil, err
	}

	// refresh options
	err = machineClient.RefreshOptions(context.TODO(), userOptions, false)
	if err != nil {
		return nil, err
	}

	return machineClient, nil
}

func resolveMachine(
	devsyConfig *config.Config,
	args []string,
	log log.Logger,
) (client.Client, error) {
	// check if we have no args
	if len(args) == 0 {
		return nil, fmt.Errorf("please specify the machine name")
	}

	// convert to id
	machineID := ToID(args[0])

	// check if desired id already exists
	if providerpkg.MachineExists(devsyConfig.DefaultContext, machineID) {
		log.Infof("Machine %s already exists", machineID)
		return loadExistingMachine(machineID, devsyConfig, log)
	}

	// get default provider
	defaultProvider, _, err := LoadProviders(devsyConfig, log)
	if err != nil {
		return nil, err
	}

	// resolve workspace
	machineObj, err := createMachine(
		devsyConfig.DefaultContext,
		machineID,
		defaultProvider.Config.Name,
	)
	if err != nil {
		return nil, err
	}

	// create a new client
	machineClient, err := clientimplementation.NewMachineClient(
		devsyConfig,
		defaultProvider.Config,
		machineObj,
		log,
	)
	if err != nil {
		_ = os.RemoveAll(filepath.Dir(machineObj.Origin))
		return nil, err
	}

	return machineClient, nil
}

// MachineExists checks if the given workspace already exists.
func MachineExists(devsyConfig *config.Config, args []string) string {
	if len(args) == 0 {
		return ""
	}

	// check if workspace already exists
	_, name := file.IsLocalDir(args[0])

	// convert to id
	machineID := ToID(name)

	// already exists?
	if !providerpkg.MachineExists(devsyConfig.DefaultContext, machineID) {
		return ""
	}

	return machineID
}

// GetMachine creates a machine client.
func GetMachine(
	devsyConfig *config.Config,
	args []string,
	log log.Logger,
) (client.MachineClient, error) {
	// check if we have no args
	if len(args) == 0 {
		return selectMachine(devsyConfig, log)
	}

	// check if workspace already exists
	_, name := file.IsLocalDir(args[0])

	// convert to id
	machineID := ToID(name)

	// already exists?
	if !providerpkg.MachineExists(devsyConfig.DefaultContext, machineID) {
		return nil, fmt.Errorf("machine %s doesn't exist", machineID)
	}

	// load workspace config
	return loadExistingMachine(machineID, devsyConfig, log)
}

func selectMachine(devsyConfig *config.Config, log log.Logger) (client.MachineClient, error) {
	if !terminal.IsTerminalIn {
		return nil, errProvideWorkspaceArg
	}

	// ask which machine to use
	machinesDir, err := providerpkg.GetMachinesDir(devsyConfig.DefaultContext)
	if err != nil {
		return nil, err
	}

	machineIDs := []string{}
	seversDirs, err := os.ReadDir(machinesDir)
	if err != nil {
		return nil, err
	}

	for _, workspace := range seversDirs {
		machineIDs = append(machineIDs, workspace.Name())
	}
	if len(machineIDs) == 0 {
		return nil, errProvideWorkspaceArg
	}

	answer, err := log.Question(&survey.QuestionOptions{
		Question:     "Please select a machine from the list below",
		DefaultValue: machineIDs[0],
		Options:      machineIDs,
		Sort:         true,
	})
	if err != nil {
		return nil, err
	}

	// load workspace
	return loadExistingMachine(answer, devsyConfig, log)
}

func loadExistingMachine(
	machineID string,
	devsyConfig *config.Config,
	log log.Logger,
) (client.MachineClient, error) {
	machineConfig, err := providerpkg.LoadMachineConfig(devsyConfig.DefaultContext, machineID)
	if err != nil {
		return nil, err
	}

	providerWithOptions, err := FindProvider(devsyConfig, machineConfig.Provider.Name, log)
	if err != nil {
		return nil, err
	}

	return clientimplementation.NewMachineClient(
		devsyConfig,
		providerWithOptions.Config,
		machineConfig,
		log,
	)
}

func createMachine(context, machineID, providerName string) (*providerpkg.Machine, error) {
	// get the machine dir
	machineDir, err := providerpkg.GetMachineDir(context, machineID)
	if err != nil {
		return nil, err
	}

	// save machine config
	machine := &providerpkg.Machine{
		ID:      machineID,
		Context: context,
		Provider: providerpkg.MachineProviderConfig{
			Name: providerName,
		},
		CreationTimestamp: types.Now(),
		Origin:            filepath.Join(machineDir, providerpkg.MachineConfigFile),
	}

	// create machine folder
	err = providerpkg.SaveMachineConfig(machine)
	if err != nil {
		_ = os.RemoveAll(machineDir)
		return nil, err
	}

	return machine, nil
}

func SingleMachineName(devsyConfig *config.Config, provider string, log log.Logger) string {
	legacyMachineName := config.BinaryName + "-shared-" + provider
	machines, err := listMachines(devsyConfig, log)
	if err == nil {
		for _, machine := range machines {
			if machine.Provider.Name == provider && machine.ID == legacyMachineName {
				return legacyMachineName
			}
		}
	}

	return encoding.SafeConcatNameMax(
		[]string{config.BinaryName + "-shared", provider, encoding.GetMachineUIDShort(log)},
		encoding.MachineUIDLength,
	)
}
