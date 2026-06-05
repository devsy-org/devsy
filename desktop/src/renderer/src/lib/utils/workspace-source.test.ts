import { describe, it, expect } from "vitest"
import { buildWorkspaceSource } from "./workspace-source.js"
import type {
  GitSourceForm,
  LocalSourceForm,
  ImageSourceForm,
} from "./workspace-source.js"

function gitForm(overrides: Partial<GitSourceForm>): GitSourceForm {
  return {
    sourceType: "git",
    repoUrl: "",
    refType: "branch",
    refValue: "",
    subPath: "",
    devcontainerPath: "",
    prebuildRepository: "",
    ...overrides,
  }
}

function localForm(overrides: Partial<LocalSourceForm>): LocalSourceForm {
  return {
    sourceType: "local",
    localPath: "",
    devcontainerPath: "",
    prebuildRepository: "",
    ...overrides,
  }
}

function imageForm(overrides: Partial<ImageSourceForm>): ImageSourceForm {
  return {
    sourceType: "image",
    imageRef: "",
    ...overrides,
  }
}

describe("buildWorkspaceSource", () => {
  it("git: bare repo url, no suffixes", () => {
    const out = buildWorkspaceSource(gitForm({ repoUrl: "github.com/org/repo" }))
    expect(out.source).toBe("github.com/org/repo")
    expect(out.devcontainerPath).toBeUndefined()
    expect(out.prebuildRepository).toBeUndefined()
  })

  it("git: branch ref appends @branch", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "github.com/org/repo", refType: "branch", refValue: "dev" }),
    )
    expect(out.source).toBe("github.com/org/repo@dev")
  })

  it("git: commit ref appends @sha256:", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "github.com/org/repo", refType: "commit", refValue: "abc123" }),
    )
    expect(out.source).toBe("github.com/org/repo@sha256:abc123")
  })

  it("git: PR ref appends @pull/N/head", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "github.com/org/repo", refType: "pr", refValue: "42" }),
    )
    expect(out.source).toBe("github.com/org/repo@pull/42/head")
  })

  it("git: subpath appends @subpath: after ref", () => {
    const out = buildWorkspaceSource(
      gitForm({
        repoUrl: "github.com/org/repo",
        refType: "branch",
        refValue: "main",
        subPath: "packages/api",
      }),
    )
    expect(out.source).toBe("github.com/org/repo@main@subpath:packages/api")
  })

  it("git: subpath without ref appends @subpath: directly", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "github.com/org/repo", refValue: "", subPath: "packages/api" }),
    )
    expect(out.source).toBe("github.com/org/repo@subpath:packages/api")
  })

  it("git: whitespace-only optional fields become undefined", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "github.com/org/repo", devcontainerPath: "   ", prebuildRepository: "  " }),
    )
    expect(out.devcontainerPath).toBeUndefined()
    expect(out.prebuildRepository).toBeUndefined()
  })

  it("git: empty refValue omits the ref even if refType set", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "github.com/org/repo", refType: "commit", refValue: "" }),
    )
    expect(out.source).toBe("github.com/org/repo")
  })

  it("git: trims whitespace from inputs", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "  github.com/org/repo  ", refValue: "  main  " }),
    )
    expect(out.source).toBe("github.com/org/repo@main")
  })

  it("git: forwards devcontainerPath and prebuildRepository", () => {
    const out = buildWorkspaceSource(
      gitForm({
        repoUrl: "github.com/org/repo",
        devcontainerPath: ".devcontainer/devcontainer.json",
        prebuildRepository: "ghcr.io/org/prebuilds",
      }),
    )
    expect(out.devcontainerPath).toBe(".devcontainer/devcontainer.json")
    expect(out.prebuildRepository).toBe("ghcr.io/org/prebuilds")
  })

  it("local: uses localPath as source", () => {
    const out = buildWorkspaceSource(
      localForm({ localPath: "/home/me/proj" }),
    )
    expect(out.source).toBe("/home/me/proj")
  })

  it("local: forwards devcontainerPath and prebuildRepository", () => {
    const out = buildWorkspaceSource(
      localForm({
        localPath: "/home/me/proj",
        devcontainerPath: ".devcontainer/devcontainer.json",
        prebuildRepository: "ghcr.io/org/prebuilds",
      }),
    )
    expect(out.devcontainerPath).toBe(".devcontainer/devcontainer.json")
    expect(out.prebuildRepository).toBe("ghcr.io/org/prebuilds")
  })

  it("image: uses imageRef as source, ignores build options", () => {
    const out = buildWorkspaceSource(
      imageForm({ imageRef: "mcr.microsoft.com/devcontainers/python:3.12" }),
    )
    expect(out.source).toBe("mcr.microsoft.com/devcontainers/python:3.12")
    expect(out.devcontainerPath).toBeUndefined()
    expect(out.prebuildRepository).toBeUndefined()
  })

  it("image: trims whitespace from imageRef", () => {
    const out = buildWorkspaceSource(
      imageForm({ imageRef: "  mcr.microsoft.com/devcontainers/go:1  " }),
    )
    expect(out.source).toBe("mcr.microsoft.com/devcontainers/go:1")
  })
})
