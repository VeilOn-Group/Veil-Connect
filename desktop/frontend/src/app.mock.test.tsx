// @vitest-environment jsdom
// @vitest-environment-options {"url": "http://localhost/?mock"}
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { App } from "./App";

beforeAll(() => {
  vi.spyOn(window, "confirm").mockReturnValue(true);
  Object.defineProperty(navigator, "clipboard", { value: { writeText: vi.fn().mockResolvedValue(undefined) }, configurable: true });
  window.HTMLElement.prototype.scrollIntoView = vi.fn();
  globalThis.URL.createObjectURL = vi.fn(() => "blob:x");
  globalThis.URL.revokeObjectURL = vi.fn();
});
afterEach(() => {
  cleanup();
  window.location.hash = "";
});

const settle = () => act(() => new Promise((r) => setTimeout(r, 150)));

function fillInputs(root: HTMLElement) {
  for (const el of Array.from(root.querySelectorAll("input, textarea"))) {
    const input = el as HTMLInputElement;
    if (input.value || input.type === "checkbox" || input.dataset.filled) continue;
    input.dataset.filled = "1";
    fireEvent.change(input, { target: { value: input.type === "number" ? "5" : "example.org" } });
  }
}

async function clickAll(root: HTMLElement, skip: (b: HTMLElement) => boolean) {
  for (let i = 0; i < 80; i++) {
    fillInputs(root);
    const b = within(root)
      .queryAllByRole("button")
      .concat(within(root).queryAllByRole("switch"), within(root).queryAllByRole("checkbox"))
      .find((x) => !(x as HTMLButtonElement).disabled && !skip(x) && !x.dataset.pressed);
    if (!b) break;
    b.dataset.pressed = "1";
    fireEvent.click(b);
    await settle();
  }
  for (const s of Array.from(root.querySelectorAll("select"))) {
    const opts = Array.from(s.querySelectorAll("option"));
    const last = opts.at(-1);
    if (last) fireEvent.change(s, { target: { value: last.value } });
    await settle();
  }
}

describe("desktop UI (demo data)", () => {
  it("walks every tab and presses every control", async () => {
    render(<App />);
    const n = within(await screen.findByRole("navigation")).getAllByRole("button").length;
    expect(n).toBeGreaterThanOrEqual(6);
    cleanup();
    // A fresh window per tab: some actions (sign out, sub-screens) leave
    // the tab bar, so each tab starts from the same state.
    for (let i = 0; i < n; i++) {
      window.location.hash = "";
      render(<App />);
      const nav = await screen.findByRole("navigation").catch(() => {
        throw new Error(`tab ${i}: no tab bar; page: ${document.body.textContent?.slice(0, 300)}`);
      });
      fireEvent.click(within(nav).getAllByRole("button")[i]!);
      await settle();
      // The header button opens the separate support window (tested below).
      await clickAll(document.body, (b) => !!b.closest("nav") || /Sign out|Delete|Выйти|Удалить|Odjav|Obriši|^\s*(Support|Поддержка|Podrška)\s*$/.test(b.textContent ?? ""));
      fireEvent.keyDown(window, { key: "Escape" });
      await settle();
      cleanup();
    }
    // Finally the destructive actions on the account tab: deletion and
    // sign-out end on the sign-in screen.
    window.location.hash = "";
    render(<App />);
    fireEvent.click(within(await screen.findByRole("navigation")).getAllByRole("button")[2]!);
    await settle();
    await clickAll(document.body, (b) => !!b.closest("nav") || /^\s*(Support|Поддержка|Podrška)\s*$/.test(b.textContent ?? ""));
    await clickAll(document.body, () => false);
  }, 300_000);

  it("renders the support window and the component catalog", async () => {
    for (const hash of ["#/support", "#/catalog"]) {
      window.location.hash = hash;
      render(<App />);
      await settle();
      await settle();
      expect(document.body.textContent?.length).toBeGreaterThan(0);
      await clickAll(document.body, () => false);
      cleanup();
    }
  }, 300_000);
});
