import { cleanup, fireEvent, render, screen } from "@testing-library/svelte"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { CatalogImage } from "../lib/catalog-schema"
import ImageCard from "./ImageCard.svelte"

const IMAGE: CatalogImage = {
  id: "py",
  ref: "mcr.microsoft.com/devcontainers/python:3.12",
  name: "Python 3.12",
  description: "Python image",
  categories: ["languages"],
}

describe("ImageCard", () => {
  beforeEach(() => {
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    })
  })

  afterEach(() => {
    cleanup()
  })

  it("renders name, description, and ref", () => {
    render(ImageCard, { props: { image: IMAGE } })
    expect(screen.getByText("Python 3.12")).toBeInTheDocument()
    expect(screen.getByText("Python image")).toBeInTheDocument()
    expect(screen.getByText(IMAGE.ref)).toBeInTheDocument()
  })

  it("copies the docker pull command when the copy button is clicked", async () => {
    render(ImageCard, { props: { image: IMAGE } })
    await fireEvent.click(screen.getByRole("button", { name: /copy/i }))
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(
      `docker pull ${IMAGE.ref}`,
    )
  })

  it("does not throw when clipboard write fails", async () => {
    navigator.clipboard.writeText = vi
      .fn()
      .mockRejectedValue(new Error("denied"))
    render(ImageCard, { props: { image: IMAGE } })
    await fireEvent.click(screen.getByRole("button", { name: /copy/i }))
    expect(screen.getByRole("button", { name: /copy/i })).toBeInTheDocument()
  })
})
