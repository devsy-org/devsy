import { render, fireEvent, cleanup } from "@testing-library/svelte"
import { describe, it, expect, vi, afterEach } from "vitest"

vi.mock("$lib/stores/imageCatalog.js", async () => {
  const { writable } = await import("svelte/store")
  const { filterImages } = await vi.importActual<
    typeof import("$lib/stores/imageCatalog.js")
  >("$lib/stores/imageCatalog.js")
  return {
    imageCatalog: writable({
      images: [
        { id: "py", ref: "py:1", name: "Python", categories: ["lang"], featured: true },
        { id: "tf", ref: "tf:1", name: "Terraform", categories: ["tools"] },
      ],
      categories: [
        { id: "lang", label: "Languages" },
        { id: "tools", label: "Tools" },
      ],
      loading: false,
    }),
    loadImageCatalog: vi.fn(),
    filterImages,
  }
})

import ImagePicker from "./ImagePicker.svelte"

describe("ImagePicker", () => {
  afterEach(() => {
    cleanup()
  })

  it("renders catalog images", () => {
    const { getByText } = render(ImagePicker, { props: { value: "" } })
    expect(getByText("Python")).toBeTruthy()
    expect(getByText("Terraform")).toBeTruthy()
  })

  it("filters by search", async () => {
    const { getByPlaceholderText, queryByText } = render(ImagePicker, {
      props: { value: "" },
    })
    await fireEvent.input(getByPlaceholderText(/search images/i), {
      target: { value: "python" },
    })
    expect(queryByText("Terraform")).toBeNull()
  })

  it("selecting an image emits the ref", async () => {
    const onselect = vi.fn()
    const { getByText } = render(ImagePicker, { props: { value: "", onselect } })
    await fireEvent.click(getByText("Python"))
    expect(onselect).toHaveBeenCalledWith("py:1")
  })
})
