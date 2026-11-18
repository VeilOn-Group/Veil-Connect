import { useState } from "react";
import { ArrowLeft, ChevronDown, ChevronUp, Send, Trash2 } from "lucide-react";
import { api, type CrashReport } from "../api";
import { Banner, Button, Card, SectionTitle } from "../components/ui";
import { useAction, usePoll } from "../hooks";
import { useT } from "../i18n";

// Crash reports waiting on this device. The text shown is
// exactly what would be sent: already cleaned of addresses, host names
// and paths.
export function CrashReports({ onBack }: { onBack: () => void }) {
  const t = useT();
  const { data, refresh } = usePoll(api.crashReports, 30_000);
  const { busy, error, run } = useAction();
  const [open, setOpen] = useState<string | null>(null);
  const act = (fn: () => Promise<unknown>) =>
    run(async () => {
      await fn();
      await refresh();
    });
  const reports = data?.reports ?? [];
  return (
    <div className="flex flex-col gap-4">
      <button type="button" onClick={onBack} className="inline-flex items-center gap-1.5 self-start text-xs text-accent">
        <ArrowLeft size={14} aria-hidden /> {t("settings")}
      </button>
      <SectionTitle hint={data?.auto_send ? t("crashAutoOn") : t("crashAutoOff")}>{t("crashTitle")}</SectionTitle>
      {reports.length === 0 && <p className="text-sm text-text-muted">{t("crashNone")}</p>}
      {reports.map((r) => (
        <Card key={r.id} className="flex flex-col gap-2">
          <button type="button" onClick={() => setOpen(open === r.id ? null : r.id)} className="flex items-start justify-between gap-3 text-left" aria-expanded={open === r.id}>
            <span className="min-w-0">
              <span className="block truncate font-mono text-xs text-text-primary">{r.title}</span>
              <span className="mt-1 block text-[11px] text-text-muted">{meta(r)}</span>
            </span>
            {open === r.id ? <ChevronUp size={16} className="shrink-0 text-text-muted" aria-hidden /> : <ChevronDown size={16} className="shrink-0 text-text-muted" aria-hidden />}
          </button>
          {open === r.id && (
            <>
              <p className="text-[11px] text-text-muted">{t("crashPreviewHint")}</p>
              <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md bg-bg-elevated p-3 font-mono text-[11px] leading-relaxed text-text-primary">{r.text}</pre>
            </>
          )}
          <div className="flex gap-2">
            <Button variant="secondary" loading={busy} onClick={() => void act(() => api.sendCrash(r.id))}>
              <Send size={14} aria-hidden /> {t("crashSend")}
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => void act(() => api.deleteCrash(r.id))}>
              <Trash2 size={14} aria-hidden /> {t("crashDelete")}
            </Button>
          </div>
        </Card>
      ))}
      {error && <Banner tone="danger">{error}</Banner>}
    </div>
  );
}

function meta(r: CrashReport) {
  const comp = r.component_version ? `${r.component} ${r.component_version}` : r.component;
  return `${r.day} · ${comp} · VeilOn ${r.app_version}`;
}
