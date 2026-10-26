const ron = new Intl.NumberFormat("ro-RO", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const whole = new Intl.NumberFormat("ro-RO", { maximumFractionDigits: 0 });

/** 1234.5 -> "1.234,50 RON" */
export function money(amount: number, currency = "RON"): string {
  return `${ron.format(amount)} ${currency}`;
}

/** For chart axes: 12500 -> "12,5k" */
export function compact(n: number): string {
  const a = Math.abs(n);
  if (a >= 1e6) return `${(n / 1e6).toFixed(1).replace(".", ",")}M`;
  if (a >= 1e4) return `${whole.format(n / 1e3)}k`;
  if (a >= 1e3) return `${(n / 1e3).toFixed(1).replace(".", ",")}k`;
  return whole.format(n);
}

const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "2026-03" -> "Mar 26" */
export function monthLabel(ym: string): string {
  const [y, m] = ym.split("-");
  return `${months[Number(m) - 1]} ${y.slice(2)}`;
}

/** An ISO date or timestamp -> "14 Mar 2026" */
export function day(iso: string): string {
  const [y, m, d] = iso.slice(0, 10).split("-");
  return `${Number(d)} ${months[Number(m) - 1]} ${y}`;
}

/** Nice round ticks from 0 (or below) to max. */
export function ticks(min: number, max: number, count = 4): number[] {
  const span = max - min || 1;
  const step0 = span / count;
  const mag = 10 ** Math.floor(Math.log10(step0));
  const step = [1, 2, 2.5, 5, 10].map((f) => f * mag).find((s) => s >= step0) ?? step0;
  const out: number[] = [];
  for (let t = Math.ceil(min / step) * step; t <= max + 1e-9; t += step) out.push(Math.round(t * 100) / 100);
  return out;
}
