"use client";

import { useEffect, useState } from "react";

const SIZES = [
  { label: "A", title: "Normal text", pct: 100 },
  { label: "A+", title: "Large text", pct: 112.5 },
  { label: "A++", title: "Larger text", pct: 125 },
];
const KEY = "yatrax:text-size";

/**
 * Text size for people who need it bigger. Everything is sized in rem, so scaling the root
 * font scales the whole app; the choice is remembered on this device.
 */
export function TextSizeToggle() {
  const [i, setI] = useState(0);
  useEffect(() => {
    let saved = 0;
    try {
      saved = Number(localStorage.getItem(KEY) ?? 0) || 0;
    } catch {
      // storage blocked: stay at normal size
    }
    setI(Math.min(saved, SIZES.length - 1));
  }, []);
  useEffect(() => {
    document.documentElement.style.fontSize = `${SIZES[i]!.pct}%`;
  }, [i]);
  const pick = (n: number) => {
    setI(n);
    try {
      localStorage.setItem(KEY, String(n));
    } catch {
      // not remembered, but still applied
    }
  };
  return (
    <div role="group" aria-label="Text size" className="hidden items-center rounded-md border border-line sm:flex">
      {SIZES.map((s, n) => (
        <button
          key={s.label}
          type="button"
          title={s.title}
          aria-label={s.title}
          aria-pressed={i === n}
          onClick={() => pick(n)}
          className={`px-1.5 py-0.5 text-xs ${i === n ? "bg-canvas font-semibold text-ink" : "text-muted hover:text-ink"}`}
        >
          {s.label}
        </button>
      ))}
    </div>
  );
}
