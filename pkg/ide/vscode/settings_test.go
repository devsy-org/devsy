package vscode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type SettingsSuite struct {
	suite.Suite
	tmpDir string
}

func TestSettingsSuite(t *testing.T) {
	suite.Run(t, new(SettingsSuite))
}

func (s *SettingsSuite) SetupTest() {
	s.tmpDir = s.T().TempDir()
}

func (s *SettingsSuite) TestMergeUserSettings_CreatesNewFile() {
	path := filepath.Join(s.tmpDir, "User", "settings.json")
	err := mergeUserSettings(path, map[string]any{
		settingUseExecServer: false,
	})
	s.Require().NoError(err)

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture
	s.Require().NoError(err)

	var settings map[string]any
	s.Require().NoError(json.Unmarshal(data, &settings))
	s.Equal(false, settings[settingUseExecServer])
}

func (s *SettingsSuite) TestMergeUserSettings_PreservesExistingSettings() {
	path := filepath.Join(s.tmpDir, "settings.json")
	existing := map[string]any{
		"editor.fontSize":      float64(14),
		"workbench.colorTheme": "One Dark Pro",
	}
	s.writeJSON(path, existing)

	err := mergeUserSettings(path, map[string]any{
		settingUseExecServer: false,
	})
	s.Require().NoError(err)

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture
	s.Require().NoError(err)

	var settings map[string]any
	s.Require().NoError(json.Unmarshal(data, &settings))
	s.Equal(float64(14), settings["editor.fontSize"])
	s.Equal("One Dark Pro", settings["workbench.colorTheme"])
	s.Equal(false, settings[settingUseExecServer])
}

func (s *SettingsSuite) TestMergeUserSettings_DoesNotRewriteIfAlreadySet() {
	path := filepath.Join(s.tmpDir, "settings.json")
	existing := map[string]any{
		settingUseExecServer: false,
	}
	s.writeJSON(path, existing)

	info1, _ := os.Stat(path)

	err := mergeUserSettings(path, map[string]any{
		settingUseExecServer: false,
	})
	s.Require().NoError(err)

	info2, _ := os.Stat(path)
	s.Equal(info1.ModTime(), info2.ModTime())
}

func (s *SettingsSuite) TestMergeUserSettings_OverridesWrongValue() {
	path := filepath.Join(s.tmpDir, "settings.json")
	existing := map[string]any{
		settingUseExecServer: true,
	}
	s.writeJSON(path, existing)

	err := mergeUserSettings(path, map[string]any{
		settingUseExecServer: false,
	})
	s.Require().NoError(err)

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture
	s.Require().NoError(err)

	var settings map[string]any
	s.Require().NoError(json.Unmarshal(data, &settings))
	s.Equal(false, settings[settingUseExecServer])
}

func (s *SettingsSuite) TestMergeUserSettings_HandlesLineComments() {
	path := filepath.Join(s.tmpDir, "settings.json")
	s.writeRaw(path, `{
    // Editor configuration
    "editor.fontSize": 14,
    // Theme setting
    "workbench.colorTheme": "Monokai"
}`)

	err := mergeUserSettings(path, map[string]any{
		settingUseExecServer: false,
	})
	s.Require().NoError(err)

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture
	s.Require().NoError(err)

	var settings map[string]any
	s.Require().NoError(json.Unmarshal(data, &settings))
	s.Equal(float64(14), settings["editor.fontSize"])
	s.Equal("Monokai", settings["workbench.colorTheme"])
	s.Equal(false, settings[settingUseExecServer])
}

func (s *SettingsSuite) TestMergeUserSettings_HandlesBlockComments() {
	path := filepath.Join(s.tmpDir, "settings.json")
	s.writeRaw(path, `{
    /* Main editor settings */
    "editor.fontSize": 16,
    /*
     * Multi-line block comment
     * describing the theme
     */
    "workbench.colorTheme": "Solarized Dark"
}`)

	err := mergeUserSettings(path, map[string]any{
		settingUseExecServer: false,
	})
	s.Require().NoError(err)

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture
	s.Require().NoError(err)

	var settings map[string]any
	s.Require().NoError(json.Unmarshal(data, &settings))
	s.Equal(float64(16), settings["editor.fontSize"])
	s.Equal("Solarized Dark", settings["workbench.colorTheme"])
	s.Equal(false, settings[settingUseExecServer])
}

func (s *SettingsSuite) TestMergeUserSettings_HandlesTrailingCommas() {
	path := filepath.Join(s.tmpDir, "settings.json")
	s.writeRaw(path, `{
    "editor.fontSize": 12,
    "workbench.colorTheme": "Nord",
    "editor.tabSize": 4,
}`)

	err := mergeUserSettings(path, map[string]any{
		settingUseExecServer: false,
	})
	s.Require().NoError(err)

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture
	s.Require().NoError(err)

	var settings map[string]any
	s.Require().NoError(json.Unmarshal(data, &settings))
	s.Equal(float64(12), settings["editor.fontSize"])
	s.Equal("Nord", settings["workbench.colorTheme"])
	s.Equal(float64(4), settings["editor.tabSize"])
	s.Equal(false, settings[settingUseExecServer])
}

func (s *SettingsSuite) TestMergeUserSettings_HandlesMixedJSONC() {
	path := filepath.Join(s.tmpDir, "settings.json")
	s.writeRaw(path, `{
    // Line comment at the top
    "editor.fontSize": 14,
    /* Block comment */
    "workbench.colorTheme": "One Dark Pro",
    "editor.wordWrap": "on", // inline comment
    /*
     * Another block comment
     */
    "files.autoSave": "afterDelay",
}`)

	err := mergeUserSettings(path, map[string]any{
		settingUseExecServer: false,
	})
	s.Require().NoError(err)

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture
	s.Require().NoError(err)

	var settings map[string]any
	s.Require().NoError(json.Unmarshal(data, &settings))
	s.Equal(float64(14), settings["editor.fontSize"])
	s.Equal("One Dark Pro", settings["workbench.colorTheme"])
	s.Equal("on", settings["editor.wordWrap"])
	s.Equal("afterDelay", settings["files.autoSave"])
	s.Equal(false, settings[settingUseExecServer])
}

func (s *SettingsSuite) TestMergeUserSettings_HandlesCommentsAroundTargetSetting() {
	path := filepath.Join(s.tmpDir, "settings.json")
	s.writeRaw(path, `{
    "editor.fontSize": 14,
    // This setting controls SSH exec server mode
    "remote.SSH.useExecServer": true,
    /* Keep this disabled for proxy compatibility */
    "workbench.colorTheme": "Monokai"
}`)

	err := mergeUserSettings(path, map[string]any{
		settingUseExecServer: false,
	})
	s.Require().NoError(err)

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture
	s.Require().NoError(err)

	var settings map[string]any
	s.Require().NoError(json.Unmarshal(data, &settings))
	s.Equal(float64(14), settings["editor.fontSize"])
	s.Equal("Monokai", settings["workbench.colorTheme"])
	s.Equal(false, settings[settingUseExecServer])
}

func (s *SettingsSuite) writeJSON(path string, v any) {
	s.T().Helper()
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o750))
	data, err := json.Marshal(v)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, data, 0o600))
}

func (s *SettingsSuite) writeRaw(path string, content string) {
	s.T().Helper()
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o750))
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
}
