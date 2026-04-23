package config

// ConfigDirName is the hidden directory name used for Devsy configuration.
const ConfigDirName = "." + RepoName

func GetConfigDir() (string, error) {
	return DefaultPathManager().DataDir()
}

func GetConfigPath() (string, error) {
	return DefaultPathManager().ConfigFilePath()
}
