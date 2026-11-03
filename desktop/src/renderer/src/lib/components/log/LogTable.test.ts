import { cleanup, render } from "@testing-library/svelte"
import { afterEach, describe, expect, it, vi } from "vitest"
import * as parser from "$lib/utils/log-parser.js"
import type { TranscriptLine } from "$shared/workspace-logs.js"
import LogTable from "./LogTable.svelte"

afterEach(cleanup)

const line = (
  key: number,
  text: string,
  stream: TranscriptLine["stream"] = "stdout",
): TranscriptLine => ({
  key,
  text,
  stream,
  invocationId: "a1111111-0000-4000-8000-000000000001",
  label: "postCreateCommand",
})

describe("LogTable transcript mode", () => {
  it("never parses lines and renders JSON and HTML as literal text", () => {
    const spy = vi.spyOn(parser, "parseLogLine")
    const ui = render(LogTable, {
      lines: [],
      transcript: [
        line(0, '{"level":"error","msg":"nope","ts":"2026-01-01T00:00:00Z"}'),
        line(1, "<script>alert(1)</script><b>bold</b>", "stderr"),
        line(2, "2026-01-01 ERROR looks like a log"),
        line(3, "  indented"),
        line(4, ""),
      ],
    })
    expect(spy).not.toHaveBeenCalled()
    expect(
      ui.getByText(
        '{"level":"error","msg":"nope","ts":"2026-01-01T00:00:00Z"}',
      ),
    ).toBeTruthy()
    expect(ui.getByText("<script>alert(1)</script><b>bold</b>")).toBeTruthy()
    expect(ui.container.querySelector("script")).toBeNull()
    expect(ui.container.querySelector("b")).toBeNull()
    expect(ui.getByText("2026-01-01 ERROR looks like a log")).toBeTruthy()
    spy.mockRestore()
  })

  it("labels each row with its stream and step", () => {
    const ui = render(LogTable, {
      lines: [],
      transcript: [line(0, "x", "stderr"), line(1, "y", "combined")],
    })
    const rows = [...ui.container.querySelectorAll("[data-stream]")]
    expect(rows.map((r) => r.getAttribute("data-stream"))).toEqual([
      "stderr",
      "combined",
    ])
    expect(ui.getAllByText("postCreateCommand")).toHaveLength(2)
    expect(ui.getByText("out+err")).toBeTruthy()
  })

  it("keeps diagnostic mode parsing log lines", () => {
    const spy = vi.spyOn(parser, "parseLogLine")
    render(LogTable, { lines: ["plain text"] })
    expect(spy).toHaveBeenCalledWith("plain text")
    spy.mockRestore()
  })
})
