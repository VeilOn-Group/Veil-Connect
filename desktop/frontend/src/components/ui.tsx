import type { ButtonHTMLAttributes, ReactNode } from "react";

// Primitives built only on design tokens: text on buttons uses
// primary-solid (#7C3AED, 5.19:1): never #8B5CF6, which fails AA for text.

type Variant = "primary" | "secondary" | "ghost" | "danger";

export function Button({
  variant = "primary",
  loading,
  className = "",
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; loading?: boolean }) {
  const styles: Record<Variant, string> = {
    primary: "bg-primary-solid hover:bg-primary-hover active:bg-primary-deep text-text-primary",
    secondary: "bg-bg-elevated hover:bg-bg-surface border border-border-strong text-text-primary",
    ghost: "bg-transparent hover:bg-bg-elevated text-accent",
    danger: "bg-transparent border border-danger text-danger hover:bg-bg-elevated",
  };
  return (
    <button
      {...rest}
      disabled={rest.disabled || loading}
      aria-busy={loading || undefined}
      className={`inline-flex items-center justify-center gap-2 rounded-md px-4 py-2.5 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${styles[variant]} ${className}`}
    >
      {loading && <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-current border-t-transparent" aria-hidden />}
      {children}
    </button>
  );
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <section className={`rounded-lg border border-border bg-bg-surface p-4 ${className}`}>{children}</section>;
}

export function SectionTitle({ children, hint }: { children: ReactNode; hint?: string }) {
  return (
    <div className="mb-3">
      <h2 className="text-base font-semibold text-text-primary">{children}</h2>
      {hint && <p className="mt-1 text-xs leading-relaxed text-text-muted">{hint}</p>}
    </div>
  );
}

export function Toggle({
  checked,
  onChange,
  label,
  hint,
  disabled,
  badge,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint?: string;
  disabled?: boolean;
  badge?: string;
}) {
  return (
    <label className={`flex items-start justify-between gap-4 py-2.5 ${disabled ? "opacity-50" : "cursor-pointer"}`}>
      <span className="text-sm">
        <span className="text-text-primary">{label}</span>
        {badge && <span className="ml-2 rounded-full bg-bg-elevated px-2 py-0.5 text-[11px] text-text-muted">{badge}</span>}
        {hint && <span className="mt-0.5 block text-xs text-text-muted">{hint}</span>}
      </span>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        aria-label={label}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={`relative mt-0.5 h-6 w-11 shrink-0 rounded-full border transition-colors ${
          checked ? "border-primary-solid bg-primary-solid" : "border-border-strong bg-bg-elevated"
        }`}
      >
        <span
          aria-hidden
          className="absolute rounded-full bg-text-primary transition-transform"
          style={{ left: 2, top: 2, width: 18, height: 18, transform: `translateX(${checked ? 20 : 0}px)` }}
        />
      </button>
    </label>
  );
}

export type Tone = "success" | "warning" | "danger" | "muted";

export function StatusDot({ tone, label }: { tone: Tone; label: string }) {
  const c: Record<Tone, string> = { success: "bg-success", warning: "bg-warning", danger: "bg-danger", muted: "bg-text-muted" };
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-text-muted">
      <span className={`h-2 w-2 rounded-full ${c[tone]}`} aria-hidden />
      {label}
    </span>
  );
}

export function Banner({ tone, children }: { tone: "warning" | "danger" | "info"; children: ReactNode }) {
  const c = { warning: "border-warning text-warning", danger: "border-danger text-danger", info: "border-accent text-accent" }[tone];
  return <div role="status" className={`rounded-md border bg-bg-elevated px-3 py-2 text-xs ${c}`}>{children}</div>;
}
