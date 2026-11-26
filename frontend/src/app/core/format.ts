const date = new Intl.DateTimeFormat("th-TH-u-ca-buddhist", {
  day: "numeric",
  month: "short",
  year: "numeric",
  timeZone: "UTC",
});
const time = new Intl.DateTimeFormat("th-TH-u-ca-buddhist", {
  day: "numeric",
  month: "short",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
  timeZone: "Asia/Bangkok",
});
export function thaiDate(value: string | null | undefined): string {
  if (!value) return "—";
  const d = new Date(value.length === 10 ? value + "T12:00:00Z" : value);
  return Number.isNaN(d.getTime()) ? "—" : date.format(d);
}
export function thaiTime(seconds: number | null | undefined): string {
  if (!seconds) return "ยังไม่มีข้อมูล";
  const d = new Date(seconds * 1000);
  if (Number.isNaN(d.getTime())) return "—";
  const p = time.formatToParts(d);
  const pick = (type: string) => p.find((x) => x.type === type)?.value || "";
  return `${pick("day")} ${pick("month")} ${pick("year")} เวลา ${pick("hour")}:${pick("minute")} น.`;
}
export function count(value: number | null | undefined): string {
  return typeof value === "number" && Number.isFinite(value)
    ? new Intl.NumberFormat("th-TH", { maximumFractionDigits: 1 }).format(value)
    : "—";
}
export function usageReady(status: string): boolean {
  return status === "ready" || status === "partial";
}
