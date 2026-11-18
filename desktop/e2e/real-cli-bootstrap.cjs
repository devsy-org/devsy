const { app } = require("electron")
const { isAbsolute, join } = require("node:path")

const root = process.env.REAL_CLI_ROOT
if (!root || !isAbsolute(root))
  throw new Error("REAL_CLI_ROOT must be absolute")
app.setPath("userData", join(root, "electron-user-data"))
app.setPath("sessionData", join(root, "electron-session-data"))
app.setAppLogsPath(join(root, "electron-logs"))
