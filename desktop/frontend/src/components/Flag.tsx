// Country flag from an ISO 3166-1 alpha-2 code (regional indicator symbols).
export function Flag({ cc, className = "" }: { cc: string; className?: string }) {
  const f = /^[A-Z]{2}$/.test(cc) ? String.fromCodePoint(...[...cc].map((c) => 0x1f1a5 + c.charCodeAt(0))) : "🌐";
  return (
    <span aria-hidden className={`inline-block leading-none ${className}`}>
      {f}
    </span>
  );
}

/** City part of a profile node name ("Beograd · Beograd 1" -> "Beograd"). */
export const cityOf = (name: string) => name.split(" · ")[0] ?? name;
export const detailOf = (name: string) => name.split(" · ")[1] ?? "";
