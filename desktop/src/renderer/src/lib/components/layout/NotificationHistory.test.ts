import { render, screen, waitFor } from "@testing-library/svelte"
import { afterEach, describe, expect, it } from "vitest"
import { notificationHistory, toasts } from "$lib/stores/toasts.js"
import NotificationHistory from "./NotificationHistory.svelte"

describe("NotificationHistory", () => {
  afterEach(() => notificationHistory.clear())

  it("updates the accessible name as the unread count changes, including zero", async () => {
    render(NotificationHistory)

    const trigger = screen.getByRole("button", { name: "Notifications" })

    const firstID = toasts.info("First notification")
    await waitFor(() =>
      expect(trigger.getAttribute("aria-label")).toBe(
        "Notifications (1 unread)",
      ),
    )

    const secondID = toasts.info("Second notification")
    await waitFor(() =>
      expect(trigger.getAttribute("aria-label")).toBe(
        "Notifications (2 unread)",
      ),
    )

    notificationHistory.remove(firstID)
    await waitFor(() =>
      expect(trigger.getAttribute("aria-label")).toBe(
        "Notifications (1 unread)",
      ),
    )

    notificationHistory.remove(secondID)
    await waitFor(() =>
      expect(trigger.getAttribute("aria-label")).toBe("Notifications"),
    )
  })
})
