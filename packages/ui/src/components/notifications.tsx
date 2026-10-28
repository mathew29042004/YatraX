"use client";

import type { AppNotification } from "@yatrax/api-client";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { cn } from "./primitives";
import { useYatrax } from "./session";

function ago(iso: string) {
  const s = Math.max(1, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

/** Header bell with in-app notifications, polled every 15 seconds while signed in. */
export function NotificationBell() {
  const { client, session } = useYatrax();
  const [items, setItems] = useState<AppNotification[]>([]);
  const [unread, setUnread] = useState(0);
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!session) return;
    const load = () =>
      client
        .notifications()
        .then((n) => {
          setItems(n.items);
          setUnread(n.unread);
        })
        .catch(() => undefined);
    load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, [client, session]);

  useEffect(() => {
    const close = (e: MouseEvent) => box.current && !box.current.contains(e.target as Node) && setOpen(false);
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, []);

  if (!session) return null;
  return (
    <div className="relative" ref={box}>
      <button
        type="button"
        aria-label={`Notifications${unread ? `, ${unread} unread` : ""}`}
        onClick={() => {
          setOpen(!open);
          if (!open && unread) {
            client.readNotifications().catch(() => undefined);
            setUnread(0);
          }
        }}
        className="relative flex h-9 w-9 items-center justify-center rounded-md text-muted hover:bg-canvas hover:text-ink"
      >
        <svg viewBox="0 0 24 24" className="h-5 w-5" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
          <path d="M6 8a6 6 0 1 1 12 0c0 7 3 9 3 9H3s3-2 3-9M10.3 21a1.94 1.94 0 0 0 3.4 0" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        {unread > 0 && (
          <span className="absolute -right-0.5 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-danger px-1 text-[10px] font-semibold text-white">
            {unread > 9 ? "9+" : unread}
          </span>
        )}
      </button>
      {open && (
        <div className="absolute right-0 z-30 mt-2 w-80 overflow-hidden rounded-xl border border-line bg-surface shadow-xl">
          <div className="border-b border-line px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted">Notifications</div>
          <ul className="max-h-96 overflow-auto">
            {items.length === 0 && <li className="px-3 py-6 text-center text-sm text-muted">Nothing yet.</li>}
            {items.map((n) => (
              <li key={n.id} className={cn("border-b border-line last:border-0", !n.read_at && "bg-[var(--accent)]/5")}>
                <Link href={n.link || "#"} onClick={() => setOpen(false)} className="block px-3 py-2.5 hover:bg-canvas">
                  <div className="text-sm font-medium leading-snug">{n.title}</div>
                  <div className="mt-0.5 text-xs text-muted">{n.body}</div>
                  <div className="mt-1 text-[11px] text-muted">{ago(n.created_at)}</div>
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
