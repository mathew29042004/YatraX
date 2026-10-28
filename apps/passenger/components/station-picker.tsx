"use client";

import type { Station } from "@yatrax/api-client";
import { cn, useYatrax } from "@yatrax/ui";
import { useEffect, useId, useRef, useState } from "react";

/**
 * Station autocomplete: type a code ("MAS") or part of a name ("chennai").
 * Keyboard: ↑/↓ to move, Enter to pick, Esc to close.
 */
export function StationPicker({
  label, value, onChange, placeholder,
}: { label: string; value: Station | null; onChange: (s: Station | null) => void; placeholder: string }) {
  const { client } = useYatrax();
  const id = useId();
  const [text, setText] = useState(value ? `${value.name} (${value.code})` : "");
  const [options, setOptions] = useState<Station[]>([]);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const typing = useRef(false);

  useEffect(() => {
    if (!typing.current) setText(value ? `${value.name} (${value.code})` : "");
  }, [value]);

  useEffect(() => {
    if (!typing.current) return;
    const q = text.trim();
    if (q.length < 2) {
      setOptions([]);
      return;
    }
    const timer = setTimeout(() => {
      client
        .stations(q, 8)
        .then((s) => {
          setOptions(s);
          setActive(0);
          setOpen(true);
        })
        .catch(() => setOptions([]));
    }, 150);
    return () => clearTimeout(timer);
  }, [text, client]);

  function pick(s: Station) {
    typing.current = false;
    onChange(s);
    setText(`${s.name} (${s.code})`);
    setOpen(false);
  }

  return (
    <div className="relative">
      <label htmlFor={id} className="mb-1.5 block text-xs font-medium uppercase tracking-wide text-muted">
        {label}
      </label>
      <input
        id={id}
        role="combobox"
        aria-expanded={open}
        aria-controls={`${id}-list`}
        aria-autocomplete="list"
        autoComplete="off"
        placeholder={placeholder}
        value={text}
        onChange={(e) => {
          typing.current = true;
          setText(e.target.value);
          if (value) onChange(null);
        }}
        onFocus={(e) => e.target.select()}
        onBlur={() => setTimeout(() => setOpen(false), 120)}
        onKeyDown={(e) => {
          if (!open || options.length === 0) return;
          if (e.key === "ArrowDown") {
            e.preventDefault();
            setActive((a) => Math.min(a + 1, options.length - 1));
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setActive((a) => Math.max(a - 1, 0));
          } else if (e.key === "Enter") {
            e.preventDefault();
            pick(options[active]!);
          } else if (e.key === "Escape") {
            setOpen(false);
          }
        }}
        className="h-12 w-full rounded-lg border border-line bg-surface px-3 text-base font-medium text-ink placeholder:font-normal placeholder:text-muted/70 focus:border-[var(--accent)] focus:outline-none focus:ring-2 focus:ring-[var(--accent)]/20"
      />
      {open && options.length > 0 && (
        <ul id={`${id}-list`} role="listbox" className="absolute z-30 mt-1 max-h-72 w-full overflow-auto rounded-lg border border-line bg-surface py-1 shadow-lg">
          {options.map((s, i) => (
            <li
              key={s.code}
              role="option"
              aria-selected={i === active}
              onMouseDown={(e) => {
                e.preventDefault();
                pick(s);
              }}
              onMouseEnter={() => setActive(i)}
              className={cn("flex cursor-pointer items-center justify-between gap-3 px-3 py-2 text-sm", i === active && "bg-canvas")}
            >
              <span>
                <span className="font-medium">{s.name}</span>
                <span className="block text-xs text-muted">
                  {s.state} · {s.zone}
                </span>
              </span>
              <span className="font-mono text-xs font-semibold text-muted">{s.code}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
