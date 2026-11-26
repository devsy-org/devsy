import { mount } from "svelte"
import App from "./App.svelte"
import { installRendererErrorReporting } from "./lib/utils/renderer-error.js"

installRendererErrorReporting(window, (summary) => {
  window.electronAPI?.reportRendererError(summary)
})

const target = document.getElementById("app")
if (!target) throw new Error("App mount element not found")

const app = mount(App, { target })

export default app
