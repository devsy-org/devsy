import { existsSync, readFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { describe, expect, it } from "vitest"

const DESKTOP_ROOT = resolve(__dirname, "../../..")
const RESOURCES_DIR = join(DESKTOP_ROOT, "resources")
const ICONS_DIR = join(RESOURCES_DIR, "icons")

const PNG_MAGIC = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
const ICNS_MAGIC = Buffer.from("icns", "ascii")
const ICO_MAGIC = Buffer.from([0x00, 0x00, 0x01, 0x00])

function getPngDimensions(buf: Buffer): { width: number; height: number } {
  expect(buf.subarray(0, 8)).toEqual(PNG_MAGIC)
  const width = buf.readUInt32BE(16)
  const height = buf.readUInt32BE(20)
  return { width, height }
}

describe("Devsy Application Icon Assets", () => {
  describe("Canonical Vector Source", () => {
    it("provides valid icon SVG canvas", () => {
      const svgPath = join(RESOURCES_DIR, "icon.svg")
      expect(existsSync(svgPath)).toBe(true)
      const content = readFileSync(svgPath, "utf8")
      expect(content).toContain("<svg")
      expect(content).toContain("viewBox=")
    })

    it("keeps box-icon-app.svg in sync with icon.svg", () => {
      const boxPath = join(RESOURCES_DIR, "box-icon-app.svg")
      const iconPath = join(RESOURCES_DIR, "icon.svg")
      expect(existsSync(boxPath)).toBe(true)
      expect(existsSync(iconPath)).toBe(true)
      expect(readFileSync(boxPath, "utf8")).toBe(readFileSync(iconPath, "utf8"))
    })
  })

  describe("macOS Icon Assets", () => {
    it("provides valid multi-resolution ICNS file", () => {
      const icnsPath = join(RESOURCES_DIR, "icon.icns")
      expect(existsSync(icnsPath)).toBe(true)
      const buf = readFileSync(icnsPath)
      expect(buf.subarray(0, 4)).toEqual(ICNS_MAGIC)
      const totalLen = buf.readUInt32BE(4)
      expect(totalLen).toBe(buf.length)

      const types: string[] = []
      let offset = 8
      while (offset < buf.length) {
        const type = buf.toString("ascii", offset, offset + 4)
        const len = buf.readUInt32BE(offset + 4)
        expect(len).toBeGreaterThanOrEqual(8)
        expect(offset + len).toBeLessThanOrEqual(buf.length)
        types.push(type)
        offset += len
      }

      expect(types).toContain("icp4") // 16x16
      expect(types).toContain("icp5") // 32x32
      expect(types).toContain("icp6") // 64x64
      expect(types).toContain("ic07") // 128x128
      expect(types).toContain("ic08") // 256x256
      expect(types).toContain("ic09") // 512x512
      expect(types).toContain("ic10") // 1024x1024
    })
  })

  describe("Windows Icon Assets", () => {
    it("provides valid multi-resolution ICO file with standard sizes", () => {
      const icoPath = join(RESOURCES_DIR, "icon.ico")
      expect(existsSync(icoPath)).toBe(true)
      const buf = readFileSync(icoPath)
      expect(buf.subarray(0, 4)).toEqual(ICO_MAGIC)

      const count = buf.readUInt16LE(4)
      expect(count).toBe(7)

      const expectedSizes = [16, 24, 32, 48, 64, 128, 256]
      const actualSizes: number[] = []

      let offset = 6
      for (let i = 0; i < count; i++) {
        const w = buf.readUInt8(offset)
        const size = w === 0 ? 256 : w
        actualSizes.push(size)
        const imgSize = buf.readUInt32LE(offset + 8)
        const imgOffset = buf.readUInt32LE(offset + 12)
        const imgBuf = buf.subarray(imgOffset, imgOffset + imgSize)
        expect(imgBuf.subarray(0, 8)).toEqual(PNG_MAGIC)
        offset += 16
      }

      expect(actualSizes).toEqual(expectedSizes)
    })
  })

  describe("Linux and Master PNG Assets", () => {
    it("provides valid 1024x1024 master icon.png", () => {
      const pngPath = join(RESOURCES_DIR, "icon.png")
      expect(existsSync(pngPath)).toBe(true)
      const buf = readFileSync(pngPath)
      const { width, height } = getPngDimensions(buf)
      expect(width).toBe(1024)
      expect(height).toBe(1024)
    })

    it("provides valid 32x32 Linux icon", () => {
      const pngPath = join(ICONS_DIR, "32x32.png")
      expect(existsSync(pngPath)).toBe(true)
      const buf = readFileSync(pngPath)
      const { width, height } = getPngDimensions(buf)
      expect(width).toBe(32)
      expect(height).toBe(32)
    })

    it("provides valid 128x128 Linux icon", () => {
      const pngPath = join(ICONS_DIR, "128x128.png")
      expect(existsSync(pngPath)).toBe(true)
      const buf = readFileSync(pngPath)
      const { width, height } = getPngDimensions(buf)
      expect(width).toBe(128)
      expect(height).toBe(128)
    })
  })
})
