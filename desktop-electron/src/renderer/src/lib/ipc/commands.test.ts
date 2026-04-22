import { beforeEach, describe, expect, it } from "vitest"
import { mockInvoke, resetTauriMocks } from "$lib/__mocks__/tauri.js"

// Import after mocks are set up
import {
  auditByResource,
  auditRecent,
  contextUse,
  devpodVersion,
  machineCreate,
  machineDelete,
  machineStatus,
  providerAdd,
  providerSetOptions,
  sshKeyGenerate,
  sshKeyList,
  workspaceDelete,
  workspaceRebuild,
  workspaceStatus,
  workspaceStop,
  workspaceUp,
} from "./commands.js"

describe("IPC commands", () => {
  beforeEach(() => {
    resetTauriMocks()
    mockInvoke.mockResolvedValue(undefined)
  })

  describe("workspace commands", () => {
    it("workspaceUp passes all parameters", async () => {
      mockInvoke.mockResolvedValue("cmd-123")
      const result = await workspaceUp({
        source: "github.com/org/repo",
        workspaceId: "my-ws",
        provider: "docker",
        ide: "vscode",
      })
      expect(mockInvoke).toHaveBeenCalledWith("workspace_up", {
        source: "github.com/org/repo",
        workspaceId: "my-ws",
        provider: "docker",
        ide: "vscode",
      })
      expect(result).toBe("cmd-123")
    })

    it("workspaceUp works with minimal parameters", async () => {
      mockInvoke.mockResolvedValue("cmd-456")
      await workspaceUp({ source: "my-repo" })
      expect(mockInvoke).toHaveBeenCalledWith("workspace_up", {
        source: "my-repo",
      })
    })

    it("workspaceStop passes workspaceId", async () => {
      await workspaceStop("ws-1")
      expect(mockInvoke).toHaveBeenCalledWith("workspace_stop", {
        workspaceId: "ws-1",
      })
    })

    it("workspaceDelete passes workspaceId", async () => {
      await workspaceDelete("ws-1")
      expect(mockInvoke).toHaveBeenCalledWith("workspace_delete", {
        workspaceId: "ws-1",
      })
    })

    it("workspaceRebuild passes workspaceId", async () => {
      await workspaceRebuild("ws-1")
      expect(mockInvoke).toHaveBeenCalledWith("workspace_rebuild", {
        workspaceId: "ws-1",
      })
    })

    it("workspaceStatus passes workspaceId", async () => {
      mockInvoke.mockResolvedValue('{"state":"Running"}')
      const result = await workspaceStatus("ws-1")
      expect(mockInvoke).toHaveBeenCalledWith("workspace_status", {
        workspaceId: "ws-1",
      })
      expect(result).toBe('{"state":"Running"}')
    })
  })

  describe("provider commands", () => {
    it("providerAdd passes name and source", async () => {
      await providerAdd("docker", "https://example.com/provider")
      expect(mockInvoke).toHaveBeenCalledWith("provider_add", {
        name: "docker",
        source: "https://example.com/provider",
      })
    })

    it("providerAdd passes undefined source when omitted", async () => {
      await providerAdd("docker")
      expect(mockInvoke).toHaveBeenCalledWith("provider_add", {
        name: "docker",
        source: undefined,
      })
    })

    it("providerSetOptions formats options as key=value strings", async () => {
      await providerSetOptions("docker", {
        DOCKER_HOST: "tcp://localhost:2375",
        TIMEOUT: 30,
        VERBOSE: true,
      })
      expect(mockInvoke).toHaveBeenCalledWith("provider_set_options", {
        name: "docker",
        options: [
          "DOCKER_HOST=tcp://localhost:2375",
          "TIMEOUT=30",
          "VERBOSE=true",
        ],
      })
    })

    it("providerSetOptions handles empty options", async () => {
      await providerSetOptions("docker", {})
      expect(mockInvoke).toHaveBeenCalledWith("provider_set_options", {
        name: "docker",
        options: [],
      })
    })
  })

  describe("machine commands", () => {
    it("machineCreate passes name, provider, and options", async () => {
      await machineCreate("my-machine", "aws", { region: "us-east-1" })
      expect(mockInvoke).toHaveBeenCalledWith("machine_create", {
        name: "my-machine",
        provider: "aws",
        options: { region: "us-east-1" },
      })
    })

    it("machineDelete passes id and force defaults to false", async () => {
      await machineDelete("m-1")
      expect(mockInvoke).toHaveBeenCalledWith("machine_delete", {
        id: "m-1",
        force: false,
      })
    })

    it("machineDelete passes force=true when specified", async () => {
      await machineDelete("m-1", true)
      expect(mockInvoke).toHaveBeenCalledWith("machine_delete", {
        id: "m-1",
        force: true,
      })
    })

    it("machineStatus passes id", async () => {
      mockInvoke.mockResolvedValue('{"state":"Running"}')
      await machineStatus("m-1")
      expect(mockInvoke).toHaveBeenCalledWith("machine_status", { id: "m-1" })
    })
  })

  describe("context commands", () => {
    it("contextUse passes name", async () => {
      await contextUse("production")
      expect(mockInvoke).toHaveBeenCalledWith("context_use", {
        name: "production",
      })
    })
  })

  describe("audit commands", () => {
    it("auditRecent passes limit", async () => {
      mockInvoke.mockResolvedValue([])
      await auditRecent(25)
      expect(mockInvoke).toHaveBeenCalledWith("audit_recent", { limit: 25 })
    })

    it("auditByResource passes all parameters", async () => {
      mockInvoke.mockResolvedValue([])
      await auditByResource("workspace", "ws-1", 50)
      expect(mockInvoke).toHaveBeenCalledWith("audit_by_resource", {
        resourceType: "workspace",
        resourceId: "ws-1",
        limit: 50,
      })
    })
  })

  describe("system commands", () => {
    it("devpodVersion returns version string", async () => {
      mockInvoke.mockResolvedValue("v0.5.0")
      const result = await devpodVersion()
      expect(result).toBe("v0.5.0")
      expect(mockInvoke).toHaveBeenCalledWith("devpod_version")
    })
  })

  describe("SSH key commands", () => {
    it("sshKeyList returns key list", async () => {
      const mockKeys = [
        {
          name: "id_ed25519",
          keyType: "ed25519",
          fingerprint: "SHA256:abc123",
          comment: "user@host",
          publicKey: "ssh-ed25519 AAAA...",
          path: "/home/user/.ssh/id_ed25519",
          hasPassphrase: true,
        },
      ]
      mockInvoke.mockResolvedValue(mockKeys)

      const result = await sshKeyList()

      expect(mockInvoke).toHaveBeenCalledWith("ssh_key_list")
      expect(result).toHaveLength(1)
      expect(result[0].name).toBe("id_ed25519")
      expect(result[0].hasPassphrase).toBe(true)
    })

    it("sshKeyGenerate passes all parameters", async () => {
      const mockKey = {
        name: "devpod-aws",
        keyType: "ed25519",
        fingerprint: "SHA256:xyz789",
        comment: "dev@example.com",
        publicKey: "ssh-ed25519 BBBB...",
        path: "/home/user/.ssh/devpod-aws",
        hasPassphrase: false,
      }
      mockInvoke.mockResolvedValue(mockKey)

      const result = await sshKeyGenerate({
        name: "devpod-aws",
        keyType: "ed25519",
        comment: "dev@example.com",
      })

      expect(mockInvoke).toHaveBeenCalledWith("ssh_key_generate", {
        name: "devpod-aws",
        keyType: "ed25519",
        comment: "dev@example.com",
      })
      expect(result.name).toBe("devpod-aws")
    })

    it("sshKeyGenerate works with minimal parameters", async () => {
      mockInvoke.mockResolvedValue({
        name: "test-key",
        keyType: "ed25519",
        fingerprint: "",
        comment: "",
        publicKey: "",
        path: "",
        hasPassphrase: false,
      })

      await sshKeyGenerate({ name: "test-key" })

      expect(mockInvoke).toHaveBeenCalledWith("ssh_key_generate", {
        name: "test-key",
      })
    })
  })
})
