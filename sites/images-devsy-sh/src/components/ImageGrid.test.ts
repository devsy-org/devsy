import { cleanup, render, screen } from "@testing-library/svelte"
import { afterEach, describe, expect, it } from "vitest"
import type { CatalogImage } from "../lib/catalog-schema"
import ImageGrid from "./ImageGrid.svelte"

const IMAGES: CatalogImage[] = [
  {
    id: "py",
    ref: "mcr/python:3.12",
    name: "Python 3.12",
    categories: ["languages"],
  },
]

afterEach(() => cleanup())

describe("ImageGrid", () => {
  it("renders a card per image", () => {
    render(ImageGrid, { props: { images: IMAGES } })
    expect(screen.getByText("Python 3.12")).toBeInTheDocument()
  })

  it("shows an empty state when there are no images", () => {
    render(ImageGrid, { props: { images: [] } })
    expect(screen.getByText(/no images match/i)).toBeInTheDocument()
  })
})
