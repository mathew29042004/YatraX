// Date and money formatting for the passenger app. All railway times are IST.

const TZ = "Asia/Kolkata";

export const rupees = (n: number) => "₹" + new Intl.NumberFormat("en-IN").format(n);

/** Today's date in IST as YYYY-MM-DD. */
export function todayIST(): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: TZ }).format(new Date());
}

/** Adds days to a YYYY-MM-DD date. */
export function addDays(date: string, days: number): string {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

export function clockIST(iso: string): string {
  return new Intl.DateTimeFormat("en-GB", { timeZone: TZ, hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(iso));
}

/** "Mon, 2 Nov" for an ISO timestamp or a YYYY-MM-DD date. */
export function dayLabel(isoOrDate: string): string {
  const d = isoOrDate.length === 10 ? new Date(`${isoOrDate}T12:00:00+05:30`) : new Date(isoOrDate);
  return new Intl.DateTimeFormat("en-IN", { timeZone: TZ, weekday: "short", day: "numeric", month: "short" }).format(d);
}

export function duration(min: number): string {
  return `${Math.floor(min / 60)}h ${String(min % 60).padStart(2, "0")}m`;
}

/** Hour of day (IST) of an ISO timestamp. */
export function hourIST(iso: string): number {
  return Number(new Intl.DateTimeFormat("en-GB", { timeZone: TZ, hour: "2-digit", hour12: false }).format(new Date(iso))) % 24;
}

export const DAY_LETTERS = ["M", "T", "W", "T", "F", "S", "S"];
