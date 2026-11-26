// @vitest-environment node
import { describe, expect, it, vi } from "vitest"
import { PROVIDER_ICON_MAX_BYTES } from "../../shared/provider-icon.js"
import { ProviderIconReader, toObservedIcon } from "../provider-icons.js"

const SVG_BASE64 = Buffer.from(
  "<svg xmlns='http://www.w3.org/2000/svg'/>",
).toString("base64")

function cached(revision: string, overrides: Record<string, unknown> = {}) {
  return {
    revision,
    mimeType: "image/svg+xml",
    dataBase64: SVG_BASE64,
    ...overrides,
  }
}

function makeReader(run: (args: string[]) => Promise<unknown>) {
  const cli = { run: vi.fn((args: string[]) => run(args)) }
  return { reader: new ProviderIconReader(cli as never), cli }
}

function request(overrides: Record<string, unknown> = {}) {
  return {
    providerName: "harvester",
    context: "default",
    theme: "light" as const,
    expectedRevision: "rev-a",
    ...overrides,
  }
}

describe("ProviderIconReader", () => {
  it("reads the installed provider through the icon command and builds a data URL", async () => {
    const { reader, cli } = makeReader(async () => cached("rev-a"))

    await expect(reader.read(request())).resolves.toEqual({
      revision: "rev-a",
      dataUrl: `data:image/svg+xml;base64,${SVG_BASE64}`,
    })
    expect(cli.run).toHaveBeenCalledWith(
      [
        "provider",
        "icon",
        "harvester",
        "--theme",
        "light",
        "--context",
        "default",
        "--result-format",
        "json",
      ],
      { maxBuffer: expect.any(Number) },
    )
    const [, options] = cli.run.mock.calls[0] as unknown as [
      string[],
      { maxBuffer: number },
    ]
    // A maximum-size image encodes to ~1.4 MB of base64, above Node's default cap.
    expect(options.maxBuffer).toBeGreaterThan(PROVIDER_ICON_MAX_BYTES * 1.4)
  })

  it("caches by context, provider, revision and theme", async () => {
    const { reader, cli } = makeReader(async () => cached("rev-a"))

    await reader.read(request())
    await reader.read(request())
    expect(cli.run).toHaveBeenCalledTimes(1)

    await reader.read(request({ theme: "dark" }))
    await reader.read(request({ context: "other" }))
    await reader.read(request({ providerName: "sibling" }))
    expect(cli.run).toHaveBeenCalledTimes(4)
  })

  it("coalesces concurrent identical reads into one CLI call", async () => {
    const gate = Promise.withResolvers<unknown>()
    const { reader, cli } = makeReader(() => gate.promise)

    const first = reader.read(request())
    const second = reader.read(request())
    gate.resolve(cached("rev-a"))

    expect(await first).toEqual(await second)
    expect(cli.run).toHaveBeenCalledTimes(1)
  })

  it("returns the observed revision, never old bytes, for a stale expectedRevision", async () => {
    const { reader, cli } = makeReader(async () => cached("rev-b"))

    const stale = await reader.read(request({ expectedRevision: "rev-a" }))

    expect(stale).toEqual({ revision: "rev-b", stale: true })
    expect(stale).not.toHaveProperty("dataUrl")

    // The observed revision is cached, so the caller's retry does not hit the CLI.
    await expect(
      reader.read(request({ expectedRevision: "rev-b" })),
    ).resolves.toMatchObject({ revision: "rev-b", dataUrl: expect.any(String) })
    expect(cli.run).toHaveBeenCalledTimes(1)
  })

  it("never serves a cached revision A image for a request expecting revision B", async () => {
    const revisions = ["rev-a", "rev-b"]
    const { reader } = makeReader(async () => cached(revisions.shift() ?? ""))

    await reader.read(request({ expectedRevision: "rev-a" }))
    const reply = await reader.read(request({ expectedRevision: "rev-b" }))

    expect(reply).toEqual({ revision: "rev-b", dataUrl: expect.any(String) })
  })

  it("reports a missing variant as a normal, cached result", async () => {
    const { reader, cli } = makeReader(async () => ({
      revision: "rev-a",
      missing: true,
    }))

    await expect(reader.read(request())).resolves.toEqual({
      revision: "rev-a",
      missing: true,
    })
    await reader.read(request())
    expect(cli.run).toHaveBeenCalledTimes(1)
  })

  it("answers an installation without icons with an empty-revision missing result and does not cache it", async () => {
    const { reader, cli } = makeReader(async () => ({ missing: true }))

    const reply = await reader.read(request({ expectedRevision: "" }))
    await reader.read(request({ expectedRevision: "" }))

    expect(reply).toEqual({ revision: "", missing: true })
    expect(cli.run).toHaveBeenCalledTimes(2)
  })

  it("does not cache failures", async () => {
    let calls = 0
    const { reader } = makeReader(async () => {
      if (++calls === 1) throw new Error("disk error")
      return cached("rev-a")
    })

    await expect(reader.read(request())).rejects.toThrow("disk error")
    await expect(reader.read(request())).resolves.toMatchObject({
      dataUrl: expect.any(String),
    })
  })

  it("refetches a provider after it is invalidated, leaving other providers cached", async () => {
    const { reader, cli } = makeReader(async () => cached("rev-a"))
    await reader.read(request())
    await reader.read(request({ providerName: "sibling" }))

    reader.invalidateProvider("harvester")
    await reader.read(request())
    await reader.read(request({ providerName: "sibling" }))

    expect(cli.run).toHaveBeenCalledTimes(3)
  })

  it("clears every provider on context switch", async () => {
    const { reader, cli } = makeReader(async () => cached("rev-a"))
    await reader.read(request())
    await reader.read(request({ providerName: "sibling" }))

    reader.invalidateAll()
    await reader.read(request())
    await reader.read(request({ providerName: "sibling" }))

    expect(cli.run).toHaveBeenCalledTimes(4)
  })

  it.each(["provider", "context"] as const)(
    "returns stale without old bytes after pending-read %s invalidation",
    async (scope) => {
      const gates = [
        Promise.withResolvers<unknown>(),
        Promise.withResolvers<unknown>(),
      ]
      let next = 0
      const { reader } = makeReader(() => gates[next++].promise)
      const newData = Buffer.from("<svg id='updated'/>").toString("base64")
      const newImage = `data:image/svg+xml;base64,${newData}`

      const inFlight = reader.read(request())
      if (scope === "provider") reader.invalidateProvider("harvester")
      else reader.invalidateAll()
      const after = reader.read(request({ expectedRevision: "rev-b" }))
      gates[1].resolve(cached("rev-b", { dataBase64: newData }))
      await expect(after).resolves.toEqual({
        revision: "rev-b",
        dataUrl: newImage,
      })

      gates[0].resolve(cached("rev-a"))
      const stale = await inFlight
      expect(stale).toEqual({ revision: "rev-a", stale: true })
      expect(stale).not.toHaveProperty("dataUrl")
      await expect(
        reader.read(request({ expectedRevision: "rev-b" })),
      ).resolves.toEqual({ revision: "rev-b", dataUrl: newImage })
    },
  )

  it.each([
    ["theme", { theme: "sepia" }],
    ["provider name", { providerName: "" }],
    ["flag-like provider name", { providerName: "--help" }],
    ["context", { context: "" }],
    ["dot traversal context", { context: "../../other" }],
    ["slash context", { context: "default/other" }],
    ["backslash context", { context: String.raw`default\other` }],
    ["drive-shaped context", { context: "C:other" }],
    ["oversized context", { context: "a".repeat(256) }],
    ["provider path", { providerName: "../harvester" }],
    ["uppercase provider", { providerName: "Harvester" }],
    ["oversized provider", { providerName: "a".repeat(33) }],
    ["NUL provider", { providerName: "harvester\0other" }],
    ["NUL context", { context: "default\0other" }],
    ["NUL revision", { expectedRevision: "rev-a\0other" }],
  ])(
    "rejects an invalid %s without invoking the CLI",
    async (_label, overrides) => {
      const { reader, cli } = makeReader(async () => cached("rev-a"))

      await expect(reader.read(request(overrides))).rejects.toThrow()
      expect(cli.run).not.toHaveBeenCalled()
    },
  )
  it("accepts safe Unicode legacy contexts", async () => {
    const { reader, cli } = makeReader(async () => cached("rev-a"))
    const context = "legacy-\u6771\u4eac"

    await reader.read(request({ context }))

    expect(cli.run).toHaveBeenCalledTimes(1)
    const args = cli.run.mock.calls[0][0]
    expect(args[args.indexOf("--context") + 1]).toBe(context)
  })

  it("rejects embedded context separators without exposing existing cached bytes", async () => {
    const { reader, cli } = makeReader(async () => cached("rev-a"))
    await reader.read(request())

    // Cache key fields must not contain another identity's separator.
    await expect(
      reader.read(
        request({
          context: "default\0harvester",
          providerName: "rev-a",
          expectedRevision: "",
        }),
      ),
    ).rejects.toThrow(/valid context/)
    expect(cli.run).toHaveBeenCalledTimes(1)
    await expect(reader.read(request())).resolves.toMatchObject({
      revision: "rev-a",
      dataUrl: `data:image/svg+xml;base64,${SVG_BASE64}`,
    })
  })
})

describe("toObservedIcon bounds", () => {
  it.each(["image/png", "image/jpeg", "image/webp", "image/svg+xml"])(
    "accepts %s",
    (mimeType) => {
      expect(toObservedIcon(cached("rev", { mimeType }))).toEqual({
        revision: "rev",
        dataUrl: `data:${mimeType};base64,${SVG_BASE64}`,
      })
    },
  )

  it.each([
    "text/html",
    "image/gif",
    "image/svg+xml;charset=utf-8",
    "image/png,evil",
    "",
  ])("rejects unsupported MIME type %j", (mimeType) => {
    expect(() => toObservedIcon(cached("rev", { mimeType }))).toThrow(
      /unsupported image type/,
    )
  })

  it("accepts exactly the size limit and rejects one byte more", () => {
    const atLimit = Buffer.alloc(PROVIDER_ICON_MAX_BYTES, 1).toString("base64")
    const overLimit = Buffer.alloc(PROVIDER_ICON_MAX_BYTES + 1, 1).toString(
      "base64",
    )

    expect(
      toObservedIcon(cached("rev", { dataBase64: atLimit })),
    ).toMatchObject({ revision: "rev" })
    expect(() =>
      toObservedIcon(cached("rev", { dataBase64: overLimit })),
    ).toThrow(/too large/)
  })

  it.each([
    ["empty data", ""],
    ["non-base64 characters", "not base64!"],
    ["unpadded data", "QUJD1"],
    ["data URL injection", `${SVG_BASE64}"onerror="x`],
    ["non-string data", 42],
  ])("rejects %s", (_label, dataBase64) => {
    expect(() => toObservedIcon(cached("rev", { dataBase64 }))).toThrow(
      /invalid or too large/,
    )
  })

  it("rejects a result that is not an object", () => {
    expect(() => toObservedIcon("nope")).toThrow()
    expect(() => toObservedIcon(null)).toThrow()
  })
})
