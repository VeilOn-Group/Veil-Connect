import { Trash2 } from "lucide-react";
import { api } from "../api";
import { Button, Card, SectionTitle } from "../components/ui";
import { useAction, usePoll } from "../hooks";
import { useT } from "../i18n";

export function Logs() {
  const t = useT();
  const { data, refresh } = usePoll(() => api.logs(300), 5_000);
  const { busy, run } = useAction();
  const lines = data?.lines ?? [];
  return (
    <div className="flex flex-col gap-4">
      <SectionTitle hint={t("localLogsHint")}>{t("localLogs")}</SectionTitle>
      <Card className="max-h-[52vh] overflow-auto p-3">
        {lines.length === 0 ? (
          <p className="text-xs text-text-muted">{t("noLogs")}</p>
        ) : (
          <pre className="selectable whitespace-pre-wrap break-all font-mono text-[11px] leading-relaxed text-text-muted">{lines.join("\n")}</pre>
        )}
      </Card>
      <Button variant="secondary" loading={busy} onClick={() => run(async () => { await api.wipeLogs(); await refresh(); })}>
        <Trash2 size={16} aria-hidden /> {t("wipeLogs")}
      </Button>
    </div>
  );
}
