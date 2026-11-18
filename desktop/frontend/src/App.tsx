import { useEffect, useState } from "react";
import { Activity, CircleUser, House, LifeBuoy, Lock, ScrollText, Settings as Gear } from "lucide-react";
import { api, type ProfileSummary } from "./api";
import { Banner } from "./components/ui";
import { Wordmark } from "./components/Wordmark";
import { usePoll } from "./hooks";
import { LangContext, detectLang, useT, type Key, type Lang } from "./i18n";
import { Account, SignIn } from "./screens/Account";
import { Catalog } from "./screens/Catalog";
import { Home } from "./screens/Home";
import { Locations } from "./screens/Locations";
import { CrashReports } from "./screens/CrashReports";
import { SplitTunnel } from "./screens/SplitTunnel";
import { Logs } from "./screens/Logs";
import { Privacy } from "./screens/Privacy";
import { Settings } from "./screens/Settings";
import { Support } from "./screens/Support";
import { Transports } from "./screens/Transports";

type Route = "home" | "transports" | "account" | "settings" | "privacy" | "logs" | "locations" | "split" | "crashes";

const tabs: { id: Route; icon: typeof House; label: Key }[] = [ // short nav labels (navX keys)
  { id: "home", icon: House, label: "navHome" },
  { id: "transports", icon: Activity, label: "navTransports" },
  { id: "account", icon: CircleUser, label: "account" },
  { id: "privacy", icon: Lock, label: "navPrivacy" },
  { id: "logs", icon: ScrollText, label: "logs" },
  { id: "settings", icon: Gear, label: "navSettings" },
];

function useHash() {
  const [hash, setHash] = useState(window.location.hash);
  useEffect(() => {
    const on = () => setHash(window.location.hash);
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return hash;
}

export function App() {
  const [lang, setLang] = useState<Lang>(detectLang());
  const hash = useHash();
  useEffect(() => {
    document.documentElement.lang = lang === "sr" ? "sr-Latn" : lang;
  }, [lang]);
  // The user's language lives in veilond settings; every window (main and
  // support) reads it, so both always speak the same language.
  useEffect(() => {
    api.settings().then((s) => {
      if (s.language === "ru" || s.language === "sr" || s.language === "en") setLang(s.language);
    }, () => {});
  }, []);
  let body;
  if (hash.startsWith("#/support")) body = <Support />;
  else if (hash.startsWith("#/catalog")) body = <Catalog />;
  else body = <MainWindow lang={lang} onLang={setLang} />;
  return <LangContext.Provider value={lang}>{body}</LangContext.Provider>;
}

function MainWindow({ lang, onLang }: { lang: Lang; onLang: (l: Lang) => void }) {
  const t = useT();
  const [route, setRoute] = useState<Route>("home");
  const { data: status, error, setData: setStatus, refresh } = usePoll(api.status, 2_000);
  const [profile, setProfile] = useState<ProfileSummary | null>(null);

  useEffect(() => {
    if (status?.has_profile && !profile) api.profile().then(setProfile, () => {});
  }, [status?.has_profile, profile]);

  if (!status) {
    return (
      <div className="grid h-full place-items-center p-6">
        {error ? <Banner tone="danger">{t("serviceDown")}</Banner> : <span className="text-sm text-text-muted">…</span>}
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between px-5 pt-5">
        <Wordmark className="text-xl" />
        <button
          type="button"
          onClick={() => void api.openSupport()}
          className="inline-flex items-center gap-1.5 rounded-md border border-border-strong px-3 py-1.5 text-xs text-text-primary hover:bg-bg-elevated"
        >
          <LifeBuoy size={14} className="text-accent" aria-hidden />
          {t("support")}
        </button>
      </header>

      <main className="flex-1 overflow-y-auto px-5 py-4">
        {!status.logged_in ? (
          <SignIn onDone={() => void refresh().then(() => setRoute("home"))} />
        ) : (
          <>
            {route === "home" && <Home status={status} profile={profile} onStatus={setStatus} go={(r) => setRoute(r as Route)} />}
            {route === "transports" && <Transports status={status} onStatus={setStatus} />}
            {route === "account" && <Account profile={profile} onProfile={setProfile} onLoggedOut={() => { setProfile(null); void refresh(); }} />}
            {route === "settings" && <Settings lang={lang} onLang={onLang} go={(r) => setRoute(r as Route)} />}
            {route === "split" && <SplitTunnel onBack={() => setRoute("settings")} />}
            {route === "crashes" && <CrashReports onBack={() => setRoute("settings")} />}
            {route === "locations" && <Locations status={status} profile={profile} onStatus={setStatus} onDone={() => setRoute("home")} />}
            {route === "privacy" && <Privacy />}
            {route === "logs" && <Logs />}
          </>
        )}
      </main>

      {status.logged_in && (
        <nav className="grid grid-cols-6 border-t border-border bg-bg-surface" aria-label="VeilOn">
          {tabs.map(({ id, icon: Icon, label }) => (
            <button
              key={id}
              type="button"
              onClick={() => setRoute(id)}
              aria-current={route === id ? "page" : undefined}
              className={`flex min-w-0 flex-col items-center gap-1 px-0.5 py-2.5 text-[10px] ${route === id || (id === "settings" && (route === "split" || route === "crashes")) || (id === "home" && route === "locations") ? "text-accent" : "text-text-muted hover:text-text-primary"}`}
            >
              <Icon size={18} aria-hidden />
              <span className="w-full truncate text-center">{t(label)}</span>
            </button>
          ))}
        </nav>
      )}
    </div>
  );
}
