import { api, type Status, type TransportID } from "../api";
import { healthLabel, healthTone, transportName } from "../components/TransportCard";
import { Banner, Card, SectionTitle, StatusDot, Toggle } from "../components/ui";
import { useAction } from "../hooks";
import { useT } from "../i18n";

export function Transports({ status, onStatus }: { status: Status; onStatus: (s: Status) => void }) {
  const t = useT();
  const { error, run } = useAction();
  const connected = status.state === "connected";

  const select = (group: "tcp" | "udp", id: TransportID) => run(async () => onStatus(await api.select(group, id)));
  const enable = (id: TransportID, v: boolean) => run(async () => onStatus(await api.setEnabled(id, v)));

  return (
    <div className="flex flex-col gap-4">
      <SectionTitle hint={status.auto_switch ? t("autoOn") : t("autoOff")}>{t("transports")}</SectionTitle>
      {error && <Banner tone="danger">{error}</Banner>}

      {status.transports.map((ts) => (
        <Card key={ts.id}>
          <div className="flex items-start justify-between gap-3">
            <div>
              <h3 className="text-sm font-semibold">{transportName[ts.id]}</h3>
              <div className="mt-1 flex flex-wrap gap-3">
                <StatusDot tone={healthTone(ts)} label={healthLabel(ts, t)} />
                {ts.health.samples > 0 && ts.health.reachable && (
                  <span className="text-xs text-text-muted">
                    {t("loss")}: {Math.round(ts.health.loss * 100)}% · jitter {ts.health.jitter_ms} ms
                  </span>
                )}
              </div>
              {!ts.capabilities.supports_udp && <p className="mt-1 text-xs text-text-muted">{t("naiveNoUdp")}</p>}
            </div>
          </div>
          <Toggle checked={ts.enabled} disabled={!ts.configured} onChange={(v) => enable(ts.id, v)} label={t("enabled")} />
          <div className="mt-1 flex gap-2" role="group" aria-label={transportName[ts.id]}>
            <GroupButton
              label={t("tcpGroup")}
              active={status.active_tcp === ts.id}
              disabled={!connected || !ts.enabled || !ts.configured}
              onClick={() => select("tcp", ts.id)}
            />
            {/* UDP is never offered for naive (ADR-0004); veilond refuses it too. */}
            {ts.capabilities.supports_udp && (
              <GroupButton
                label={t("udpGroup")}
                active={status.active_udp === ts.id}
                disabled={!connected || !ts.enabled || !ts.configured}
                onClick={() => select("udp", ts.id)}
              />
            )}
          </div>
        </Card>
      ))}
    </div>
  );
}

function GroupButton({ label, active, disabled, onClick }: { label: string; active: boolean; disabled: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      disabled={disabled}
      onClick={onClick}
      className={`rounded-md border px-3 py-1.5 text-xs transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${
        active ? "border-primary-solid bg-primary-solid text-text-primary" : "border-border-strong text-text-muted hover:bg-bg-elevated"
      }`}
    >
      {label}
    </button>
  );
}
