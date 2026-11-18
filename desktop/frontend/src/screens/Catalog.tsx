import tokens from "../../../../../veil-shared/design/dist/tokens.json";
import type { TransportStatus } from "../api";
import { ConnectButton } from "../components/ConnectButton";
import { TransportPills } from "../components/TransportCard";
import { Banner, Button, Card, SectionTitle, StatusDot, Toggle } from "../components/ui";
import { useT } from "../i18n";

// Component catalog (spec: "Storybook or equivalent" for token checks).
// Open with #/catalog?mock: renders every component state and recomputes
// WCAG contrast for the declared token pairs in the browser.

function lum(hex: string) {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0]! + 0.7152 * c[1]! + 0.0722 * c[2]!;
}
function ratio(a: string, b: string) {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
  return (x! + 0.05) / (y! + 0.05);
}

const sample: TransportStatus[] = [
  { id: "xhttp", configured: true, enabled: true, capabilities: { supports_udp: true, obfuscation: "reality", requires_external_process: true }, health: { reachable: true, latency_ms: 52, jitter_ms: 4, loss: 0, samples: 10 } },
  { id: "vless", configured: true, enabled: true, capabilities: { supports_udp: true, obfuscation: "reality", requires_external_process: false }, health: { reachable: true, latency_ms: 48, jitter_ms: 3, loss: 0, samples: 10 } },
  { id: "hysteria2", configured: true, enabled: true, capabilities: { supports_udp: true, obfuscation: "salamander", requires_external_process: false }, health: { reachable: false, latency_ms: 0, jitter_ms: 0, loss: 1, samples: 10 } },
  { id: "naive", configured: true, enabled: true, capabilities: { supports_udp: false, obfuscation: "https", requires_external_process: false }, health: { reachable: true, latency_ms: 520, jitter_ms: 40, loss: 0.3, samples: 10 } },
];

export function Catalog() {
  const t = useT();
  const colors = tokens.color as Record<string, string>;
  const pairs = [
    ...tokens.contrast.text.map((p) => ({ fg: p[0]!, bg: p[1]!, min: 4.5 })),
    ...tokens.contrast.ui.map((p) => ({ fg: p[0]!, bg: p[1]!, min: 3 })),
  ];
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
      <h1 className="text-xl font-semibold">{t("catalog")}</h1>

      <Card>
        <SectionTitle>Tokens</SectionTitle>
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          {Object.entries(colors).map(([name, hex]) => (
            <div key={name} className="rounded-md border border-border p-2 text-xs">
              <div className="mb-2 h-10 rounded" style={{ background: hex }} />
              <p className="font-medium">{name}</p>
              <p className="font-mono text-text-muted">{hex}</p>
            </div>
          ))}
        </div>
      </Card>

      <Card>
        <SectionTitle hint="WCAG AA: 4.5:1 text, 3:1 UI components">Contrast</SectionTitle>
        <table className="w-full text-xs">
          <tbody>
            {pairs.map(({ fg, bg, min }) => {
              const r = ratio(colors[fg]!, colors[bg]!);
              return (
                <tr key={fg + bg} className="border-t border-border">
                  <td className="py-1.5">
                    <span className="rounded px-2 py-0.5" style={{ color: colors[fg], background: colors[bg] }}>
                      Aa {fg}
                    </span>
                  </td>
                  <td className="text-text-muted">on {bg}</td>
                  <td className="text-right font-mono">{r.toFixed(2)}</td>
                  <td className="pl-2 text-right">{r >= min ? <StatusDot tone="success" label="pass" /> : <StatusDot tone="danger" label="FAIL" />}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </Card>

      <Card className="flex flex-wrap items-center gap-3">
        <Button>Primary</Button>
        <Button variant="secondary">Secondary</Button>
        <Button variant="ghost">Ghost</Button>
        <Button variant="danger">Danger</Button>
        <Button loading>Loading</Button>
        <Button disabled>Disabled</Button>
      </Card>

      <Card className="grid grid-cols-2 gap-6">
        <ConnectButton state="disconnected" onToggle={() => {}} />
        <ConnectButton state="connected" onToggle={() => {}} />
      </Card>

      <Card className="flex flex-col gap-3">
        <TransportPills items={sample} />
        <Toggle checked onChange={() => {}} label="Toggle on" />
        <Toggle checked={false} onChange={() => {}} label="Toggle off" hint="Hint text" />
        <Toggle checked={false} onChange={() => {}} disabled label="Disabled" badge={t("soon")} />
        <Banner tone="info">Info banner</Banner>
        <Banner tone="warning">{t("udpUnavailable")}</Banner>
        <Banner tone="danger">{t("serviceDown")}</Banner>
      </Card>
    </div>
  );
}
