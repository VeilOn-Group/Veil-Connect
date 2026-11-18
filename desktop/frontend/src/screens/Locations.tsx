import { Check } from "lucide-react";
import { api, type ProfileSummary, type Status } from "../api";
import { Flag, cityOf, detailOf } from "../components/Flag";
import { Banner, SectionTitle } from "../components/ui";
import { useAction } from "../hooks";
import { useT } from "../i18n";

const short: Record<string, string> = { xhttp: "XHTTP", vless: "Vision", hysteria2: "Hysteria 2", naive: "Naive" };

// Location picker: the nodes of the signed profile, i.e. the active nodes the
// panel bound to the user's plan.
export function Locations({ status, profile, onStatus, onDone }: { status: Status; profile: ProfileSummary | null; onStatus: (s: Status) => void; onDone: () => void }) {
  const t = useT();
  const { busy, error, run } = useAction();
  const pick = (id: string) =>
    run(async () => {
      onStatus(await api.location(id));
      onDone();
    });
  return (
    <div className="flex flex-col gap-4">
      <SectionTitle hint={t("locationsHint")}>{t("locations")}</SectionTitle>
      {error && <Banner tone="danger">{error}</Banner>}
      <ul className="flex flex-col gap-2" aria-busy={busy || undefined}>
        {(profile?.nodes ?? []).map((n) => {
          const current = n.id === status.node_id;
          return (
            <li key={n.id}>
              <button
                type="button"
                disabled={busy}
                onClick={() => (current ? onDone() : void pick(n.id))}
                aria-current={current || undefined}
                className={`flex w-full items-center gap-3 rounded-lg border px-4 py-3 text-left transition-colors disabled:opacity-60 ${
                  current ? "border-primary-solid bg-bg-elevated" : "border-border bg-bg-surface hover:bg-bg-elevated"
                }`}
              >
                <Flag cc={n.region} className="text-2xl" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{cityOf(n.name)}</span>
                  <span className="block truncate text-xs text-text-muted">
                    {detailOf(n.name)}
                    {detailOf(n.name) && " · "}
                    {n.transports.map((x) => short[x] ?? x).join(", ")}
                  </span>
                </span>
                {current && (
                  <span className="inline-flex items-center gap-1 text-xs text-accent">
                    <Check size={14} aria-hidden /> {t("current")}
                  </span>
                )}
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
