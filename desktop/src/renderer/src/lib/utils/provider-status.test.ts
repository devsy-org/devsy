import { describe, expect, it } from "vitest"
import { providerStatus } from "./provider-status.js"

describe("providerStatus", () => {
  it("preserves structured failure metadata for the desktop", () => {
    const result = providerStatus(
      { name: "docker", state: { initialized: false } },
      {
        activity: "initializing",
        state: "failed",
        error: "Docker is unavailable.",
        errorCode: "docker_daemon_unreachable",
        errorHint: "Start Docker and retry.",
        errorContext: { context: "desktop-linux" },
      },
    )

    expect(result).toEqual({
      kind: "failed",
      label: "failed",
      error: "Docker is unavailable.",
      errorCode: "docker_daemon_unreachable",
      errorHint: "Start Docker and retry.",
      errorContext: { context: "desktop-linux" },
    })
  })
})
