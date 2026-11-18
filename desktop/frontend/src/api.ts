// Typed client for veilond's local API (via the Wails bridge at /veilond).
// Shapes mirror client/veilond/internal/core and internal/localapi.

export type TransportID = "xhttp" | "vless" | "hysteria2" | "naive";
export type ConnState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "disconnecting"
  | "failed";

export interface Health {
  reachable: boolean;
  latency_ms: number;
  jitter_ms: number;
  loss: number;
  samples: number;
  last_error?: string;
}

export interface TransportStatus {
  id: TransportID;
  configured: boolean;
  enabled: boolean;
  capabilities: { supports_udp: boolean; obfuscation: string; requires_external_process: boolean };
  health: Health;
}

export interface Status {
  state: ConnState;
  error?: string;
  node_id?: string;
  core_version?: string;
  active_tcp?: TransportID;
  active_udp?: TransportID;
  udp_available: boolean;
  transports: TransportStatus[];
  logged_in: boolean;
  has_profile: boolean;
  kill_switch_active: boolean;
  auto_switch: boolean;
  naive_external: boolean;
  bypass_ru?: "on" | "off" | "tunnel_kill_switch";
}

export type SplitKind = "domain" | "domain_suffix" | "ip" | "app";
export type SplitAction = "direct" | "proxy" | "block";
export interface SplitRule {
  kind: SplitKind;
  value: string;
  action: SplitAction;
}

export interface UpdateInfo {
  current: string;
  available: boolean;
  version?: string;
  notes?: string;
  size?: number;
  mandatory?: boolean;
  downloaded: boolean;
  path?: string;
  silent_install?: boolean;
  installing?: boolean;
}

export interface ProfileSummary {
  subscription: { plan: string; status: string; ends_on: string; traffic_limit_bytes: number; device_limit: number };
  nodes: { id: string; name: string; region: string; transports: TransportID[] }[];
  expires_at: number;
}

export interface Settings {
  clear_logs_on_exit: boolean;
  allow_lan: boolean;
  node_id: string;
  disabled_transports: Record<string, boolean>;
  manual_tcp: string;
  manual_udp: string;
  language: string;
  crash_reports: boolean;
  kill_switch: boolean;
  auto_switch: boolean;
  split_rules: SplitRule[] | null;
  bypass_ru?: boolean;
  dns?: { provider: string; custom_url?: string };
  auto_connect?: boolean;
}

export interface CrashReport {
  id: string;
  component: "veilond" | "sing-box" | "xray" | "naive" | "desktop" | "android";
  kind: "panic" | "signal" | "exception";
  day: string;
  app_version: string;
  component_version?: string;
  title: string;
  text: string;
}

export interface CrashList {
  auto_send: boolean;
  reports: CrashReport[];
}

export interface Autostart {
  enabled: boolean;
  supported: boolean;
}

export interface DnsPreset {
  id: string;
  name: string;
  host: string;
  ips: string[];
  path: string;
}

export interface Localized {
  title: string;
  text: string;
}
export interface WhatWeStore {
  stored: { category: string; ru: Localized; sr: Localized; en: Localized }[];
  not_stored: Record<"ru" | "sr" | "en", string[]>;
}

export interface BillingPrice {
  provider: string;
  amount_minor: number;
  currency: string;
}
export interface BillingOffer {
  plan_id: string;
  name: string;
  duration_days: number;
  traffic_limit_bytes: number;
  device_limit: number;
  prices: BillingPrice[];
}
export interface Checkout {
  order_id: string;
  provider: string;
  amount_minor: number;
  currency: string;
  expires_at: number;
}
export interface BillingOrder {
  id: string;
  plan_id: string;
  status: string;
  payment_status: string;
  amount_minor: number;
  currency: string;
  provider: string;
}

export interface SupportTicket {
  id: string;
  subject: string;
  lang: string;
  status: "open" | "waiting_user" | "waiting_human" | "closed";
  ai_turns: number;
  created_at: string;
  updated_at: string;
}
export interface SupportMessage {
  id: number;
  author: "user" | "ai" | "staff" | "system";
  body: string;
  kb_refs: string[];
  created_at: string;
}
export interface SupportThread {
  ticket: SupportTicket;
  messages: SupportMessage[];
  attachments: { id: string; kind: string; size_bytes: number }[];
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

const MOCK = new URLSearchParams(window.location.search).has("mock");

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (MOCK) return mock<T>(method, path, body);
  const res = await fetch(`/veilond${path}`, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : {};
  if (!res.ok) throw new ApiError(res.status, data.code ?? "error", data.message ?? res.statusText);
  return data as T;
}

let mockAutostart: Autostart = { enabled: false, supported: true };

async function appCall<T>(method: "GET" | "PUT", body?: unknown): Promise<T> {
  if (MOCK) {
    if (method === "PUT") mockAutostart = { ...mockAutostart, ...(body as Partial<Autostart>) };
    return structuredClone(mockAutostart) as T;
  }
  const res = await fetch("/app/v1/autostart", {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, data.code ?? "error", data.message ?? res.statusText);
  return data as T;
}

export const api = {
  status: () => call<Status>("GET", "/v1/status"),
  connect: (node_id?: string) => call<Status>("POST", "/v1/connect", node_id ? { node_id } : undefined),
  disconnect: () => call<Status>("POST", "/v1/disconnect"),
  location: (node_id: string) => call<Status>("POST", "/v1/location", { node_id }),
  exportAccount: () => call<unknown>("GET", "/v1/account/export"),
  deleteAccount: () => call<{ ok: boolean }>("POST", "/v1/account/delete", { confirm: "DELETE" }),
  checkUpdate: () => call<UpdateInfo>("GET", "/v1/update"),
  downloadUpdate: () => call<UpdateInfo>("POST", "/v1/update/download"),
  installUpdate: () => call<UpdateInfo>("POST", "/v1/update/install"),
  select: (group: "tcp" | "udp", transport: TransportID) =>
    call<Status>("POST", "/v1/transport/select", { group, transport }),
  setEnabled: (transport: TransportID, enabled: boolean) =>
    call<Status>("POST", "/v1/transport/enabled", { transport, enabled }),
  profile: () => call<ProfileSummary>("GET", "/v1/profile"),
  refreshProfile: () => call<ProfileSummary>("POST", "/v1/profile/refresh"),
  register: () => call<{ account_number: string; profile_error?: string }>("POST", "/v1/account/register"),
  login: (account_number: string) => call<{ ok: boolean }>("POST", "/v1/account/login", { account_number }),
  logout: () => call<{ ok: boolean }>("POST", "/v1/account/logout"),
  account: (reveal = false) =>
    call<{ logged_in: boolean; account_number?: string }>("GET", `/v1/account${reveal ? "?reveal=1" : ""}`),
  settings: () => call<Settings>("GET", "/v1/settings"),
  dnsPresets: () => call<{ default: string; presets: DnsPreset[] }>("GET", "/v1/dns/presets"),
  saveSettings: (s: Settings) => call<Settings>("PUT", "/v1/settings", s),
  whatWeStore: () => call<WhatWeStore>("GET", "/v1/privacy/what-we-store"),
  logs: (n = 200) => call<{ lines: string[] }>("GET", `/v1/logs?n=${n}`),
  wipeLogs: () => call<{ ok: boolean }>("POST", "/v1/logs/wipe"),
  offers: () => call<BillingOffer[]>("GET", "/v1/billing/offers"),
  checkout: (plan_id: string, provider: string) => call<Checkout>("POST", "/v1/billing/checkout", { plan_id, provider }),
  order: () => call<BillingOrder>("GET", "/v1/billing/order"),
  // The UI process reads the provider page address from veilond itself and
  // opens it in the system browser; the web view never handles the URL.
  supportTickets: () => call<SupportTicket[]>("GET", "/v1/support/tickets"),
  supportTicket: (id: string) => call<SupportThread>("GET", `/v1/support/tickets/${id}`),
  supportOpen: (subject: string, body: string, lang: string) => call<SupportThread>("POST", "/v1/support/tickets", { subject, body, lang }),
  supportPost: (id: string, body: string) => call<SupportThread>("POST", `/v1/support/tickets/${id}/messages`, { body }),
  supportHuman: (id: string) => call<SupportThread>("POST", `/v1/support/tickets/${id}/human`),
  supportClose: (id: string) => call<{ status: string }>("POST", `/v1/support/tickets/${id}/close`),
  diagnosticsPreview: () => call<{ sha256: string; report: unknown }>("GET", "/v1/support/diagnostics"),
  attachDiagnostics: (id: string, sha256: string) => call<unknown>("POST", `/v1/support/tickets/${id}/diagnostics`, { sha256 }),
  openPayment: async () => {
    if (MOCK) return;
    await fetch("/app/open-payment", { method: "POST" });
  },
  crashReports: () => call<CrashList>("GET", "/v1/crash-reports"),
  sendCrash: (id: string) => call<unknown>("POST", `/v1/crash-reports/${id}/send`),
  deleteCrash: (id: string) => call<unknown>("POST", `/v1/crash-reports/${id}/delete`),
  // The login item is per user and handled by the UI process.
  autostart: () => appCall<Autostart>("GET"),
  setAutostart: (enabled: boolean) => appCall<Autostart>("PUT", { enabled }),
  openSupport: async () => {
    if (MOCK) {
      window.location.hash = "#/support";
      return;
    }
    await fetch("/app/open-support", { method: "POST" });
  },
};

// ---------- mock backend (component catalog and browser development) ----------

const mockState: { status: Status; settings: Settings } = {
  status: {
    state: "disconnected",
    udp_available: false,
    logged_in: true,
    has_profile: true,
    kill_switch_active: false,
    auto_switch: true,
    naive_external: false,
    bypass_ru: "on",
    node_id: "rs-beg-1",
    transports: (["xhttp", "vless", "hysteria2", "naive"] as TransportID[]).map((id, i) => ({
      id,
      configured: true,
      enabled: true,
      capabilities: { supports_udp: id !== "naive", obfuscation: ["reality", "salamander", "https"][i]!, requires_external_process: false },
      health: { reachable: i !== 1, latency_ms: [48, 0, 95][i]!, jitter_ms: [4, 0, 11][i]!, loss: [0, 1, 0.1][i]!, samples: 10, last_error: i === 1 ? "timeout" : undefined },
    })),
  },
  settings: {
    clear_logs_on_exit: true,
    allow_lan: true,
    node_id: "rs-beg-1",
    disabled_transports: {},
    manual_tcp: "",
    manual_udp: "",
    language: "ru",
    crash_reports: false,
    kill_switch: true,
    auto_switch: true,
    bypass_ru: true,
    split_rules: [
      { kind: "domain_suffix", value: "bank.example.rs", action: "direct" },
      { kind: "app", value: "steam.exe", action: "direct" },
    ],
  },
};

let mockOrderPolls = 0;
const mockThread: SupportThread = {
  ticket: { id: "7a1c0e52-1111-4a6b-9c11-000000000003", subject: "Не работает в мобильной сети", lang: "ru", status: "waiting_user", ai_turns: 1, created_at: "2026-10-23T08:40:00Z", updated_at: "2026-10-23T08:40:05Z" },
  messages: [
    { id: 7, author: "user", body: "Через Wi-Fi всё работает, а через мобильный интернет не подключается.", kb_refs: [], created_at: "2026-10-23T08:40:00Z" },
    { id: 8, author: "ai", body: "Не подключается: выберите другую локацию или другой протокол на экране «Сеть». В сетях, где режется UDP, Hysteria 2 работать не будет; попробуйте XHTTP или NaiveProxy. Если не поможет, приложите диагностику. [kb:cannot-connect]", kb_refs: ["cannot-connect"], created_at: "2026-10-23T08:40:05Z" },
  ],
  attachments: [],
};

const mockCrashes: CrashList = {
  auto_send: false,
  reports: [
    {
      id: "5f0c2a9e7b3d4c1aa0e1f2d3c4b5a697",
      component: "sing-box",
      kind: "panic",
      day: "2026-11-01",
      app_version: "0.8.0",
      component_version: "1.13.21",
      title: "panic: runtime error: invalid memory address or nil pointer dereference",
      text: "panic: runtime error: invalid memory address or nil pointer dereference\n[signal SIGSEGV]\n\ngoroutine 87 [running]:\ngithub.com/sagernet/sing-box/transport/v2rayhttp.(*Client).DialContext(...)\n\tsing-box@v1.13.21/transport/v2rayhttp/client.go:142\ngithub.com/sagernet/sing-box/outbound.(*VLESS).DialContext(...)\n\tsing-box@v1.13.21/outbound/vless.go:118\nexit status 2",
    },
    {
      id: "a1b2c3d4e5f60718293a4b5c6d7e8f90",
      component: "veilond",
      kind: "panic",
      day: "2026-10-30",
      app_version: "0.8.0",
      title: "panic: dial tcp [IP]:443: connect: network is unreachable",
      text: "panic: dial tcp [IP]:443: connect: network is unreachable\n\ngoroutine 1 [running]:\ngithub.com/veilon/veilon-app/veilond/internal/core.(*Manager).probe(...)\n\tinternal/core/probe.go:77",
    },
  ],
};

async function mock<T>(method: string, path: string, body?: unknown): Promise<T> {
  await new Promise((r) => setTimeout(r, 150));
  const s = mockState.status;
  const b = body as Record<string, string> | undefined;
  if (path.startsWith("/v1/crash-reports/")) {
    const id = path.split("/")[3];
    mockCrashes.reports = mockCrashes.reports.filter((r) => r.id !== id);
    return {} as T;
  }
  if (path === "/v1/crash-reports") return structuredClone({ ...mockCrashes, auto_send: mockState.settings.crash_reports }) as T;
  if (path.startsWith("/v1/support/tickets/") && method === "GET") return structuredClone(mockThread) as T;
  if (path.startsWith("/v1/support/tickets/")) {
    if (path.endsWith("/human")) {
      mockThread.ticket.status = "waiting_human";
      mockThread.messages.push({ id: 9, author: "system", body: "Мы передали обращение сотруднику поддержки. Ответ придёт сюда же, обычно в течение рабочего дня.", kb_refs: [], created_at: "2026-10-23T08:41:00Z" });
    }
    if (path.endsWith("/messages")) mockThread.messages.push({ id: 10, author: "user", body: (body as { body: string }).body, kb_refs: [], created_at: "2026-10-23T08:42:00Z" });
    if (path.endsWith("/diagnostics")) mockThread.attachments.push({ id: "d1", kind: "diagnostics", size_bytes: 1800 });
    return structuredClone(mockThread) as T;
  }
  switch (`${method} ${path.split("?")[0]}`) {
    case "GET /v1/status":
      return structuredClone(s) as T;
    case "POST /v1/connect":
      Object.assign(s, { state: "connected", active_tcp: "xhttp", active_udp: "xhttp", udp_available: true, core_version: "sing-box 1.13.21", kill_switch_active: mockState.settings.kill_switch });
      return structuredClone(s) as T;
    case "POST /v1/disconnect":
      Object.assign(s, { state: "disconnected", active_tcp: undefined, active_udp: undefined, udp_available: false, kill_switch_active: false });
      return structuredClone(s) as T;
    case "POST /v1/location":
      s.node_id = b?.node_id;
      mockState.settings.node_id = b?.node_id ?? "";
      return structuredClone(s) as T;
    case "GET /v1/update":
      return { current: "0.1.0", available: true, version: "0.2.0", notes: "Kill switch, локации, раздельное туннелирование.", size: 41_200_000, downloaded: false, silent_install: true } as T;
    case "POST /v1/update/download":
      return { current: "0.1.0", available: true, version: "0.2.0", size: 41_200_000, downloaded: true, silent_install: true, path: "C:\\ProgramData\\VeilOn\\update\\VeilOn-0.2.0-x64.msi" } as T;
    case "POST /v1/update/install":
      return { current: "0.1.0", available: true, version: "0.2.0", size: 41_200_000, downloaded: true, silent_install: true, installing: true } as T;
    case "GET /v1/account/export":
      return { account: { id: "5b7f3c1e-…", status: "active", created_on: "2026-09-30", devices: [{ label: "Windows", last_used_on: "2026-10-22" }] }, not_stored: { ru: ["IP-адреса"] } } as T;
    case "POST /v1/transport/select":
      if (b?.group === "udp" && b.transport === "naive") throw new ApiError(400, "bad_request", "naive cannot carry UDP");
      if (b?.group === "tcp") s.active_tcp = b.transport as TransportID;
      else s.active_udp = b?.transport as TransportID;
      return structuredClone(s) as T;
    case "GET /v1/profile":
    case "POST /v1/profile/refresh":
      return {
        subscription: { plan: "monthly", status: "active", ends_on: "2026-11-30", traffic_limit_bytes: 0, device_limit: 5 },
        nodes: [
          { id: "rs-beg-1", name: "Beograd · Beograd 1", region: "RS", transports: ["xhttp", "vless", "hysteria2", "naive"] },
          { id: "nl-ams-1", name: "Amsterdam · Amsterdam 1", region: "NL", transports: ["xhttp", "vless", "hysteria2", "naive"] },
          { id: "de-fra-1", name: "Frankfurt · Frankfurt 1", region: "DE", transports: ["xhttp", "vless", "naive"] },
          { id: "fi-hel-1", name: "Helsinki · Helsinki 1", region: "FI", transports: ["xhttp", "vless", "hysteria2"] },
        ],
        expires_at: Date.now() / 1000 + 86400,
      } as T;
    case "GET /v1/support/tickets":
      return [mockThread.ticket, { ...mockThread.ticket, id: "x2", subject: "Двойное списание", status: "waiting_human", updated_at: "2026-10-21T10:00:00Z" }] as T;
    case "POST /v1/support/tickets":
      return structuredClone(mockThread) as T;
    case "GET /v1/support/diagnostics":
      return { sha256: "ab".repeat(32), report: { format: "veilon-diagnostics/1", client: { version: "0.6.1", os: "windows", arch: "amd64", core: "sing-box 1.13.21" }, state: { state: "failed", error: "all transports unreachable", node: "rs-beg-1" }, transports: [{ id: "xhttp", enabled: true, reachable: false, last_error: "dial tcp [IP]:443: i/o timeout" }, { id: "hysteria2", enabled: true, reachable: false, last_error: "udp blocked" }], settings: { kill_switch: true, bypass_ru: "on", split_rules: 2 }, log: ["WARN outbound/xray[xhttp]: connection to [HOST]:443 reset"] } } as T;
    case "GET /v1/billing/offers":
      return [
        { plan_id: "pro-month", name: "Pro · 1 mesec", duration_days: 30, traffic_limit_bytes: 0, device_limit: 5, prices: [{ provider: "yookassa", amount_minor: 49_900, currency: "RUB" }, { provider: "heleket", amount_minor: 599, currency: "USD" }] },
        { plan_id: "pro-year", name: "Pro · 12 meseci", duration_days: 365, traffic_limit_bytes: 0, device_limit: 5, prices: [{ provider: "yookassa", amount_minor: 449_000, currency: "RUB" }, { provider: "heleket", amount_minor: 5_400, currency: "USD" }] },
      ] as T;
    case "POST /v1/billing/checkout":
      mockOrderPolls = 0;
      return { order_id: "c1a2b3c4-0000-4000-8000-000000000001", provider: b?.provider ?? "yookassa", amount_minor: 49_900, currency: "RUB", expires_at: Date.now() / 1000 + 3600 } as T;
    case "GET /v1/billing/order":
      mockOrderPolls++;
      return { id: "c1a2b3c4-0000-4000-8000-000000000001", plan_id: "pro-month", status: mockOrderPolls > 2 ? "fulfilled" : "pending", payment_status: mockOrderPolls > 2 ? "succeeded" : "pending", amount_minor: 49_900, currency: "RUB", provider: "yookassa" } as T;
    case "GET /v1/account":
      return { logged_in: true, account_number: path.includes("reveal") ? "4821 7730 1956 0442" : "•••• •••• •••• 0442" } as T;
    case "GET /v1/dns/presets":
      return {
        default: "quad9",
        presets: [
          { id: "quad9", name: "Quad9", host: "dns.quad9.net", ips: ["9.9.9.9", "149.112.112.112"], path: "/dns-query" },
          { id: "cloudflare", name: "Cloudflare", host: "cloudflare-dns.com", ips: ["1.1.1.1", "1.0.0.1"], path: "/dns-query" },
          { id: "google", name: "Google", host: "dns.google", ips: ["8.8.8.8", "8.8.4.4"], path: "/dns-query" },
          { id: "adguard", name: "AdGuard", host: "dns.adguard-dns.com", ips: ["94.140.14.14", "94.140.15.15"], path: "/dns-query" },
          { id: "opendns", name: "OpenDNS", host: "doh.opendns.com", ips: ["208.67.222.222", "208.67.220.220"], path: "/dns-query" },
        ],
      } as T;
    case "GET /v1/settings":
      return structuredClone(mockState.settings) as T;
    case "PUT /v1/settings":
      Object.assign(mockState.settings, body);
      return structuredClone(mockState.settings) as T;
    case "GET /v1/privacy/what-we-store":
      return (await import("../../../../veil-shared/design/dist/what-we-store.json")).default.summary as T;
    case "GET /v1/logs":
      return { lines: ['{"time":"…","level":"WARN","msg":"dial tcp [IP]:443: i/o timeout"}', "WARN outbound/naive[naive]: connection to [HOST]:443 reset"] } as T;
    default:
      return { ok: true } as T;
  }
}
