import { useEffect, useRef, useState } from "react";
import { Bitcoin, CircleCheck, CreditCard, Copy, ExternalLink, Eye, EyeOff, RefreshCw } from "lucide-react";
import { api, ApiError, type BillingOffer, type BillingOrder, type ProfileSummary } from "../api";
import { Banner, Button, Card, SectionTitle } from "../components/ui";
import { useAction } from "../hooks";
import { useT } from "../i18n";

function groupDigits(n: string) {
  return n.replace(/\s/g, "").replace(/(\d{4})(?=\d)/g, "$1 ");
}

// Signed-out view: anonymous registration or login by account number.
export function SignIn({ onDone }: { onDone: () => void }) {
  const t = useT();
  const { busy, error, run } = useAction();
  const [number, setNumber] = useState("");
  const [created, setCreated] = useState<string | null>(null);

  if (created) {
    return (
      <Card className="flex flex-col gap-3">
        <SectionTitle hint={t("saveNumberText")}>{t("saveNumberTitle")}</SectionTitle>
        <p className="selectable rounded-md bg-bg-elevated px-3 py-3 text-center font-mono text-xl tracking-wider">{groupDigits(created)}</p>
        <Button variant="secondary" onClick={() => void navigator.clipboard?.writeText(created)}>
          <Copy size={16} aria-hidden /> {t("copy")}
        </Button>
        <Button onClick={onDone}>{t("saved")}</Button>
      </Card>
    );
  }
  return (
    <div className="flex flex-col gap-4">
      <Card className="flex flex-col gap-3">
        <SectionTitle hint={t("createAccountHint")}>{t("createAccount")}</SectionTitle>
        <Button loading={busy} onClick={() => run(async () => setCreated((await api.register()).account_number))}>
          {t("createAccount")}
        </Button>
      </Card>
      <Card className="flex flex-col gap-3">
        <SectionTitle>{t("login")}</SectionTitle>
        <label className="text-xs text-text-muted" htmlFor="acc">
          {t("accountNumber")}
        </label>
        <input
          id="acc"
          inputMode="numeric"
          autoComplete="off"
          spellCheck={false}
          value={groupDigits(number)}
          onChange={(e) => setNumber(e.target.value.replace(/\D/g, "").slice(0, 16))}
          placeholder="0000 0000 0000 0000"
          className="selectable rounded-md border border-border-strong bg-bg-elevated px-3 py-2.5 font-mono text-base tracking-wider text-text-primary placeholder:text-text-muted"
        />
        <Button variant="secondary" loading={busy} disabled={number.length !== 16} onClick={() => run(async () => { await api.login(number); onDone(); })}>
          {t("login")}
        </Button>
      </Card>
      {error && <Banner tone="danger">{error}</Banner>}
    </div>
  );
}

export function Account({ profile, onProfile, onLoggedOut }: {
  profile: ProfileSummary | null;
  onProfile: (p: ProfileSummary) => void;
  onLoggedOut: () => void;
}) {
  const t = useT();
  const { busy, error, run } = useAction();
  const [shown, setShown] = useState<string | null>(null);
  const reveal = () => run(async () => setShown(shown ? null : (await api.account(true)).account_number ?? null));
  const [masked, setMasked] = useState<string>("");
  useEffect(() => {
    api.account().then((a) => setMasked(a.account_number ?? ""), () => setMasked(""));
  }, []);

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <SectionTitle>{t("accountNumber")}</SectionTitle>
        <div className="flex items-center justify-between gap-2">
          <span className="selectable font-mono text-base tracking-wider">{shown ? groupDigits(shown) : masked}</span>
          <Button variant="ghost" onClick={reveal} aria-label={shown ? t("hide") : t("reveal")}>
            {shown ? <EyeOff size={16} aria-hidden /> : <Eye size={16} aria-hidden />}
            {shown ? t("hide") : t("reveal")}
          </Button>
        </div>
      </Card>
      {profile && (
        <Card>
          <dl className="grid grid-cols-2 gap-y-2 text-sm">
            <dt className="text-text-muted">{t("plan")}</dt>
            <dd>{profile.subscription.plan}</dd>
            <dt className="text-text-muted">{t("validUntil")}</dt>
            <dd>{profile.subscription.ends_on}</dd>
            <dt className="text-text-muted">{t("devices")}</dt>
            <dd>≤ {profile.subscription.device_limit}</dd>
          </dl>
          <Button className="mt-4 w-full" variant="secondary" loading={busy} onClick={() => run(async () => onProfile(await api.refreshProfile()))}>
            <RefreshCw size={16} aria-hidden /> {t("refreshProfile")}
          </Button>
        </Card>
      )}
      <Purchase onPaid={(p) => onProfile(p)} />
      {error && <Banner tone="danger">{error}</Banner>}
      <Button variant="danger" loading={busy} onClick={() => run(async () => { await api.logout(); onLoggedOut(); })}>
        {t("logout")}
      </Button>
    </div>
  );
}

const DECIMALS: Record<string, number> = { RUB: 2, USD: 2, EUR: 2, RSD: 2 };
function money(minor: number, cur: string) {
  const d = DECIMALS[cur] ?? 2;
  return `${(minor / 10 ** d).toFixed(d)} ${cur === "RUB" ? "₽" : cur}`;
}

/**
 * Purchase: offers, provider choice, checkout. The provider page opens in
 * the system browser (the UI process reads its address from veilond); the
 * order is polled until the server reports it fulfilled, then the signed
 * profile is refreshed. Nothing about the purchase is stored on the device.
 */
function Purchase({ onPaid }: { onPaid: (p: ProfileSummary) => void }) {
  const t = useT();
  const { busy, error, run } = useAction();
  const [offers, setOffers] = useState<BillingOffer[] | null>(null);
  const [plan, setPlan] = useState("");
  const [order, setOrder] = useState<BillingOrder | null>(null);
  const [phase, setPhase] = useState<"idle" | "waiting" | "done" | "expired">("idle");
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => {
    api.offers().then(
      (o) => {
        setOffers(o);
        if (o[0]) setPlan(o[0].plan_id);
      },
      () => setOffers([]),
    );
    return () => window.clearInterval(timer.current);
  }, []);

  const poll = () => {
    window.clearInterval(timer.current);
    let fails = 0;
    timer.current = window.setInterval(async () => {
      try {
        const o = await api.order();
        setOrder(o);
        if (o.status === "fulfilled") {
          window.clearInterval(timer.current);
          setPhase("done");
          onPaid(await api.profile());
        } else if (o.status === "expired" || o.status === "canceled") {
          window.clearInterval(timer.current);
          setPhase("expired");
        }
      } catch (e) {
        // 404: veilond forgot the order (restart). Stop after a few errors.
        if ((e instanceof ApiError && e.status === 404) || ++fails > 5) {
          window.clearInterval(timer.current);
          setPhase("idle");
        }
      }
    }, 3000);
  };

  const pay = (provider: string) =>
    run(async () => {
      await api.checkout(plan, provider);
      await api.openPayment();
      setPhase("waiting");
      poll();
    });

  const offer = offers?.find((o) => o.plan_id === plan);
  return (
    <Card>
      <SectionTitle hint={t("paymentsHint")}>{t("payments")}</SectionTitle>
      {offers && offers.length === 0 && <p className="text-xs text-text-muted">{t("payNone")}</p>}
      {phase === "done" && (
        <Banner tone="info">
          <span className="inline-flex items-center gap-2">
            <CircleCheck size={16} aria-hidden /> {t("payDone")}
          </span>
        </Banner>
      )}
      {phase === "expired" && <Banner tone="warning">{t("payExpired")}</Banner>}
      {phase === "waiting" && (
        <div className="flex flex-col gap-3" role="status">
          <p className="flex items-center gap-2 text-sm">
            <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-accent border-t-transparent" aria-hidden />
            {t("payWaiting")}
          </p>
          {order && (
            <p className="font-mono text-xs text-text-muted">
              {money(order.amount_minor, order.currency)} · {order.payment_status || order.status}
            </p>
          )}
          <Button variant="ghost" onClick={() => void api.openPayment()}>
            <ExternalLink size={16} aria-hidden /> {t("payReopen")}
          </Button>
        </div>
      )}
      {phase !== "waiting" && offers && offers.length > 0 && (
        <div className="flex flex-col gap-3">
          <div className="grid gap-2" role="radiogroup" aria-label={t("plan")}>
            {offers.map((o) => (
              <button
                key={o.plan_id}
                type="button"
                role="radio"
                aria-checked={plan === o.plan_id}
                onClick={() => setPlan(o.plan_id)}
                className={`flex items-center justify-between rounded-md border px-3 py-2.5 text-left text-sm ${plan === o.plan_id ? "border-accent bg-bg-elevated" : "border-border hover:bg-bg-elevated"}`}
              >
                <span>
                  {o.name}
                  <span className="block text-xs text-text-muted">
                    {o.duration_days} {t("payDays")} · ≤ {o.device_limit} {t("devices").toLowerCase()}
                  </span>
                </span>
                <span className="text-right font-mono text-xs text-text-muted">
                  {o.prices.map((p) => (
                    <span key={p.provider} className="block">
                      {money(p.amount_minor, p.currency)}
                    </span>
                  ))}
                </span>
              </button>
            ))}
          </div>
          <p className="text-xs text-text-muted">{t("payButton")}:</p>
          {offer?.prices.map((p) => (
            <Button key={p.provider} variant={p.provider === "yookassa" ? "primary" : "secondary"} loading={busy} onClick={() => pay(p.provider)}>
              {p.provider === "heleket" ? <Bitcoin size={16} aria-hidden /> : <CreditCard size={16} aria-hidden />}
              {p.provider === "heleket" ? t("payCrypto") : t("payCard")} · {money(p.amount_minor, p.currency)}
            </Button>
          ))}
        </div>
      )}
      {error && <Banner tone="danger">{error}</Banner>}
    </Card>
  );
}
