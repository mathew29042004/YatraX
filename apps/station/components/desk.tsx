"use client";

import type { Announcement, CoachPosition, StationAnnouncements, StationFacilities, StationPlatforms } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, Label, Spinner, Stat, cn, hhmm, useYatrax } from "@yatrax/ui";
import { useEffect, useRef, useState } from "react";
import { langName, useAnnouncer } from "./announcer";

function usePoll<T>(load: () => Promise<T>, every: number, deps: unknown[]) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let alive = true;
    const run = () =>
      load()
        .then((d) => alive && (setData(d), setError(null)))
        .catch((e) => alive && setError(e instanceof ApiError ? e.message : "Unavailable"));
    run();
    const t = setInterval(run, every);
    return () => {
      alive = false;
      clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);
  return { data, error, refresh: () => setTick((n) => n + 1) };
}

const kindLabel: Record<Announcement["kind"], string> = {
  arriving: "Arriving", on_platform: "On platform", departing: "Departing", delayed: "Running late", platform_changed: "Platform change",
};

/** Announcements due now; with auto-announce on, each new one plays once (chime, then every language). */
export function Announcements({ code, compact = false }: { code: string; compact?: boolean }) {
  const { client } = useYatrax();
  const { data, error } = usePoll<StationAnnouncements>(() => client.stationAnnouncements(code), 5000, [client, code]);
  const ann = useAnnouncer();
  const played = useRef(new Set<string>());
  const primed = useRef(false);

  // Auto-announce: everything already due when switched on is marked heard; new ones play.
  useEffect(() => {
    if (!data) return;
    if (!ann.enabled || !primed.current) {
      data.items.forEach((a) => played.current.add(a.id));
      primed.current = ann.enabled;
      return;
    }
    for (const a of data.items) {
      if (!played.current.has(a.id)) {
        played.current.add(a.id);
        ann.announce(a);
      }
    }
  }, [data, ann.enabled]); // eslint-disable-line react-hooks/exhaustive-deps

  if (error) return <Alert>{error}</Alert>;
  if (!data) return <Spinner label="Loading announcements" />;
  return (
    <Card>
      <CardTitle hint={data.languages.map((l) => langName[l] ?? l).join(" · ")}>Announcements</CardTitle>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        {ann.enabled ? (
          <Button size="sm" variant="secondary" onClick={ann.disable}>
            Stop auto-announce
          </Button>
        ) : (
          <Button size="sm" onClick={ann.enable} disabled={!ann.supported}>
            Start auto-announce
          </Button>
        )}
        {data.languages.map((l) => {
          const line = data.items[0]?.lines.find((x) => x.lang === l);
          const ok = line ? ann.hasVoice(line.bcp47) : true;
          return (
            <Badge key={l} tone={ok ? "ok" : "neutral"}>
              {langName[l] ?? l}: {ok ? "voice ready" : "no voice on this device (text only)"}
            </Badge>
          );
        })}
        {data.missing_regional && <Badge tone="saffron">{langName[data.missing_regional] ?? data.missing_regional} template not written yet</Badge>}
      </div>
      {data.items.length === 0 && <p className="text-sm text-muted">Nothing to announce right now.</p>}
      <ul className="space-y-3">
        {data.items.slice(0, compact ? 4 : 12).map((a) => (
          <li key={a.id} className={cn("rounded-lg border border-line p-3", ann.speaking?.id === a.id && "border-[var(--accent)] bg-[var(--accent)]/5")}>
            <div className="mb-1 flex flex-wrap items-center gap-2">
              <span className="font-mono font-semibold">{a.train}</span>
              <span className="text-sm">{a.name}</span>
              <Badge tone={a.kind === "delayed" ? "danger" : a.kind === "platform_changed" ? "saffron" : "brand"}>{kindLabel[a.kind]}</Badge>
              <span className="ml-auto text-xs text-muted">due {hhmm(a.at)}</span>
              <Button size="sm" variant="ghost" onClick={() => { if (!ann.enabled) ann.enable(); ann.announce(a); }}>
                ▶ Play
              </Button>
            </div>
            {a.lines.map((l) => (
              <p key={l.lang} lang={l.bcp47} className={cn("text-sm", ann.speaking?.id === a.id && ann.speaking.lang === l.lang ? "font-medium text-ink" : "text-muted")}>
                <span className="mr-1 text-xs uppercase text-muted">{l.lang}</span>
                {l.text}
              </p>
            ))}
          </li>
        ))}
      </ul>
    </Card>
  );
}

const levelTone = { low: "bg-green-500", busy: "bg-saffron-500", crowded: "bg-danger" } as const;

/** Platform occupancy for the next hours: clashes, one-click fixes, reassignment, crowd. */
export function Platforms({ code, onPick }: { code: string; onPick: (train: string, date: string) => void }) {
  const { client } = useYatrax();
  const { data, error, refresh } = usePoll<StationPlatforms>(() => client.stationPlatforms(code, 4), 15000, [client, code]);
  const [msg, setMsg] = useState<string | null>(null);
  if (error) return <Alert>{error}</Alert>;
  if (!data) return <Spinner label="Loading platforms" />;
  const now = new Date(data.sim_now).getTime();
  const span = 4 * 3600_000;
  const lo = now - 3600_000 / 2;
  const pct = (iso: string) => Math.max(0, Math.min(100, ((new Date(iso).getTime() - lo) / span) * 100));
  const move = async (train: string, date: string, pf: number) => {
    setMsg(null);
    try {
      await client.assignPlatform(code, train, date, pf);
      setMsg(pf ? `${train} moved to platform ${pf}. Boards, announcements and passengers are updated.` : `${train} back on its timetabled platform.`);
      refresh();
    } catch (e) {
      setMsg(e instanceof ApiError ? e.message : "Could not reassign");
    }
  };
  const slotOf = (train: string) => data.platforms.flatMap((p) => p.slots ?? []).find((s) => s.train === train);
  return (
    <Card>
      <CardTitle hint={`${data.platforms.length} platforms · next 4 hours`}>Platforms</CardTitle>
      {data.clashes.length > 0 && (
        <div className="mb-3 space-y-2">
          {data.clashes.map((c) => {
            const b = slotOf(c.b);
            return (
              <Alert key={c.platform + c.a + c.b}>
                Clash on platform {c.platform}: {c.a} and {c.b} overlap by {c.overlap_min} min.{" "}
                {c.suggest > 0 && b && (
                  <button type="button" className="font-semibold underline" onClick={() => move(c.b, b.origin_date, c.suggest)}>
                    Move {c.b} to platform {c.suggest}
                  </button>
                )}
              </Alert>
            );
          })}
        </div>
      )}
      {msg && (
        <div className="mb-3">
          <Alert tone="info">{msg}</Alert>
        </div>
      )}
      <div className="relative space-y-1">
        <div className="absolute bottom-0 top-0 w-px bg-danger/70" style={{ left: `calc(3rem + (100% - 3rem) * ${pct(data.sim_now) / 100})` }} title="now" />
        {data.platforms.map((p) => (
          <div key={p.no} className="flex items-center gap-2">
            <span className="w-10 shrink-0 text-right font-mono text-xs text-muted">PF {p.no}</span>
            <div className="relative h-7 flex-1 rounded bg-canvas">
              {(p.slots ?? []).map((s) => (
                <button
                  type="button"
                  key={s.train + s.origin_date}
                  onClick={() => onPick(s.train, s.origin_date)}
                  aria-label={`${s.train} ${s.name}, ${hhmm(s.start)} to ${hhmm(s.end)}${s.delay_min ? `, ${s.delay_min} min late` : ""}`}
                  title={`${s.train} ${s.name} · ${hhmm(s.start)}–${hhmm(s.end)} · ${s.estimated ? "~" : ""}${s.boarding} boarding, ${s.estimated ? "~" : ""}${s.alighting} alighting, ~${s.general} unreserved${s.delay_min ? ` · ${s.delay_min} min late` : ""}`}
                  className={cn("absolute top-0.5 flex h-6 items-center overflow-hidden rounded px-1 font-mono text-[10px] text-white",
                    s.assigned ? "bg-[var(--accent)]" : s.delay_min >= 15 ? "bg-warn" : "bg-slate-500",
                    data.clashes.some((c) => c.platform === p.no && (c.a === s.train || c.b === s.train)) && "ring-2 ring-danger")}
                  style={{ left: `${pct(s.start)}%`, width: `${Math.max(1.5, pct(s.end) - pct(s.start))}%` }}
                >
                  {s.train}
                </button>
              ))}
            </div>
          </div>
        ))}
      </div>
      <div className="mt-1 flex justify-between pl-12 text-[10px] text-muted">
        {[0, 1, 2, 3, 4].map((h) => (
          <span key={h}>{hhmm(new Date(lo + h * 3600_000))}</span>
        ))}
      </div>
      <h3 className="mb-1 mt-4 text-xs font-semibold text-muted">Crowd on the platforms (boarding, alighting and unreserved)</h3>
      <div className="flex items-end gap-2">
        {data.hourly.map((h) => (
          <div key={h.hour} className="flex-1 text-center">
            <div className="flex h-16 items-end justify-center">
              <div className={cn("w-8 rounded-t", levelTone[h.level])} style={{ height: `${Math.min(100, (h.count / 5000) * 100)}%` }} />
            </div>
            <div className="text-[10px] text-muted">
              {hhmm(h.hour)} · {h.count.toLocaleString("en-IN")}
            </div>
          </div>
        ))}
      </div>
    </Card>
  );
}

/** Reassign one train, and show where its coaches stop. */
export function TrainAtStation({ code, train, date, platforms }: { code: string; train: string; date: string; platforms: number }) {
  const { client } = useYatrax();
  const [cp, setCp] = useState<CoachPosition | null>(null);
  const [pf, setPf] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  const load = () => client.coachPosition(code, train, date).then(setCp).catch(() => setCp(null));
  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, code, train, date]);
  if (!cp) return <Spinner label="Loading train" />;
  return (
    <Card>
      <CardTitle hint={`left origin ${cp.origin_date}`}>
        {cp.train} {cp.name} · platform {cp.platform}
      </CardTitle>
      <form
        className="mb-4 flex flex-wrap items-end gap-2"
        onSubmit={async (e) => {
          e.preventDefault();
          try {
            await client.assignPlatform(code, train, date, Number(pf));
            setMsg(Number(pf) ? `Moved to platform ${pf}` : "Back on the timetabled platform");
            load();
          } catch (err) {
            setMsg(err instanceof ApiError ? err.message : "Could not reassign");
          }
        }}
      >
        <div>
          <Label htmlFor="pf">Platform</Label>
          <select id="pf" value={pf} onChange={(e) => setPf(e.target.value)} className="h-9 rounded-md border border-line bg-surface px-2 text-sm">
            <option value="">Choose…</option>
            <option value="0">Timetabled platform</option>
            {Array.from({ length: platforms }, (_, i) => i + 1).map((n) => (
              <option key={n} value={n}>
                Platform {n}
              </option>
            ))}
          </select>
        </div>
        <Button type="submit" size="sm" disabled={pf === ""}>
          Assign
        </Button>
        {msg && <span className="text-sm text-muted">{msg}</span>}
      </form>
      <h3 className="mb-1 text-xs font-semibold text-muted">Coach position · engine end towards {cp.engine_toward}</h3>
      <div className="overflow-x-auto">
        <div className="flex min-w-max gap-0.5 rounded-lg bg-board p-2">
          {Array.from({ length: cp.markers }, (_, i) => i + 1).map((m) => {
            const s = cp.spots.find((x) => x.marker === m);
            return (
              <div key={m} className="flex w-10 flex-col items-center font-mono">
                <span className="text-[9px] text-white/40">{m}</span>
                <span className={cn("mt-0.5 w-full rounded py-1 text-center text-[11px] font-bold", s ? (s.coach === "ENG" ? "bg-red-500 text-white" : "bg-board-amber text-black") : "text-white/20")}>
                  {s?.coach ?? "·"}
                </span>
              </div>
            );
          })}
        </div>
      </div>
    </Card>
  );
}

export function Facilities({ code }: { code: string }) {
  const { client } = useYatrax();
  const { data, error, refresh } = usePoll<StationFacilities>(() => client.stationFacilities(code), 30000, [client, code]);
  const [item, setItem] = useState("");
  const [where, setWhere] = useState("");
  const [who, setWho] = useState("");
  const [pnr, setPnr] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  if (error) return <Alert>{error}</Alert>;
  if (!data) return <Spinner label="Loading facilities" />;
  const act = async (f: () => Promise<unknown>, ok: string) => {
    setMsg(null);
    try {
      await f();
      setMsg(ok);
      refresh();
    } catch (e) {
      setMsg(e instanceof ApiError ? e.message : "Failed");
    }
  };
  return (
    <Card>
      <CardTitle>Facilities</CardTitle>
      <div className="mb-4 grid grid-cols-2 gap-3">
        <Stat label="Retiring rooms free" value={`${data.retiring_rooms.free} / ${data.retiring_rooms.total}`} />
        <Stat label="Cloak room lockers free" value={`${data.cloak_room.free} / ${data.cloak_room.lockers}`} />
      </div>
      {msg && <p className="mb-2 text-sm text-muted">{msg}</p>}
      <div className="grid gap-5 lg:grid-cols-2">
        <div>
          <h3 className="mb-2 text-sm font-semibold">Lost and found</h3>
          <form className="mb-2 flex flex-wrap gap-2" onSubmit={(e) => { e.preventDefault(); act(() => client.addLostItem(code, item, "", where), `Logged: ${item}`); setItem(""); setWhere(""); }}>
            <Input aria-label="Item" placeholder="Black backpack" value={item} onChange={(e) => setItem(e.target.value)} className="w-40" />
            <Input aria-label="Found at" placeholder="PF 3 / 12623 S4" value={where} onChange={(e) => setWhere(e.target.value)} className="w-40" />
            <Button size="sm" type="submit" disabled={!item.trim()}>
              Log
            </Button>
          </form>
          <ul className="space-y-1 text-sm">
            {data.lost_found.map((x) => (
              <li key={x.id} className="flex items-center justify-between gap-2 border-t border-line py-1">
                <span>
                  #{x.id} {x.item} <span className="text-muted">{x.found_at}</span>
                </span>
                {x.status === "held" ? (
                  <Button size="sm" variant="ghost" onClick={() => { const to = window.prompt("Collected by (name and ID)?"); if (to) act(() => client.returnLostItem(code, x.id, to), `#${x.id} returned`); }}>
                    Return
                  </Button>
                ) : (
                  <Badge tone="ok">returned to {x.returned_to}</Badge>
                )}
              </li>
            ))}
          </ul>
        </div>
        <div>
          <h3 className="mb-2 text-sm font-semibold">Wheelchair desk</h3>
          <form className="mb-2 flex flex-wrap gap-2" onSubmit={(e) => { e.preventDefault(); act(() => client.addWheelchair(code, who, pnr, "", ""), `Request for ${who} logged`); setWho(""); setPnr(""); }}>
            <Input aria-label="Passenger" placeholder="Passenger name" value={who} onChange={(e) => setWho(e.target.value)} className="w-40" />
            <Input aria-label="PNR" placeholder="PNR" value={pnr} onChange={(e) => setPnr(e.target.value)} className="w-32" />
            <Button size="sm" type="submit" disabled={!who.trim()}>
              Request
            </Button>
          </form>
          <ul className="space-y-1 text-sm">
            {data.wheelchair.map((x) => (
              <li key={x.id} className="flex items-center justify-between gap-2 border-t border-line py-1">
                <span>
                  {x.passenger} {x.pnr && <span className="font-mono text-muted">{x.pnr}</span>}
                </span>
                {x.status === "requested" && (
                  <Button size="sm" variant="ghost" onClick={() => { const a = window.prompt("Attendant's name?"); if (a) act(() => client.updateWheelchair(code, x.id, "assigned", a), "Attendant assigned"); }}>
                    Assign
                  </Button>
                )}
                {x.status === "assigned" && (
                  <Button size="sm" variant="ghost" onClick={() => act(() => client.updateWheelchair(code, x.id, "done"), "Done")}>
                    {x.attendant} · done?
                  </Button>
                )}
                {x.status === "done" && <Badge tone="ok">done</Badge>}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </Card>
  );
}
