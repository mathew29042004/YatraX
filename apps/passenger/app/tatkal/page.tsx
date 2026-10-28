"use client";

import type { BotSignals, PassengerInput, RoomStatus, SearchTrain, WaitRoom } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, Label, Spinner, Stat, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { clockIST } from "@/components/format";
import { SiteHeader } from "@/components/site-header";

const AC_CLASSES = new Set(["1A", "2A", "3A", "3E", "CC", "EC"]);
const blank: PassengerInput = { name: "", age: 30, gender: "M", berth_pref: "" };

/** A stable, random id for this browser, so the detector can tell devices apart. */
function deviceId() {
  try {
    let id = localStorage.getItem("yatrax.device");
    if (!id) {
      id = crypto.randomUUID();
      localStorage.setItem("yatrax.device", id);
    }
    return id;
  } catch {
    return "ephemeral-" + Math.random().toString(36).slice(2);
  }
}

/** Collects the behavioural signals the bot detector looks at: form time, typing rhythm, pointer use. */
function useTelemetry() {
  const started = useRef<number | null>(null);
  const lastKey = useRef<number | null>(null);
  const intervals = useRef<number[]>([]);
  const moves = useRef(0);
  useEffect(() => {
    const onKey = () => {
      const now = performance.now();
      started.current ??= now;
      if (lastKey.current !== null && intervals.current.length < 60) intervals.current.push(Math.round(now - lastKey.current));
      lastKey.current = now;
    };
    const onMove = () => {
      started.current ??= performance.now();
      moves.current++;
    };
    window.addEventListener("keydown", onKey);
    window.addEventListener("pointermove", onMove, { passive: true });
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("pointermove", onMove);
    };
  }, []);
  return useCallback(
    (): BotSignals => ({
      device: deviceId(),
      form_ms: started.current === null ? 0 : Math.round(performance.now() - started.current),
      key_intervals: intervals.current.slice(-30),
      pointer_moves: moves.current,
    }),
    [],
  );
}

/** Finds a nonce so that sha256(prefix + nonce) starts with `bits` zero bits. */
async function solvePoW(prefix: string, bits: number, onProgress: (tries: number) => void) {
  const enc = new TextEncoder();
  for (let i = 0; ; i++) {
    const h = new Uint8Array(await crypto.subtle.digest("SHA-256", enc.encode(prefix + i)));
    let zeros = 0;
    for (const b of h) {
      if (b === 0) {
        zeros += 8;
        continue;
      }
      zeros += Math.clz32(b) - 24;
      break;
    }
    if (zeros >= bits) return String(i);
    if (i % 2000 === 0) onProgress(i);
  }
}

function roomLabel(r: WaitRoom) {
  return `${r.ac ? "AC" : "Non-AC"} Tatkal · journey ${r.journey_date}${r.kind === "drill" ? " · drill" : ""}`;
}

function stateTone(s: WaitRoom["state"]): "ok" | "saffron" | "neutral" {
  return s === "open" ? "ok" : s === "joining" ? "saffron" : "neutral";
}

export default function TatkalPage() {
  const { client, session, ready } = useYatrax();
  const [rooms, setRooms] = useState<WaitRoom[] | null>(null);
  const [room, setRoom] = useState<WaitRoom | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const load = () => client.waitrooms().then(setRooms).catch(() => setError("Could not load Tatkal rooms."));
    load();
    const t = setInterval(load, 10_000);
    return () => clearInterval(t);
  }, [client]);

  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1} className="mx-auto max-w-3xl space-y-5 px-4 py-6">
        <div>
          <h1 className="text-xl font-semibold">Tatkal waiting room</h1>
          <p className="mt-1 text-sm text-muted">
            Join before opening and fill in your journey. At opening everyone already waiting is shuffled into a fair random
            order; people who arrive later join the back of the queue. When your turn comes, your booking runs automatically.
          </p>
        </div>
        {error && <Alert>{error}</Alert>}
        {ready && !session && (
          <Alert tone="info">
            <Link href="/login?next=/tatkal" className="font-medium underline">
              Sign in
            </Link>{" "}
            to join a waiting room.
          </Alert>
        )}
        {room ? (
          <Room room={room} onLeave={() => setRoom(null)} />
        ) : !rooms ? (
          <Spinner label="Loading rooms" />
        ) : rooms.length === 0 ? (
          <Card>No Tatkal rooms are scheduled right now. AC Tatkal opens at 10:00 and non-AC at 11:00, a day before the journey.</Card>
        ) : (
          <div className="space-y-3">
            {rooms.map((r) => (
              <Card key={r.id} className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <div className="flex items-center gap-2 font-medium">
                    {roomLabel(r)} <Badge tone={stateTone(r.state)}>{r.state}</Badge>
                  </div>
                  <div className="text-sm text-muted">
                    Opens {clockIST(r.opens_at)} · joining from {clockIST(r.joins_open)} · {r.queue} waiting
                  </div>
                </div>
                <Button disabled={!session || r.state === "closed" || r.state === "upcoming"} onClick={() => setRoom(r)}>
                  {r.state === "upcoming" ? "Not open yet" : "Enter room"}
                </Button>
              </Card>
            ))}
          </div>
        )}
      </main>
    </>
  );
}

function Room({ room, onLeave }: { room: WaitRoom; onLeave: () => void }) {
  const { client } = useYatrax();
  const router = useRouter();
  const telemetry = useTelemetry();
  const [st, setSt] = useState<RoomStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [solving, setSolving] = useState<number | null>(null);
  const booking = useRef(false);

  // Join, then follow the position over a WebSocket (falling back to polling).
  useEffect(() => {
    let ws: WebSocket | null = null;
    let poll: ReturnType<typeof setInterval> | null = null;
    let alive = true;
    client
      .joinRoom(room.id, telemetry())
      .then((s) => {
        if (!alive) return;
        setSt(s);
        try {
          ws = new WebSocket(client.roomSocketURL(room.id));
          ws.onmessage = (e) => setSt(JSON.parse(e.data));
          ws.onerror = () => {
            poll ??= setInterval(() => client.roomStatus(room.id).then(setSt).catch(() => {}), 3000);
          };
        } catch {
          poll = setInterval(() => client.roomStatus(room.id).then(setSt).catch(() => {}), 3000);
        }
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not join the room."));
    return () => {
      alive = false;
      ws?.close();
      if (poll) clearInterval(poll);
    };
  }, [client, room.id, telemetry]);

  // When it's our turn and the draft is ready, book straight away.
  useEffect(() => {
    if (!st || booking.current) return;
    if (st.booking_id) {
      router.push(`/booking/${st.booking_id}`);
      return;
    }
    if (st.admitted && st.has_draft && st.action === "allow" && !st.outcome) {
      booking.current = true;
      client
        .bookFromRoom(room.id)
        .then((b) => router.push(`/booking/${b.id}`))
        .catch((e) => {
          booking.current = false;
          setError(e instanceof ApiError ? e.message : "The booking failed.");
        });
    }
  }, [st, client, room.id, router]);

  async function verify() {
    if (!st?.challenge) return;
    setSolving(0);
    try {
      const nonce = await solvePoW(st.challenge.prefix, st.challenge.bits, setSolving);
      setSt(await client.solveChallenge(room.id, nonce));
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Verification failed.");
    } finally {
      setSolving(null);
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="font-semibold">{roomLabel(room)}</h2>
        <Button variant="ghost" size="sm" onClick={onLeave}>
          Back to rooms
        </Button>
      </div>
      {error && <Alert>{error}</Alert>}
      {!st ? (
        <Spinner label="Joining" />
      ) : (
        <>
          <Card>
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <Stat label="State" value={st.state} />
              {st.state === "joining" ? (
                <Stat label="Opens in" value={`${st.opens_in_seconds}s`} sub="order is shuffled at opening" />
              ) : (
                <Stat label="People ahead" value={st.admitted ? "Your turn" : st.position.toLocaleString("en-IN")} />
              )}
              <Stat label="In queue" value={st.queue_size.toLocaleString("en-IN")} />
              <Stat label="Estimated wait" value={st.admitted ? "—" : `${st.eta_seconds}s`} />
            </div>
            {st.outcome && <p className="mt-3 text-sm font-medium">{st.outcome}</p>}
          </Card>
          {st.action === "block" && (
            <Alert>
              This account was blocked from this room because its activity looked automated. If that&apos;s wrong, contact
              support with your user id.
            </Alert>
          )}
          {st.action === "challenge" && (
            <Card>
              <CardTitle hint="Your browser does a short calculation to show it's a real device.">Quick verification</CardTitle>
              <Button onClick={verify} disabled={solving !== null}>
                {solving !== null ? `Verifying… (${solving.toLocaleString("en-IN")} tries)` : "Verify this device"}
              </Button>
            </Card>
          )}
          {st.admitted && booking.current && <Spinner label="Booking your Tatkal ticket" />}
          <Draft room={room} hasDraft={st.has_draft} telemetry={telemetry} onSaved={setSt} />
        </>
      )}
    </div>
  );
}

function Draft({
  room,
  hasDraft,
  telemetry,
  onSaved,
}: {
  room: WaitRoom;
  hasDraft: boolean;
  telemetry: () => BotSignals;
  onSaved: (s: RoomStatus) => void;
}) {
  const { client } = useYatrax();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [trains, setTrains] = useState<SearchTrain[] | null>(null);
  const [pick, setPick] = useState<{ train: SearchTrain; cls: string } | null>(null);
  const [pax, setPax] = useState<PassengerInput[]>([{ ...blank }]);
  const [premium, setPremium] = useState(false);
  const [msg, setMsg] = useState<{ tone: "danger" | "info"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  async function find(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    setPick(null);
    try {
      const r = await client.search({ from: from.trim().toUpperCase(), to: to.trim().toUpperCase(), date: room.journey_date });
      setTrains(r.trains.filter((t) => t.runs_on_date && t.classes.some((c) => AC_CLASSES.has(c.code) === room.ac)));
    } catch (err) {
      setMsg({ tone: "danger", text: err instanceof ApiError ? err.message : "Search failed." });
    }
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!pick) return;
    setBusy(true);
    setMsg(null);
    try {
      const s = await client.saveDraft(
        room.id,
        {
          train: pick.train.number,
          from: pick.train.from.code,
          to: pick.train.to.code,
          date: room.journey_date,
          class: pick.cls,
          quota: premium ? "PT" : "TQ",
          passengers: pax,
          together: false,
          only_confirmed: false,
        },
        telemetry(),
      );
      onSaved(s);
      setMsg({ tone: "info", text: "Saved. Keep this page open — your booking runs as soon as it's your turn." });
    } catch (err) {
      setMsg({ tone: "danger", text: err instanceof ApiError ? err.message : "Could not save." });
    } finally {
      setBusy(false);
    }
  }

  const setP = (i: number, p: Partial<PassengerInput>) => setPax((xs) => xs.map((x, j) => (j === i ? { ...x, ...p } : x)));

  return (
    <Card>
      <CardTitle hint={hasDraft ? <Badge tone="ok">ready</Badge> : "fill this in before opening"}>Your journey</CardTitle>
      <form onSubmit={find} className="flex flex-wrap items-end gap-2">
        <div>
          <Label htmlFor="tq-from">From</Label>
          <Input id="tq-from" value={from} onChange={(e) => setFrom(e.target.value)} placeholder="NDLS" className="w-28" />
        </div>
        <div>
          <Label htmlFor="tq-to">To</Label>
          <Input id="tq-to" value={to} onChange={(e) => setTo(e.target.value)} placeholder="HWH" className="w-28" />
        </div>
        <Button type="submit" variant="secondary">
          Find trains
        </Button>
      </form>
      {trains && trains.length === 0 && <p className="mt-3 text-sm text-muted">No trains with {room.ac ? "AC" : "non-AC"} classes on that date.</p>}
      {trains && trains.length > 0 && (
        <div className="mt-3 space-y-1.5">
          {trains.map((t) => (
            <div key={t.number} className="flex flex-wrap items-center gap-2 text-sm">
              <span className="w-56">
                <span className="font-mono font-semibold">{t.number}</span> {t.name}
              </span>
              <span className="text-muted">{clockIST(t.departure)}</span>
              {t.classes
                .filter((c) => AC_CLASSES.has(c.code) === room.ac)
                .map((c) => (
                  <Button
                    key={c.code}
                    size="sm"
                    variant={pick?.train.number === t.number && pick.cls === c.code ? "primary" : "secondary"}
                    onClick={() => setPick({ train: t, cls: c.code })}
                  >
                    {c.code}
                  </Button>
                ))}
            </div>
          ))}
        </div>
      )}
      {pick && (
        <form onSubmit={save} className="mt-4 space-y-3 border-t border-line pt-4">
          <div className="text-sm">
            <span className="font-mono font-semibold">{pick.train.number}</span> {pick.train.from.code} → {pick.train.to.code} · {pick.cls}
          </div>
          {pax.map((p, i) => (
            <div key={i} className="flex flex-wrap gap-2">
              <Input aria-label="Name" placeholder="Full name" value={p.name} onChange={(e) => setP(i, { name: e.target.value })} className="w-48" required />
              <Input aria-label="Age" type="number" min={1} max={120} value={p.age} onChange={(e) => setP(i, { age: Number(e.target.value) })} className="w-20" />
              <select
                aria-label="Gender"
                value={p.gender}
                onChange={(e) => setP(i, { gender: e.target.value as PassengerInput["gender"] })}
                className="rounded-md border border-line bg-surface px-2 text-sm"
              >
                <option value="M">Male</option>
                <option value="F">Female</option>
                <option value="O">Other</option>
              </select>
              {pax.length > 1 && (
                <Button type="button" variant="ghost" size="sm" onClick={() => setPax((xs) => xs.filter((_, j) => j !== i))}>
                  Remove
                </Button>
              )}
            </div>
          ))}
          <div className="flex flex-wrap items-center gap-3 text-sm">
            {pax.length < 4 && (
              <Button type="button" variant="secondary" size="sm" onClick={() => setPax((xs) => [...xs, { ...blank }])}>
                Add passenger
              </Button>
            )}
            <label className="flex items-center gap-1.5">
              <input type="checkbox" checked={premium} onChange={(e) => setPremium(e.target.checked)} /> Premium Tatkal (dynamic fare)
            </label>
          </div>
          <Button type="submit" disabled={busy}>
            {hasDraft ? "Update booking details" : "Save booking details"}
          </Button>
        </form>
      )}
      {msg && (
        <div className="mt-3">
          <Alert tone={msg.tone}>{msg.text}</Alert>
        </div>
      )}
    </Card>
  );
}
