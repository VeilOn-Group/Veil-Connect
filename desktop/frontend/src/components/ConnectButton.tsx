import { Power } from "lucide-react";
import type { ConnState } from "../api";
import { useT } from "../i18n";

// Spec: large round button; purple gradient + glow when connected, muted
// grey-violet when disconnected. Busy states pulse the ring.
export function ConnectButton({ state, onToggle, disabled }: { state: ConnState; onToggle: () => void; disabled?: boolean }) {
  const t = useT();
  const on = state === "connected";
  const busy = state === "connecting" || state === "disconnecting" || state === "reconnecting";
  const label = on ? t("disconnect") : t("connect");
  return (
    <div className="flex flex-col items-center gap-5">
      <button
        type="button"
        onClick={onToggle}
        disabled={disabled || busy}
        aria-pressed={on}
        aria-label={label}
        className={`relative grid h-44 w-44 place-items-center rounded-full transition-all duration-300 disabled:cursor-wait ${
          on ? "connect-glow" : "hover:scale-[1.02]"
        }`}
        style={{
          background: on
            ? "radial-gradient(circle at 30% 25%, var(--veil-accent), var(--veil-primary) 45%, var(--veil-primary-deep))"
            : "radial-gradient(circle at 30% 25%, var(--veil-border), var(--veil-bg-elevated) 55%, var(--veil-bg-surface))",
          border: `1px solid ${on ? "var(--veil-accent)" : "var(--veil-border-strong)"}`,
        }}
      >
        {busy && <span className="absolute inset-0 animate-ping rounded-full border-2 border-accent opacity-40" aria-hidden />}
        <Power size={64} strokeWidth={1.75} className={on ? "text-text-primary" : "text-text-muted"} aria-hidden />
      </button>
      <p className="text-lg font-semibold" aria-live="polite">
        {t(state)}
      </p>
    </div>
  );
}
