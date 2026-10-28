"use client";

import { Logo, NotificationBell, SimClockBadge, TextSizeToggle, useYatrax } from "@yatrax/ui";
import Link from "next/link";

/** Header for public pages: search works without signing in. */
export function SiteHeader() {
  const { session, ready } = useYatrax();
  return (
    <>
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      <header className="sticky top-0 z-20 border-b border-line bg-surface/90 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-3 px-4">
          <Link href="/" className="flex items-center gap-2.5">
            <Logo />
            <span className="font-semibold">
              YatraX <span className="text-[var(--accent)]">Passenger</span>
            </span>
          </Link>
          <nav aria-label="Main" className="flex items-center gap-1 text-sm">
            <TextSizeToggle />
            <SimClockBadge />
            <Link href="/" className="rounded-md px-3 py-1.5 text-muted hover:bg-canvas hover:text-ink">
              Search
            </Link>
            <Link href="/tatkal" className="hidden rounded-md px-3 py-1.5 sm:block text-muted hover:bg-canvas hover:text-ink">
              Tatkal
            </Link>
            <Link href="/live" className="rounded-md px-3 py-1.5 text-muted hover:bg-canvas hover:text-ink">
              Live
            </Link>
            <Link href="/pnr" className="hidden rounded-md px-3 py-1.5 text-muted hover:bg-canvas hover:text-ink sm:block">
              PNR status
            </Link>
            {ready && session && (
              <Link href="/trips" className="rounded-md px-3 py-1.5 text-muted hover:bg-canvas hover:text-ink">
                My trips
              </Link>
            )}
            {ready && session && <NotificationBell />}
            {ready &&
              (session ? (
                <Link href="/account" className="rounded-md px-3 py-1.5 font-medium hover:bg-canvas">
                  {session.user.full_name.split(" ")[0]}
                </Link>
              ) : (
                <Link href="/login" className="rounded-md bg-[var(--accent)] px-3 py-1.5 font-medium text-white hover:brightness-110">
                  Sign in
                </Link>
              ))}
          </nav>
        </div>
      </header>
    </>
  );
}
