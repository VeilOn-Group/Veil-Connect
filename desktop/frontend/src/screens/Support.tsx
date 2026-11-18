import { useContext, useState } from "react";
import { ArrowLeft, Bot, CheckCheck, FileJson, Headset, Plus, Send, UserRound } from "lucide-react";
import { api, type SupportThread, type SupportTicket } from "../api";
import { Banner, Button, Card } from "../components/ui";
import { usePoll, useAction } from "../hooks";
import { LangContext, useT, type Key } from "../i18n";

// Separate Wails window (#/support). Everything goes through veilond, which
// relays to the API and keeps nothing on disk. The AI answers first; the
// "talk to a human" button is always visible inside a conversation.
export function Support() {
  const t = useT();
  const [open, setOpen] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  return (
    <div className="flex min-h-full flex-col gap-4 p-4">
      <header className="flex items-center gap-2">
        {(open || creating) && (
          <Button variant="ghost" className="px-2" onClick={() => (setOpen(null), setCreating(false))} aria-label={t("back")}>
            <ArrowLeft size={16} aria-hidden />
          </Button>
        )}
        <h1 className="text-lg font-semibold">{t("supportTitle")}</h1>
      </header>
      {creating ? (
        <NewTicket onCreated={(id) => (setCreating(false), setOpen(id))} />
      ) : open ? (
        <Thread id={open} />
      ) : (
        <TicketList onOpen={setOpen} onNew={() => setCreating(true)} />
      )}
    </div>
  );
}

const statusKey = (s: SupportTicket["status"]): Key => (`sup_${s}` as Key);

function TicketList({ onOpen, onNew }: { onOpen: (id: string) => void; onNew: () => void }) {
  const t = useT();
  const { data, error } = usePoll(api.supportTickets, 15000);
  return (
    <div className="flex flex-col gap-3">
      <p className="text-xs leading-relaxed text-text-muted">{t("supIntro")}</p>
      <Button onClick={onNew}>
        <Plus size={16} aria-hidden /> {t("supNew")}
      </Button>
      {error && <Banner tone="danger">{error.message}</Banner>}
      {data?.length === 0 && <p className="text-sm text-text-muted">{t("supNone")}</p>}
      <ul className="flex flex-col gap-2">
        {(data ?? []).map((tk) => (
          <li key={tk.id}>
            <button type="button" onClick={() => onOpen(tk.id)} className="w-full rounded-lg border border-border bg-bg-surface p-3 text-left hover:bg-bg-elevated">
              <span className="flex items-center gap-2">
                <span className="min-w-0 flex-1 truncate text-sm font-medium">{tk.subject}</span>
                <span className={`text-[11px] ${tk.status === "waiting_user" ? "text-success" : "text-text-muted"}`}>{t(statusKey(tk.status))}</span>
              </span>
              <span className="text-xs text-text-muted">{tk.updated_at.slice(0, 10)}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function NewTicket({ onCreated }: { onCreated: (id: string) => void }) {
  const t = useT();
  const lang = useContext(LangContext);
  const { busy, error, run } = useAction();
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        void run(async () => onCreated((await api.supportOpen(subject.trim(), body.trim(), lang)).ticket.id));
      }}
    >
      <label className="flex flex-col gap-1.5 text-xs text-text-muted">
        {t("supSubject")}
        <input value={subject} maxLength={200} onChange={(e) => setSubject(e.target.value)} required className="rounded-md border border-border-strong bg-bg-elevated px-3 py-2 text-sm text-text-primary" />
      </label>
      <label className="flex flex-col gap-1.5 text-xs text-text-muted">
        {t("supMessage")}
        <textarea value={body} rows={6} maxLength={8000} onChange={(e) => setBody(e.target.value)} required className="rounded-md border border-border-strong bg-bg-elevated px-3 py-2 text-sm text-text-primary" />
      </label>
      <p className="text-[11px] leading-snug text-text-muted">{t("supPrivacy")}</p>
      {error && <Banner tone="danger">{error}</Banner>}
      <Button type="submit" loading={busy} disabled={!subject.trim() || !body.trim()}>
        <Send size={16} aria-hidden /> {t("supSend")}
      </Button>
    </form>
  );
}

function Thread({ id }: { id: string }) {
  const t = useT();
  const { data, error, setData } = usePoll(() => api.supportTicket(id), 10000);
  const act = useAction();
  const [body, setBody] = useState("");
  const [diag, setDiag] = useState<{ sha256: string; report: unknown } | null>(null);
  if (error && !data) return <Banner tone="danger">{error.message}</Banner>;
  if (!data) return null;
  const tk = data.ticket;
  const closed = tk.status === "closed";
  const withPeople = tk.status === "waiting_human" || data.messages.some((m) => m.author === "staff");
  const update = (th: SupportThread | undefined) => th && setData(th);
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <p className="min-w-0 flex-1 truncate text-sm font-medium">{tk.subject}</p>
        {!withPeople && !closed && (
          <Button variant="secondary" loading={act.busy} onClick={() => void act.run(async () => update(await api.supportHuman(id)))}>
            <UserRound size={16} aria-hidden /> {t("callHuman")}
          </Button>
        )}
      </div>
      {tk.status === "waiting_human" && <Banner tone="info">{t("supWithPeople")}</Banner>}
      <ol className="flex flex-col gap-2" aria-live="polite">
        {data.messages.map((m) => {
          if (m.author === "system")
            return (
              <li key={m.id} className="px-2 text-center text-[11px] text-text-muted">
                {m.body}
              </li>
            );
          const mine = m.author === "user";
          const Icon = m.author === "ai" ? Bot : m.author === "staff" ? Headset : UserRound;
          return (
            <li key={m.id} className={`flex ${mine ? "justify-end" : "justify-start"}`}>
              <Card className={`max-w-[88%] p-3 ${mine ? "bg-bg-elevated" : ""}`}>
                <p className="mb-1 flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wide text-accent">
                  <Icon size={12} aria-hidden /> {t((`supAu_${m.author}`) as Key)}
                </p>
                <p className="selectable whitespace-pre-wrap text-sm leading-relaxed">{m.body.replace(/\s*\[kb:[a-z0-9-]+\]/g, "")}</p>
              </Card>
            </li>
          );
        })}
      </ol>
      {data.attachments.length > 0 && (
        <p className="text-[11px] text-text-muted">
          {t("supAttached")}: {data.attachments.length}
        </p>
      )}
      {act.error && <Banner tone="danger">{act.error}</Banner>}
      {!closed && (
        <>
          <textarea value={body} rows={3} maxLength={8000} onChange={(e) => setBody(e.target.value)} placeholder={t("supReply")} className="rounded-md border border-border-strong bg-bg-elevated px-3 py-2 text-sm text-text-primary" />
          <div className="flex flex-wrap gap-2">
            <Button
              className="flex-1"
              loading={act.busy}
              disabled={!body.trim()}
              onClick={() =>
                void act.run(async () => {
                  update(await api.supportPost(id, body.trim()));
                  setBody("");
                })
              }
            >
              <Send size={16} aria-hidden /> {t("supSend")}
            </Button>
            <Button variant="secondary" onClick={() => void act.run(async () => setDiag(await api.diagnosticsPreview()))}>
              <FileJson size={16} aria-hidden /> {t("supDiag")}
            </Button>
            <Button variant="ghost" onClick={() => void act.run(async () => (await api.supportClose(id), update(await api.supportTicket(id))))}>
              <CheckCheck size={16} aria-hidden /> {t("supClose")}
            </Button>
          </div>
        </>
      )}
      {diag && (
        <div className="fixed inset-0 z-50 grid place-items-center bg-black/60 p-4" role="dialog" aria-modal="true" aria-label={t("supDiag")}>
          <Card className="flex max-h-[90vh] w-full max-w-md flex-col gap-3">
            <p className="text-sm font-semibold">{t("supDiag")}</p>
            <p className="text-xs leading-relaxed text-text-muted">{t("supDiagHint")}</p>
            <pre className="selectable min-h-0 flex-1 overflow-auto rounded-md bg-bg-base p-2 font-mono text-[10px] leading-relaxed">{JSON.stringify(diag.report, null, 2)}</pre>
            <div className="flex gap-2">
              <Button variant="secondary" className="flex-1" onClick={() => setDiag(null)}>
                {t("cancel")}
              </Button>
              <Button
                className="flex-1"
                loading={act.busy}
                onClick={() =>
                  void act.run(async () => {
                    await api.attachDiagnostics(id, diag.sha256);
                    setDiag(null);
                    update(await api.supportTicket(id));
                  })
                }
              >
                {t("supDiagSend")}
              </Button>
            </div>
          </Card>
        </div>
      )}
    </div>
  );
}

