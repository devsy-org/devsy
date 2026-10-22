//go:build linux

package ghfixture

const (
	hostUser              = "host-user"
	localUser             = "local-user"
	secondUser            = "second-user"
	envLogLevel           = "DEVSY_LOG_LEVEL"
	envEnterpriseToken    = "GH_ENTERPRISE_TOKEN"
	envSessionBus         = "DBUS_SESSION_BUS_ADDRESS"
	envToken              = "GH_TOKEN"
	commandAPI            = "api"
	debugLevel            = "debug"
	envPath               = "PATH"
	customConfigDirectory = "/fixture/custom-gh"
	pendingEditor         = "pending"
)

const (
	envGHPath     = "GH_PATH"
	envGHBrowser  = "GH_BROWSER"
	envGHConfig   = "GH_CONFIG_DIR"
	envXDGConfig  = "XDG_CONFIG_HOME"
	githubHost    = "github.com"
	githubAPIHost = "api.github.com"
)
