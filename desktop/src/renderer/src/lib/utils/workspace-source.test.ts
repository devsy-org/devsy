import { describe, it, expect } from "vitest"
import { buildWorkspaceSource } from "./workspace-source.js"
import type { WorkspaceSourceForm } from "./workspace-source.js"

function form(overrides: Partial<WorkspaceSourceForm>): WorkspaceSourceForm {
  return {
    sourceType: "git",
    repoUrl: "",
    localPath: "",
    imageRef: "",
    refType: "branch",
    refValue: "",
    subPath: "",
    devcontainerPath: "",
    prebuildRepository: "",
    ...overrides,
  }
}

describe("buildWorkspaceSource", () => {
  it("git: bare repo url, no suffixes", () => {
    const out = buildWorkspaceSource(form({ repoUrl: "github.com/org/repo" }))
    expect(out.source).toBe("github.com/org/repo")
    expect(out.devcontainerPath).toBeUndefined()
    expect(out.prebuildRepository).toBeUndefined()
  })

  it("git: branch ref appends @branch", () => {
    const out = buildWorkspaceSource(
      form({ repoUrl: "github.com/org/repo", refType: "branch", refValue: "dev" }),
    )
    expect(out.source).toBe("github.com/org/repo@dev")
  })

  it("git: commit ref appends @sha256:", () => {
    const out = buildWorkspaceSource(
      form({ repoUrl: "github.com/org/repo", refType: "commit", refValue: "abc123" }),
    )
    expect(out.source).toBe("github.com/org/repo@sha256:abc123")
  })

  it("git: PR ref appends @pull/N/head", () => {
    const out = buildWorkspaceSource(
      form({ repoUrl: "github.com/org/repo", refType: "pr", refValue: "42" }),
    )
    expect(out.source).toBe("github.com/org/repo@pull/42/head")
  })

  it("git: subpath appends @subpath: after ref", () => {
    const out = buildWorkspaceSource(
      form({
        repoUrl: "github.com/org/repo",
        refType: "branch",
        refValue: "main",
        subPath: "packages/api",
      }),
    )
    expect(out.source).toBe("github.com/org/repo@main@subpath:packages/api")
  })

  it("git: empty refValue omits the ref even if refType set", () => {
    const out = buildWorkspaceSource(
      form({ repoUrl: "github.com/org/repo", refType: "commit", refValue: "" }),
    )
    expect(out.source).toBe("github.com/org/repo")
  })

  it("git: trims whitespace from inputs", () => {
    const out = buildWorkspaceSource(
      form({ repoUrl: "  github.com/org/repo  ", refValue: "  main  " }),
    )
    expect(out.source).toBe("github.com/org/repo@main")
  })

  it("git: forwards devcontainerPath and prebuildRepository", () => {
    const out = buildWorkspaceSource(
      form({
        repoUrl: "github.com/org/repo",
        devcontainerPath: ".devcontainer/devcontainer.json",
        prebuildRepository: "ghcr.io/org/prebuilds",
      }),
    )
    expect(out.devcontainerPath).toBe(".devcontainer/devcontainer.json")
    expect(out.prebuildRepository).toBe("ghcr.io/org/prebuilds")
  })

  it("local: uses localPath as source, supports subpath", () => {
    const out = buildWorkspaceSource(
      form({ sourceType: "local", localPath: "/home/me/proj", subPath: "sub" }),
    )
    expect(out.source).toBe("/home/me/proj@subpath:sub")
  })

  it("local: forwards devcontainerPath and prebuildRepository", () => {
    const out = buildWorkspaceSource(
      form({
        sourceType: "local",
        localPath: "/home/me/proj",
        devcontainerPath: ".devcontainer/devcontainer.json",
        prebuildRepository: "ghcr.io/org/prebuilds",
      }),
    )
    expect(out.devcontainerPath).toBe(".devcontainer/devcontainer.json")
    expect(out.prebuildRepository).toBe("ghcr.io/org/prebuilds")
  })

  it("image: uses imageRef as source, ignores git/build options", () => {
    const out = buildWorkspaceSource(
      form({
        sourceType: "image",
        imageRef: "mcr.microsoft.com/devcontainers/python:3.12",
        refValue: "main",
        subPath: "sub",
        devcontainerPath: ".devcontainer/devcontainer.json",
        prebuildRepository: "ghcr.io/org/prebuilds",
      }),
    )
    expect(out.source).toBe("mcr.microsoft.com/devcontainers/python:3.12")
    expect(out.devcontainerPath).toBeUndefined()
    expect(out.prebuildRepository).toBeUndefined()
  })
})
