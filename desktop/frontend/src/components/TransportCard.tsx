import { Shield, Zap, Globe } from "lucide-react";
import type { TransportStatus } from "../api";
import { useT } from "../i18n";
import { StatusDot, type Tone } from "./ui";

export const transportShort: Record<string, string> = { xhttp: "XHTTP", vless: "Vision", hysteria2: "Hysteria 2", naive: "Naive" };
export const transportName: Record<string, string> = { xhttp: "VLESS · XHTTP · REALITY", vless: "VLESS · Vision · REALITY", hysteria2: "Hysteria 2", naive: "NaiveProxy" };
const icons = { xhttp: Shield, vless: Shield, hysteria2: Zap, naive: Globe };

export function healthTone(ts: TransportStatus): Tone {
  if (!ts.configured || !ts.enabled) return "muted";
  if (ts.health.samples === 0) return "muted";
  if (!ts.health.reachable) return "danger";
  if (ts.health.loss > 0.2 || ts.health.latency_ms > 400) return "warning";
  return "success";
}

export function healthLabel(ts: TransportStatus, t: ReturnType<typeof useT>): string {
  if (!ts.configured) return t("notConfigured");
  if (ts.health.samples === 0) return t("unknown");
  if (!ts.health.reachable) return t("unreachable");
  return `${ts.health.latency_ms} ms`;
}

// Compact indicator row for the home screen (spec: health in status colors).
export function TransportPills({ items }: { items: TransportStatus[] }) {
  const t = useT();
  return (
    <ul className="grid grid-cols-3 gap-2" aria-label={t("transports")}>
      {items.map((ts) => {
        const Icon = icons[ts.id];
        return (
          <li key={ts.id} className="rounded-md border border-border bg-bg-surface px-2.5 py-2">
            <div className="flex items-center gap-1.5 text-xs font-medium text-text-primary">
              <Icon size={14} className="text-accent" aria-hidden />
              {transportShort[ts.id]}
            </div>
            <div className="mt-1">
              <StatusDot tone={healthTone(ts)} label={healthLabel(ts, t)} />
            </div>
          </li>
        );
      })}
    </ul>
  );
}
