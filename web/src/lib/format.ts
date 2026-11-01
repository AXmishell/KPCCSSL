import { format, formatDistanceToNow } from "date-fns";
import { enUS, zhCN } from "date-fns/locale";

export function formatDateTime(unix?: number | null): string {
  if (!unix) return "—";
  return format(new Date(unix * 1000), "yyyy-MM-dd HH:mm");
}

export function formatDate(unix?: number | null): string {
  if (!unix) return "—";
  return format(new Date(unix * 1000), "yyyy-MM-dd");
}

export function fromNow(unix?: number | null, locale = "zh-CN"): string {
  if (!unix) return "—";
  return formatDistanceToNow(new Date(unix * 1000), { addSuffix: true, locale: locale.startsWith("zh") ? zhCN : enUS });
}

export function daysUntil(unix?: number | null): number | null {
  if (!unix) return null;
  return Math.ceil((unix * 1000 - Date.now()) / 86400000);
}

export function formatBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1);
  return `${(n / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

export function splitLines(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean)
    .filter((s, i, arr) => arr.indexOf(s) === i);
}
