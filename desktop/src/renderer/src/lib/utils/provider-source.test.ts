import { describe, expect, it } from "vitest"
import { providerSourceReference } from "./provider-source.js"

describe("providerSourceReference", () => {
  it.each([
    [
      {
        raw: "  https://user:canary-secret@example.test/provider.yaml?token=canary-token#canary-fragment  ",
      },
      "https://example.test/provider.yaml (parameters hidden)",
    ],
    [
      {
        raw: "\thttps://user:canary-secret@example.test/provider.yaml?token=canary-token\n",
        url: "https://different.example/provider.yaml",
      },
      "https://example.test/provider.yaml (parameters hidden)",
    ],
    [
      {
        raw: "https://user:secret@example.test/provider.yaml?token=private#secret",
      },
      "https://example.test/provider.yaml (parameters hidden)",
    ],
    [
      { url: "http://user:secret@example.test:8080/provider.yaml" },
      "http://example.test:8080/provider.yaml",
    ],
    [
      { raw: "https://example.test/provider.yaml?signature=private" },
      "https://example.test/provider.yaml (parameters hidden)",
    ],
    [{ url: "https://user:secret@[invalid" }, "URL source"],
  ])(
    "hides URL credentials without changing the stored source %j",
    (source, expected) => {
      const original = { ...source }
      expect(providerSourceReference(source)).toBe(expected)
      expect(source).toEqual(original)
    },
  )

  it("keeps an explicitly selected release visible", () => {
    expect(
      providerSourceReference({
        raw: "github.com/example/provider@v1.2.3",
        github: "example/provider",
      }),
    ).toBe("github.com/example/provider@v1.2.3")
  })

  it.each([
    [{ github: "example/provider" }, "example/provider"],
    [
      { url: "https://example.test/provider.yaml" },
      "https://example.test/provider.yaml",
    ],
    [
      { file: "/Users/developer/my provider.yaml" },
      "/Users/developer/my provider.yaml",
    ],
    [undefined, undefined],
    [{}, undefined],
  ])("describes the stored source %j", (source, expected) => {
    expect(providerSourceReference(source)).toBe(expected)
  })
})
