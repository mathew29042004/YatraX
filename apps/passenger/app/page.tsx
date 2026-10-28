"use client";

import Link from "next/link";
import { SearchForm, searchHref } from "@/components/search-form";
import { SiteHeader } from "@/components/site-header";
import { addDays, todayIST } from "@/components/format";

const POPULAR: [string, string, string, string][] = [
  ["MAS", "ERS", "Chennai", "Ernakulam"],
  ["NDLS", "HWH", "New Delhi", "Howrah"],
  ["CSMT", "MAS", "Mumbai", "Chennai"],
  ["SBC", "MAS", "Bengaluru", "Chennai"],
  ["NDLS", "PNBE", "New Delhi", "Patna"],
  ["MMCT", "ADI", "Mumbai", "Ahmedabad"],
];

export default function SearchHome() {
  const inAWeek = addDays(todayIST(), 7);
  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1}>
        <section className="bg-gradient-to-b from-brand-900 to-brand-700 px-4 pb-24 pt-12 text-white">
          <div className="mx-auto max-w-6xl">
            <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">Where are you travelling?</h1>
            <p className="mt-2 max-w-xl text-brand-100">
              Search every train on the network: timetables, fares by class and quota, and availability for the next 60 days.
            </p>
          </div>
        </section>
        <section className="mx-auto -mt-16 max-w-6xl px-4">
          <SearchForm />
        </section>
        <section className="mx-auto max-w-6xl px-4 py-10">
          <h2 className="mb-3 text-sm font-semibold text-muted">Popular routes</h2>
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {POPULAR.map(([f, t, fn, tn]) => (
              <Link
                key={f + t}
                href={searchHref({ from: f, to: t, date: inAWeek })}
                className="flex items-center justify-between rounded-xl border border-line bg-surface px-4 py-3 hover:border-[var(--accent)]"
              >
                <span className="font-medium">
                  {fn} → {tn}
                </span>
                <span className="font-mono text-xs text-muted">
                  {f}–{t}
                </span>
              </Link>
            ))}
          </div>
          <p className="mt-8 text-xs text-muted">
            All trains, people and availability on YatraX are synthetic. Availability comes from a demand model until the booking core arrives in Phase 2.
          </p>
        </section>
      </main>
    </>
  );
}
