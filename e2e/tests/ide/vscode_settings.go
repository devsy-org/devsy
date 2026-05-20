package ide

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/ide/vscode"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("VS Code Settings JSONC Handling", ginkgo.Label("ide"), func() {
	var (
		tmpHome     string
		origHome    string
		settingsDir string
	)

	ginkgo.BeforeEach(func() {
		var err error
		origHome = os.Getenv("HOME")

		tmpHome, err = os.MkdirTemp("", "vscode-settings-test-*")
		framework.ExpectNoError(err)

		// On Linux, VS Code settings live at ~/.config/Code/User/settings.json
		settingsDir = filepath.Join(tmpHome, ".config", "Code", "User")
		err = os.MkdirAll(settingsDir, 0o750)
		framework.ExpectNoError(err)

		err = os.Setenv("HOME", tmpHome)
		framework.ExpectNoError(err)
	})

	ginkgo.AfterEach(func() {
		err := os.Setenv("HOME", origHome)
		framework.ExpectNoError(err)
		_ = os.RemoveAll(tmpHome)
	})

	settingsPath := func() string {
		return filepath.Join(settingsDir, "settings.json")
	}

	writeSettings := func(content string) {
		err := os.WriteFile(settingsPath(), []byte(content), 0o600)
		framework.ExpectNoError(err)
	}

	readParsedSettings := func() map[string]any {
		data, err := os.ReadFile(settingsPath())
		framework.ExpectNoError(err)
		var settings map[string]any
		err = json.Unmarshal(data, &settings)
		framework.ExpectNoError(err)
		return settings
	}

	ginkgo.DescribeTable("should parse JSONC settings",
		func(content string, expectedKeys map[string]any) {
			writeSettings(content)
			vscode.EnsureHostSettings(vscode.FlavorStable)

			settings := readParsedSettings()
			for key, val := range expectedKeys {
				gomega.Expect(settings).To(gomega.HaveKeyWithValue(key, val))
			}
			gomega.Expect(settings).To(gomega.HaveKeyWithValue("remote.SSH.useExecServer", false))
		},
		ginkgo.Entry("with line comments", `{
    // This is a line comment
    "editor.fontSize": 14,
    // Another comment
    "editor.tabSize": 4
}`, map[string]any{
			"editor.fontSize": float64(14),
			"editor.tabSize":  float64(4),
		}),
		ginkgo.Entry("with block comments", `{
    /* Block comment explaining font size */
    "editor.fontSize": 16,
    /*
     * Multi-line block comment
     * with multiple lines
     */
    "editor.wordWrap": "on"
}`, map[string]any{
			"editor.fontSize": float64(16),
			"editor.wordWrap": "on",
		}),
		ginkgo.Entry("with trailing commas", `{
    "editor.fontSize": 14,
    "editor.tabSize": 4,
    "editor.wordWrap": "on",
}`, map[string]any{
			"editor.fontSize": float64(14),
			"editor.tabSize":  float64(4),
			"editor.wordWrap": "on",
		}),
		ginkgo.Entry("preserving existing settings during merge", `{
    // User's custom font settings
    "editor.fontSize": 18,
    "editor.fontFamily": "Fira Code",
    /* Terminal settings */
    "terminal.integrated.fontSize": 13,
    "workbench.colorTheme": "One Dark Pro",
}`, map[string]any{
			"editor.fontSize":              float64(18),
			"editor.fontFamily":            "Fira Code",
			"terminal.integrated.fontSize": float64(13),
			"workbench.colorTheme":         "One Dark Pro",
		}),
	)

	ginkgo.It("should handle real-world VS Code settings with mixed JSONC", func() {
		writeSettings(`{
    // ========================
    // Editor Settings
    // ========================
    "editor.fontSize": 14,
    "editor.tabSize": 4,
    "editor.formatOnSave": true,
    "editor.defaultFormatter": "esbenp.prettier-vscode",
    "editor.rulers": [80, 120],
    "editor.minimap.enabled": false,

    /*
     * Terminal configuration
     * Updated: 2024-01-15
     */
    "terminal.integrated.fontSize": 13,
    "terminal.integrated.defaultProfile.linux": "zsh",

    // Git settings
    "git.autofetch": true,
    "git.confirmSync": false,

    /* File associations */
    "files.associations": {
        "*.jsonc": "jsonc",
        "*.env.*": "dotenv",
    },

    // Extension-specific settings
    "[python]": {
        "editor.defaultFormatter": "ms-python.black-formatter",
        "editor.formatOnSave": true,
    },

    // Remote SSH (user may have this set to true)
    "remote.SSH.useExecServer": true,

    // Telemetry
    "telemetry.telemetryLevel": "off",
}`)

		vscode.EnsureHostSettings(vscode.FlavorStable)

		settings := readParsedSettings()
		// Verify existing settings are preserved
		gomega.Expect(settings).To(gomega.HaveKeyWithValue("editor.fontSize", float64(14)))
		gomega.Expect(settings).To(gomega.HaveKeyWithValue("editor.formatOnSave", true))
		gomega.Expect(settings).To(gomega.HaveKeyWithValue("editor.minimap.enabled", false))
		gomega.Expect(settings).
			To(gomega.HaveKeyWithValue("terminal.integrated.fontSize", float64(13)))
		gomega.Expect(settings).To(gomega.HaveKeyWithValue("git.autofetch", true))
		gomega.Expect(settings).To(gomega.HaveKeyWithValue("telemetry.telemetryLevel", "off"))

		// Verify nested objects are preserved
		gomega.Expect(settings).To(gomega.HaveKey("files.associations"))
		gomega.Expect(settings).To(gomega.HaveKey("[python]"))

		// Verify the critical setting was overridden from true to false
		gomega.Expect(settings).To(gomega.HaveKeyWithValue("remote.SSH.useExecServer", false))
	})

	ginkgo.It("should create settings file when none exists", func() {
		// Remove the settings directory entirely to test creation from scratch
		err := os.RemoveAll(settingsDir)
		framework.ExpectNoError(err)

		vscode.EnsureHostSettings(vscode.FlavorStable)

		// Verify the file was created
		_, err = os.Stat(settingsPath())
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		settings := readParsedSettings()
		gomega.Expect(settings).To(gomega.HaveKeyWithValue("remote.SSH.useExecServer", false))
	})

	ginkgo.It("should handle empty settings file", func() {
		writeSettings(`{}`)

		vscode.EnsureHostSettings(vscode.FlavorStable)

		settings := readParsedSettings()
		gomega.Expect(settings).To(gomega.HaveKeyWithValue("remote.SSH.useExecServer", false))
	})
})
