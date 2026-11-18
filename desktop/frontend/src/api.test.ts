import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

type Call = { url: string; method: string; body?: unknown };
let calls: Call[] = [];
let reply: (c: Call) => { status: number; body?: string } = () => ({ status: 200, body: "{}" });

beforeEach(() => {
  calls = [];
  reply = () => ({ status: 200, body: "{}" });
  vi.stubGlobal("fetch", async (url: string, init: RequestInit = {}) => {
    const c = { url, method: init.method ?? "GET", body: init.body ? JSON.parse(init.body as string) : undefined };
    calls.push(c);
    const r = reply(c);
    return new Response(r.body ?? "", { status: r.status, statusText: r.status >= 400 ? "Bad" : "" });
  });
});
afterEach(() => vi.unstubAllGlobals());

describe("veilond client", () => {
  it("calls every local API route", async () => {
    const cases: [() => Promise<unknown>, string, string, unknown?][] = [
      [() => api.status(), "GET", "/veilond/v1/status"],
      [() => api.connect(), "POST", "/veilond/v1/connect", undefined],
      [() => api.connect("rs-1"), "POST", "/veilond/v1/connect", { node_id: "rs-1" }],
      [() => api.disconnect(), "POST", "/veilond/v1/disconnect"],
      [() => api.location("n"), "POST", "/veilond/v1/location", { node_id: "n" }],
      [() => api.exportAccount(), "GET", "/veilond/v1/account/export"],
      [() => api.deleteAccount(), "POST", "/veilond/v1/account/delete", { confirm: "DELETE" }],
      [() => api.checkUpdate(), "GET", "/veilond/v1/update"],
      [() => api.downloadUpdate(), "POST", "/veilond/v1/update/download"],
      [() => api.installUpdate(), "POST", "/veilond/v1/update/install"],
      [() => api.select("udp", "hysteria2"), "POST", "/veilond/v1/transport/select", { group: "udp", transport: "hysteria2" }],
      [() => api.setEnabled("naive", false), "POST", "/veilond/v1/transport/enabled", { transport: "naive", enabled: false }],
      [() => api.profile(), "GET", "/veilond/v1/profile"],
      [() => api.refreshProfile(), "POST", "/veilond/v1/profile/refresh"],
      [() => api.register(), "POST", "/veilond/v1/account/register"],
      [() => api.login("123"), "POST", "/veilond/v1/account/login", { account_number: "123" }],
      [() => api.logout(), "POST", "/veilond/v1/account/logout"],
      [() => api.account(), "GET", "/veilond/v1/account"],
      [() => api.account(true), "GET", "/veilond/v1/account?reveal=1"],
      [() => api.settings(), "GET", "/veilond/v1/settings"],
      [() => api.dnsPresets(), "GET", "/veilond/v1/dns/presets"],
      [() => api.saveSettings({ allow_lan: true } as never), "PUT", "/veilond/v1/settings", { allow_lan: true }],
      [() => api.whatWeStore(), "GET", "/veilond/v1/privacy/what-we-store"],
      [() => api.logs(), "GET", "/veilond/v1/logs?n=200"],
      [() => api.logs(5), "GET", "/veilond/v1/logs?n=5"],
      [() => api.wipeLogs(), "POST", "/veilond/v1/logs/wipe"],
      [() => api.offers(), "GET", "/veilond/v1/billing/offers"],
      [() => api.checkout("p", "heleket"), "POST", "/veilond/v1/billing/checkout", { plan_id: "p", provider: "heleket" }],
      [() => api.order(), "GET", "/veilond/v1/billing/order"],
      [() => api.supportTickets(), "GET", "/veilond/v1/support/tickets"],
      [() => api.supportTicket("t"), "GET", "/veilond/v1/support/tickets/t"],
      [() => api.supportOpen("s", "b", "en"), "POST", "/veilond/v1/support/tickets", { subject: "s", body: "b", lang: "en" }],
      [() => api.supportPost("t", "b"), "POST", "/veilond/v1/support/tickets/t/messages", { body: "b" }],
      [() => api.supportHuman("t"), "POST", "/veilond/v1/support/tickets/t/human"],
      [() => api.supportClose("t"), "POST", "/veilond/v1/support/tickets/t/close"],
      [() => api.diagnosticsPreview(), "GET", "/veilond/v1/support/diagnostics"],
      [() => api.attachDiagnostics("t", "h"), "POST", "/veilond/v1/support/tickets/t/diagnostics", { sha256: "h" }],
      [() => api.crashReports(), "GET", "/veilond/v1/crash-reports"],
      [() => api.sendCrash("c"), "POST", "/veilond/v1/crash-reports/c/send"],
      [() => api.deleteCrash("c"), "POST", "/veilond/v1/crash-reports/c/delete"],
      [() => api.autostart(), "GET", "/app/v1/autostart"],
      [() => api.setAutostart(true), "PUT", "/app/v1/autostart", { enabled: true }],
      [() => api.openPayment(), "POST", "/app/open-payment"],
      [() => api.openSupport(), "POST", "/app/open-support"],
    ];
    for (const [fn, method, url, body] of cases) {
      await fn();
      const c = calls.at(-1)!;
      expect([c.method, c.url]).toEqual([method, url]);
      expect(c.body).toEqual(body);
    }
  });

  it("maps errors", async () => {
    reply = () => ({ status: 412, body: JSON.stringify({ code: "precondition", message: "sign in" }) });
    await expect(api.profile()).rejects.toMatchObject({ status: 412, code: "precondition", message: "sign in" });
    reply = () => ({ status: 502 });
    const e = await api.status().catch((x: unknown) => x);
    expect(e).toBeInstanceOf(ApiError);
    expect(e).toMatchObject({ code: "error", message: "Bad" });
    // The UI process endpoint tolerates a non-JSON error body.
    reply = () => ({ status: 500, body: "oops" });
    await expect(api.autostart()).rejects.toMatchObject({ status: 500, code: "error" });
    reply = () => ({ status: 400, body: JSON.stringify({ code: "bad", message: "nope" }) });
    await expect(api.setAutostart(false)).rejects.toMatchObject({ code: "bad", message: "nope" });
  });
});
