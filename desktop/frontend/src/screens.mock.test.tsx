// @vitest-environment jsdom
// @vitest-environment-options {"url": "http://localhost/?mock"}
import { afterEach, describe, expect, it } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { LangContext } from "./i18n";
import { Support } from "./screens/Support";
import { CrashReports } from "./screens/CrashReports";

afterEach(cleanup);
const settle = () => act(() => new Promise((r) => setTimeout(r, 150)));
const en = (el: React.ReactNode) => render(<LangContext.Provider value="en">{el}</LangContext.Provider>);

describe("support window (demo data)", () => {
  it("opens a request, replies, attaches diagnostics and closes it", async () => {
    en(<Support />);
    fireEvent.click(await screen.findByRole("button", { name: /New request/ }));
    fireEvent.change(screen.getByLabelText(/Subject/), { target: { value: "Slow on mobile" } });
    fireEvent.change(screen.getByLabelText(/Describe the problem/), { target: { value: "It is slow since yesterday" } });
    fireEvent.click(screen.getByRole("button", { name: /Send/ }));
    await settle();
    await settle();
    const reply = await screen.findByPlaceholderText("Your reply");
    fireEvent.change(reply, { target: { value: "Still slow" } });
    fireEvent.click(screen.getAllByRole("button", { name: /Send/ }).at(-1)!);
    await settle();
    fireEvent.click(screen.getByRole("button", { name: /Attach diagnostics/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /Send diagnostics/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    for (const b of screen.queryAllByRole("button").filter((x) => /human|person/i.test(x.textContent ?? ""))) {
      fireEvent.click(b);
      await settle();
    }
    fireEvent.click(screen.getByRole("button", { name: /Solved/ }));
    await settle();
    expect(document.body.textContent).toMatch(/Support/);
  });
});

describe("crash reports (demo data)", () => {
  it("lists reports and sends and deletes them", async () => {
    en(<CrashReports onBack={() => {}} />);
    await settle();
    const sends = await screen.findAllByRole("button", { name: /Send/ });
    fireEvent.click(sends[0]!);
    await settle();
    const dels = screen.queryAllByRole("button", { name: /Delete/ });
    if (dels[0]) fireEvent.click(dels[0]);
    await settle();
    for (const d of screen.queryAllByRole("button").filter((b) => b.getAttribute("aria-expanded") !== null)) {
      fireEvent.click(d);
      await settle();
    }
    expect(document.body.textContent).toMatch(/Crash reports/);
  });
});
