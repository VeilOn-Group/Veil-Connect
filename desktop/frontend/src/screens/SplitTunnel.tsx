import { useEffect, useState } from "react";
import { ArrowLeft, Plus, Trash2 } from "lucide-react";
import { api, type Settings, type SplitAction, type SplitKind, type SplitRule } from "../api";
import { Banner, Button, Card, SectionTitle, Toggle } from "../components/ui";
import { useAction } from "../hooks";
import { useT, type Key } from "../i18n";

const kinds: SplitKind[] = ["domain_suffix", "domain", "ip", "app"];
const actions: SplitAction[] = ["direct", "proxy", "block"];
const placeholder: Record<SplitKind, string> = { domain: "www.example.rs", domain_suffix: "example.rs", ip: "192.0.2.0/24", app: "telegram.exe" };

// Split tunnelling rules (veilond validates them again and refuses bad input).
export function SplitTunnel({ onBack }: { onBack: () => void }) {
  const t = useT();
  const [s, setS] = useState<Settings | null>(null);
  const [rules, setRules] = useState<SplitRule[]>([]);
  const [draft, setDraft] = useState<SplitRule>({ kind: "domain_suffix", value: "", action: "direct" });
  const [macKs, setMacKs] = useState(false);
  const { busy, error, run } = useAction();
  useEffect(() => {
    api.settings().then((x) => {
      setS(x);
      setRules(x.split_rules ?? []);
    }, () => {});
    api.status().then((st) => setMacKs(st.bypass_ru === "tunnel_kill_switch"), () => {});
  }, []);
  const setBypass = (on: boolean) =>
    run(async () => {
      if (!s) return;
      setS(await api.saveSettings({ ...s, bypass_ru: on }));
      const st = await api.status();
      setMacKs(st.bypass_ru === "tunnel_kill_switch");
    });
  const save = (next: SplitRule[]) =>
    run(async () => {
      if (!s) return;
      const saved = await api.saveSettings({ ...s, split_rules: next });
      setS(saved);
      setRules(saved.split_rules ?? []);
    });
  const add = async () => {
    if (!draft.value.trim()) return;
    await save([...rules, { ...draft, value: draft.value.trim() }]);
    setDraft({ ...draft, value: "" });
  };
  const sel = "rounded-md border border-border-strong bg-bg-elevated px-2 py-2 text-sm text-text-primary";
  return (
    <div className="flex flex-col gap-4">
      <button type="button" onClick={onBack} className="inline-flex items-center gap-1.5 self-start text-xs text-accent">
        <ArrowLeft size={14} aria-hidden /> {t("settings")}
      </button>
      <SectionTitle hint={t("splitHint")}>{t("splitTunnel")}</SectionTitle>
      {error && <Banner tone="danger">{error}</Banner>}
      <Card>
        <Toggle checked={s?.bypass_ru !== false} disabled={!s || busy} onChange={(v) => void setBypass(v)} label={t("bypassRu")} hint={t("bypassRuHint")} />
        {macKs && s?.bypass_ru !== false && <p className="pb-2 text-xs text-warning">{t("bypassRuMacKs")}</p>}
      </Card>
      <Card className="flex flex-col gap-2">
        {rules.length === 0 && <p className="text-xs text-text-muted">{t("noRules")}</p>}
        {rules.map((r, i) => (
          <div key={`${r.kind}:${r.value}:${i}`} className="flex items-center gap-2 border-b border-border py-2 last:border-b-0">
            <span className="min-w-0 flex-1">
              <span className="block truncate font-mono text-sm">{r.value}</span>
              <span className="text-[11px] text-text-muted">{t(`kind_${r.kind}` as Key)}</span>
            </span>
            <span className={`rounded-full bg-bg-elevated px-2 py-0.5 text-[11px] ${r.action === "block" ? "text-danger" : r.action === "direct" ? "text-warning" : "text-success"}`}>{t(`act_${r.action}` as Key)}</span>
            <button type="button" aria-label={t("remove")} disabled={busy} onClick={() => void save(rules.filter((_, j) => j !== i))} className="rounded-md p-1.5 text-text-muted hover:bg-bg-elevated hover:text-danger">
              <Trash2 size={15} aria-hidden />
            </button>
          </div>
        ))}
      </Card>
      <Card className="flex flex-col gap-2">
        <div className="grid grid-cols-2 gap-2">
          <select aria-label={t("kind_domain")} value={draft.kind} onChange={(e) => setDraft({ ...draft, kind: e.target.value as SplitKind })} className={sel}>
            {kinds.map((k) => (
              <option key={k} value={k}>
                {t(`kind_${k}` as Key)}
              </option>
            ))}
          </select>
          <select aria-label={t("act_direct")} value={draft.action} onChange={(e) => setDraft({ ...draft, action: e.target.value as SplitAction })} className={sel}>
            {actions.map((a) => (
              <option key={a} value={a}>
                {t(`act_${a}` as Key)}
              </option>
            ))}
          </select>
        </div>
        <input
          aria-label={t("value")}
          value={draft.value}
          placeholder={placeholder[draft.kind]}
          spellCheck={false}
          onChange={(e) => setDraft({ ...draft, value: e.target.value })}
          onKeyDown={(e) => e.key === "Enter" && void add()}
          className="rounded-md border border-border-strong bg-bg-elevated px-3 py-2 font-mono text-sm text-text-primary placeholder:text-text-muted"
        />
        <Button onClick={() => void add()} loading={busy} disabled={!draft.value.trim()}>
          <Plus size={15} aria-hidden /> {t("addRule")}
        </Button>
      </Card>
    </div>
  );
}
