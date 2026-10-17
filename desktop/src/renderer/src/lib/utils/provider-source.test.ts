import { describe, expect, it } from "vitest"
import { providerSourceReference } from "./provider-source.js"

describe("providerSourceReference", () => {
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
