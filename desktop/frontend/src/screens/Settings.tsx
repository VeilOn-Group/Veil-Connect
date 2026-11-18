import { useState } from "react";
import { ChevronRight, Download, RefreshCw } from "lucide-react";
import { api, type Settings as S, type UpdateInfo } from "../api";
import { Banner, Button, Card, SectionTitle, Toggle } from "../components/ui";
import { useAction, usePoll } from "../hooks";
import { useT, type Lang } from "../i18n";

export function Settings({ lang, onLang, go }: { lang: Lang; onLang: (l: Lang) => void; go: (r: string) => void }) {
  const t = useT();
  const { data: s, setData } = usePoll(api.settings, 30_000);
  const { data: login, setData: setLogin } = usePoll(api.autostart, 60_000);
  const { data: crashes } = usePoll(api.crashReports, 60_000);
  const { error, run } = useAction();
  if (!s) return null;
  const queued = crashes?.reports.length ?? 0;
  const save = (patch: Partial<S>) => run(async () => setData(await api.saveSettings({ ...s, ...patch })));
  const rules = s.split_rules?.length ?? 0;

  return (
    <div className="flex flex-col gap-4">
      <SectionTitle>{t("settings")}</SectionTitle>
      <Card>
        <Toggle checked={s.kill_switch} onChange={(v) => save({ kill_switch: v })} label={t("killSwitch")} hint={t("killSwitchHint")} />
        <Toggle checked={s.auto_switch} onChange={(v) => save({ auto_switch: v })} label={t("autoSwitch")} hint={t("autoSwitchHint")} />
        <button type="button" onClick={() => go("split")} className="flex w-full items-center justify-between gap-4 py-2.5 text-left text-sm">
          <span>
            <span className="text-text-primary">{t("splitTunnel")}</span>
            <span className="mt-0.5 block text-xs text-text-muted">{rules > 0 ? `${t("rulesCount")}: ${rules}` : t("noRules")}</span>
          </span>
          <ChevronRight size={18} className="text-text-muted" aria-hidden />
        </button>
      </Card>
      <Card>
        <Toggle checked={s.allow_lan} onChange={(v) => save({ allow_lan: v })} label={t("allowLan")} />
        <Toggle checked={s.clear_logs_on_exit} onChange={(v) => save({ clear_logs_on_exit: v })} label={t("clearLogsOnExit")} />
      </Card>
      <Card>
        <Toggle
          checked={login?.enabled ?? false}
          disabled={!login?.supported}
          onChange={(v) => run(async () => setLogin(await api.setAutostart(v)))}
          label={t("autostart")}
          hint={t("autostartHint")}
        />
        <Toggle checked={s.auto_connect ?? false} onChange={(v) => save({ auto_connect: v })} label={t("autoConnect")} hint={s.kill_switch ? t("autoConnectHintKs") : t("autoConnectHint")} />
      </Card>
      <Card>
        <Toggle checked={s.crash_reports} onChange={(v) => save({ crash_reports: v })} label={t("crashReports")} hint={t("crashReportsHint")} />
        <button type="button" onClick={() => go("crashes")} className="flex w-full items-center justify-between gap-4 py-2.5 text-left text-sm">
          <span>
            <span className="text-text-primary">{t("crashTitle")}</span>
            <span className="mt-0.5 block text-xs text-text-muted">{queued > 0 ? `${t("crashQueued")}: ${queued}` : t("crashNone")}</span>
          </span>
          <ChevronRight size={18} className="text-text-muted" aria-hidden />
        </button>
      </Card>
      <DnsCard s={s} save={save} />
      <UpdateCard />
      <Card>
        <label className="flex items-center justify-between text-sm">
          {t("language")}
          <select
            value={lang}
            onChange={(e) => {
              const l = e.target.value as Lang;
              onLang(l);
              void save({ language: l });
            }}
            className="rounded-md border border-border-strong bg-bg-elevated px-2 py-1.5 text-sm text-text-primary"
          >
            <option value="ru">Русский</option>
            <option value="sr">Srpski</option>
            <option value="en">English</option>
          </select>
        </label>
      </Card>
      {error && <Banner tone="danger">{error}</Banner>}
    </div>
  );
}

/**
 * DNS over HTTPS through the tunnel (ADR-0073). Quad9 by default; the
 * resolver sees the server's address, never the user's.
 */
function DnsCard({ s, save }: { s: S; save: (p: Partial<S>) => Promise<unknown> }) {
  const t = useT();
  const { data } = usePoll(api.dnsPresets, 3_600_000);
  const cur = s.dns?.provider || data?.default || "quad9";
  const [custom, setCustom] = useState(s.dns?.custom_url ?? "");
  const [editing, setEditing] = useState(cur === "custom");
  const preset = data?.presets.find((p) => p.id === cur);
  const valid = /^https:\/\/[^\s/?#@]+(\/[^\s?#]*)?$/.test(custom.trim());
  return (
    <Card className="flex flex-col gap-2">
      <label className="flex items-center justify-between gap-3 text-sm">
        <span>
          {t("dns")}
          <span className="mt-0.5 block text-xs text-text-muted">{t("dnsHint")}</span>
        </span>
        <select
          value={editing ? "custom" : cur}
          aria-label={t("dns")}
          onChange={(e) => {
            const v = e.target.value;
            if (v === "custom") {
              setEditing(true);
              return;
            }
            setEditing(false);
            void save({ dns: { provider: v } });
          }}
          className="shrink-0 rounded-md border border-border-strong bg-bg-elevated px-2 py-1.5 text-sm text-text-primary"
        >
          {(data?.presets ?? []).map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
              {p.id === data?.default ? ` (${t("dnsDefault")})` : ""}
            </option>
          ))}
          <option value="custom">{t("dnsCustom")}</option>
        </select>
      </label>
      {!editing && preset && (
        <p className="font-mono text-[11px] text-text-muted">
          https://{preset.host}
          {preset.path} · {preset.ips[0]}
        </p>
      )}
      {editing && (
        <div className="flex flex-col gap-2">
          <input
            value={custom}
            onChange={(e) => setCustom(e.target.value)}
            placeholder="https://doh.example.net/dns-query"
            spellCheck={false}
            autoComplete="off"
            aria-label={t("dnsCustomUrl")}
            className="rounded-md border border-border-strong bg-bg-elevated px-3 py-2 font-mono text-xs text-text-primary"
          />
          <p className="text-[11px] leading-snug text-text-muted">{t("dnsCustomHint")}</p>
          <Button variant="secondary" disabled={!valid} onClick={() => void save({ dns: { provider: "custom", custom_url: custom.trim() } })}>
            {t("dnsApply")}
          </Button>
        </div>
      )}
    </Card>
  );
}

function UpdateCard() {
  const t = useT();
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  const { busy, error, run } = useAction();
  const mb = (n?: number) => (n ? `${(n / 1024 / 1024).toFixed(1)} MB` : "");
  return (
    <Card className="flex flex-col gap-2">
      <p className="text-sm text-text-primary">{t("update")}</p>
      <p className="text-xs leading-relaxed text-text-muted">{t("updateHint")}</p>
      {info && !info.available && <p className="text-xs text-success">{t("upToDate")} ({info.current})</p>}
      {info?.available && (
        <div className="rounded-md bg-bg-elevated p-3 text-xs">
          <p className="text-text-primary">
            {t("updateAvailable")} {info.version} · {mb(info.size)}
          </p>
          {info.notes && <p className="mt-1 text-text-muted">{info.notes}</p>}
          {info.downloaded && !info.installing && (
            <p className="mt-2 break-all text-success">
              {t("downloaded")}{info.silent_install ? "" : <>: <span className="font-mono">{info.path}</span></>}
            </p>
          )}
          {info.installing && <p className="mt-2 text-success">{t("installing")}</p>}
        </div>
      )}
      {error && <Banner tone="danger">{error}</Banner>}
      <div className="flex gap-2">
        <Button variant="secondary" loading={busy && !info?.available} onClick={() => void run(async () => setInfo(await api.checkUpdate()))}>
          <RefreshCw size={15} aria-hidden /> {t("checkUpdate")}
        </Button>
        {info?.available && !info.downloaded && (
          <Button loading={busy} onClick={() => void run(async () => setInfo(await api.downloadUpdate()))}>
            <Download size={15} aria-hidden /> {t("download")}
          </Button>
        )}
        {info?.downloaded && info.silent_install && !info.installing && (
          <Button loading={busy} onClick={() => void run(async () => setInfo(await api.installUpdate()))}>
            <Download size={15} aria-hidden /> {t("installNow")}
          </Button>
        )}
      </div>
    </Card>
  );
}
