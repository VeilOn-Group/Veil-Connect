import { ChevronRight, ShieldCheck, ShieldOff } from "lucide-react";
import { api, type ProfileSummary, type Status } from "../api";
import { ConnectButton } from "../components/ConnectButton";
import { Flag, cityOf } from "../components/Flag";
import { TransportPills } from "../components/TransportCard";
import { Banner, Button } from "../components/ui";
import { useAction } from "../hooks";
import { useT } from "../i18n";

export function Home({ status, profile, onStatus, go }: {
  status: Status;
  profile: ProfileSummary | null;
  onStatus: (s: Status) => void;
  go: (route: string) => void;
}) {
  const t = useT();
  const { busy, error, run } = useAction();
  const toggle = () =>
    run(async () => onStatus(status.state === "connected" || status.state === "reconnecting" ? await api.disconnect() : await api.connect(status.node_id)));
  const node = profile?.nodes.find((n) => n.id === status.node_id) ?? profile?.nodes[0];
  // Kill switch still blocking while not connected (crash, failed start):
  // the user must be able to see it and lift it.
  const blocked = status.kill_switch_active && status.state !== "connected" && status.state !== "connecting" && status.state !== "reconnecting";

  return (
    <div className="flex flex-col gap-6">
      <div className="pt-4">
        <ConnectButton state={busy && status.state === "disconnected" ? "connecting" : status.state} onToggle={toggle} disabled={!status.has_profile} />
      </div>

      {node && (
        <button
          type="button"
          onClick={() => go("locations")}
          className="mx-auto flex w-full max-w-xs items-center gap-3 rounded-lg border border-border bg-bg-surface px-4 py-3 text-left hover:bg-bg-elevated"
          aria-label={`${t("chooseLocation")}: ${cityOf(node.name)}`}
        >
          <Flag cc={node.region} className="text-2xl" />
          <span className="min-w-0 flex-1">
            <span className="block text-[11px] text-text-muted">{t("node")}</span>
            <span className="block truncate text-sm font-medium">{cityOf(node.name)}</span>
          </span>
          <ChevronRight size={18} className="text-text-muted" aria-hidden />
        </button>
      )}

      {blocked && (
        <div className="flex flex-col gap-2">
          <Banner tone="warning">
            <span className="inline-flex items-center gap-1.5">
              <ShieldOff size={14} aria-hidden /> {t("ksBlocked")}
            </span>
          </Banner>
          <Button variant="secondary" onClick={() => void run(async () => onStatus(await api.disconnect()))}>
            {t("unblock")}
          </Button>
        </div>
      )}
      {(error || status.error) && <Banner tone="danger">{error ?? status.error}</Banner>}
      {status.state === "connected" && !status.udp_available && <Banner tone="warning">{t("udpUnavailable")}</Banner>}

      <TransportPills items={status.transports} />

      {/* Unobtrusive "Zero-log" label linking to "What we store"; wording no
          broader than what the code guarantees. */}
      <button
        type="button"
        onClick={() => go("privacy")}
        className="mx-auto inline-flex items-center gap-1.5 rounded-full border border-border px-3 py-1 text-xs text-accent hover:bg-bg-elevated"
      >
        <ShieldCheck size={14} aria-hidden />
        {t("zeroLog")}
      </button>
    </div>
  );
}
