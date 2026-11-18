import { useContext, useEffect, useState } from "react";
import { Check, X } from "lucide-react";
import { api, type WhatWeStore } from "../api";
import { Banner, Button, Card, SectionTitle } from "../components/ui";
import { useAction } from "../hooks";
import { LangContext, useT } from "../i18n";

// "Privacy" screen (spec 3.4). The content comes from data-inventory.yaml via
// veilond (embedded at build time): the UI cannot claim more than the
// inventory, which CI checks against the code (rule 9).
export function Privacy() {
  const t = useT();
  const lang = useContext(LangContext);
  const [data, setData] = useState<WhatWeStore | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    api.whatWeStore().then(setData, (e: Error) => setErr(e.message));
  }, []);

  return (
    <div className="flex flex-col gap-4">
      <SectionTitle>{t("privacy")}</SectionTitle>
      {err && <Banner tone="danger">{err}</Banner>}
      {data && (
        <>
          <Card>
            <h3 className="mb-2 text-sm font-semibold">{t("whatWeDontStore")}</h3>
            <ul className="flex flex-col gap-1.5">
              {data.not_stored[lang].map((line) => (
                <li key={line} className="flex items-start gap-2 text-sm">
                  <X size={16} className="mt-0.5 shrink-0 text-success" aria-hidden />
                  {line}
                </li>
              ))}
            </ul>
          </Card>
          <Card>
            <h3 className="mb-2 text-sm font-semibold">{t("whatWeStore")}</h3>
            <ul className="flex flex-col gap-3">
              {data.stored.map((item) => (
                <li key={item.category} className="flex items-start gap-2">
                  <Check size={16} className="mt-0.5 shrink-0 text-accent" aria-hidden />
                  <div>
                    <p className="text-sm font-medium">{item[lang].title}</p>
                    <p className="text-xs leading-relaxed text-text-muted">{item[lang].text}</p>
                  </div>
                </li>
              ))}
            </ul>
          </Card>
        </>
      )}
      <DataRights />
    </div>
  );
}

// Data subject rights: export (art. 15/20) and erasure (art. 17), executed
// on the server through veilond.
function DataRights() {
  const t = useT();
  const [confirm, setConfirm] = useState(false);
  const [done, setDone] = useState<string | null>(null);
  const { busy, error, run } = useAction();
  const doExport = () =>
    run(async () => {
      const data = await api.exportAccount();
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = "veilon-my-data.json";
      a.click();
      URL.revokeObjectURL(a.href);
      setDone(t("exportDone"));
    });
  const doDelete = () =>
    run(async () => {
      await api.deleteAccount();
      window.location.reload();
    });
  return (
    <Card className="flex flex-col gap-2">
      <Button variant="secondary" onClick={() => void doExport()} loading={busy && !confirm}>
        {t("exportData")}
      </Button>
      {!confirm ? (
        <Button variant="danger" onClick={() => setConfirm(true)}>
          {t("deleteAccount")}
        </Button>
      ) : (
        <div role="alertdialog" aria-label={t("deleteConfirmTitle")} className="flex flex-col gap-2 rounded-md border border-danger p-3">
          <p className="text-sm font-medium text-danger">{t("deleteConfirmTitle")}</p>
          <p className="text-xs leading-relaxed text-text-muted">{t("deleteConfirmText")}</p>
          <div className="flex gap-2">
            <Button variant="secondary" onClick={() => setConfirm(false)}>
              {t("cancel")}
            </Button>
            <Button variant="danger" loading={busy} onClick={() => void doDelete()}>
              {t("deleteForever")}
            </Button>
          </div>
        </div>
      )}
      {done && <Banner tone="info">{done}</Banner>}
      {error && <Banner tone="danger">{error}</Banner>}
    </Card>
  );
}
