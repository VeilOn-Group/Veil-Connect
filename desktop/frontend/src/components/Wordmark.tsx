import mark from "../../../../../veil-shared/brand/dist/veilon-mark.svg";

// "VeilOn" wordmark: the brand mark replaces the letter V (ADR-0051).
// The mark sits on the text baseline and scales with font-size, so the same
// component works in headers, sign-in screens and panels.
export function Wordmark({ className = "", suffix }: { className?: string; suffix?: string }) {
  return (
    <span role="img" aria-label={suffix ? `VeilOn ${suffix}` : "VeilOn"} className={`inline-flex items-baseline font-semibold tracking-tight ${className}`}>
      <img src={mark} alt="" aria-hidden draggable={false} className="mr-[0.01em] h-[0.76em] w-[0.76em] self-baseline" />
      <span aria-hidden>
        eil<span className="text-accent">On</span>
        {suffix && <span className="ml-1.5 text-[0.75em] font-normal text-text-muted">{suffix}</span>}
      </span>
    </span>
  );
}

// Standalone mark (e.g. sign-in screens, empty states).
export function Mark({ className = "h-10 w-10" }: { className?: string }) {
  return <img src={mark} alt="VeilOn" draggable={false} className={className} />;
}
