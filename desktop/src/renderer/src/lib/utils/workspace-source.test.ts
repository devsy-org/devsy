import { describe, expect, it } from "vitest"
import type {
  GitSourceForm,
  ImageSourceForm,
  LocalSourceForm,
} from "./workspace-source.js"
import {
  buildDevcontainerArg,
  buildWorkspaceSource,
} from "./workspace-source.js"

function gitForm(overrides: Partial<GitSourceForm>): GitSourceForm {
  return {
    sourceType: "git",
    repoUrl: "",
    refType: "branch",
    refValue: "",
    subPath: "",
    devcontainer: { mode: "auto", value: "" },
    prebuildRepository: "",
    ...overrides,
  }
}

function localForm(overrides: Partial<LocalSourceForm>): LocalSourceForm {
  return {
    sourceType: "local",
    localPath: "",
    devcontainer: { mode: "auto", value: "" },
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
    expect(out.devcontainer).toBeUndefined()
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

  it("git: PR ref appends @pull/N/head for GitHub", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "github.com/org/repo", refType: "pr", refValue: "42" }),
    )
    expect(out.source).toBe("github.com/org/repo@pull/42/head")
  })

  it("git: MR ref appends @merge-requests/N/head for GitLab", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "gitlab.com/org/repo", refType: "pr", refValue: "7125" }),
    )
    expect(out.source).toBe("gitlab.com/org/repo@merge-requests/7125/head")
  })

  it("git: MR ref detects self-hosted GitLab by hostname", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "git@gitlab.example.com:org/repo.git", refType: "pr", refValue: "7" }),
    )
    expect(out.source).toBe("git@gitlab.example.com:org/repo.git@merge-requests/7/head")
  })

  it("git: PR ref uses pull/N/head when only the owner/path says gitlab", () => {
    const out = buildWorkspaceSource(
      gitForm({ repoUrl: "git@github.com:gitlab-org/repo.git", refType: "pr", refValue: "7" }),
    )
    expect(out.source).toBe("git@github.com:gitlab-org/repo.git@pull/7/head")
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
      gitForm({
        repoUrl: "github.com/org/repo",
        devcontainer: { mode: "path", value: "   " },
        prebuildRepository: "  ",
      }),
    )
    expect(out.devcontainer).toBeUndefined()
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

  it("git: forwards devcontainer path and prebuildRepository", () => {
    const out = buildWorkspaceSource(
      gitForm({
        repoUrl: "github.com/org/repo",
        devcontainer: { mode: "path", value: ".devcontainer/devcontainer.json" },
        prebuildRepository: "ghcr.io/org/prebuilds",
      }),
    )
    expect(out.devcontainer).toBe(".devcontainer/devcontainer.json")
    expect(out.prebuildRepository).toBe("ghcr.io/org/prebuilds")
  })

  it("local: uses localPath as source", () => {
    const out = buildWorkspaceSource(
      localForm({ localPath: "/home/me/proj" }),
    )
    expect(out.source).toBe("/home/me/proj")
  })

  it("local: never appends a @subpath: suffix", () => {
    const out = buildWorkspaceSource(
      localForm({ localPath: "/home/me/proj/packages/api" }),
    )
    expect(out.source).toBe("/home/me/proj/packages/api")
    expect(out.source).not.toContain("@subpath:")
  })

  it("local: forwards devcontainer path and prebuildRepository", () => {
    const out = buildWorkspaceSource(
      localForm({
        localPath: "/home/me/proj",
        devcontainer: { mode: "path", value: ".devcontainer/devcontainer.json" },
        prebuildRepository: "ghcr.io/org/prebuilds",
      }),
    )
    expect(out.devcontainer).toBe(".devcontainer/devcontainer.json")
    expect(out.prebuildRepository).toBe("ghcr.io/org/prebuilds")
  })

  it("image: uses imageRef as source, ignores build options", () => {
    const out = buildWorkspaceSource(
      imageForm({ imageRef: "mcr.microsoft.com/devcontainers/python:3.12" }),
    )
    expect(out.source).toBe("mcr.microsoft.com/devcontainers/python:3.12")
    expect(out.devcontainer).toBeUndefined()
    expect(out.prebuildRepository).toBeUndefined()
  })

  it("image: trims whitespace from imageRef", () => {
    const out = buildWorkspaceSource(
      imageForm({ imageRef: "  mcr.microsoft.com/devcontainers/go:1  " }),
    )
    expect(out.source).toBe("mcr.microsoft.com/devcontainers/go:1")
  })
})

describe("buildDevcontainerArg", () => {
  it("auto omits the flag", () => {
    expect(buildDevcontainerArg({ mode: "auto", value: "" })).toBeUndefined()
    expect(buildDevcontainerArg({ mode: "auto", value: "ignored" })).toBeUndefined()
  })

  it("none maps to the none token", () => {
    expect(buildDevcontainerArg({ mode: "none", value: "" })).toBe("none")
  })

  it("path passes the raw path", () => {
    expect(
      buildDevcontainerArg({ mode: "path", value: ".devcontainer/devcontainer.json" }),
    ).toBe(".devcontainer/devcontainer.json")
  })

  it("image prefixes with image:", () => {
    expect(buildDevcontainerArg({ mode: "image", value: "python:3" })).toBe(
      "image:python:3",
    )
  })

  it("id prefixes with id:", () => {
    expect(buildDevcontainerArg({ mode: "id", value: "backend" })).toBe("id:backend")
  })

  it("trims the value", () => {
    expect(buildDevcontainerArg({ mode: "image", value: "  python  " })).toBe(
      "image:python",
    )
  })

  it("value-requiring modes omit the flag when the value is blank", () => {
    expect(buildDevcontainerArg({ mode: "path", value: "  " })).toBeUndefined()
    expect(buildDevcontainerArg({ mode: "image", value: "" })).toBeUndefined()
    expect(buildDevcontainerArg({ mode: "id", value: "   " })).toBeUndefined()
  })
})
