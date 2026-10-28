import type { TTEPassenger } from "@yatrax/api-client";
import { CameraView, useCameraPermissions } from "expo-camera";
import { useMemo, useRef, useState } from "react";
import { FlatList, Pressable, ScrollView, Switch, Text, View } from "react-native";
import { berthFree, canNoShow, fareFor, nextRAC, place, vacantBerths } from "../logic";
import { useStore } from "../store";
import { verifyTicket, type TicketPayload } from "../ticket";
import { Button, Card, Chips, Field, H, Muted, Pill, color, s } from "../ui";

type Tab = "chart" | "scan" | "rac" | "fine" | "more";

export function Run() {
  const { bundle, view, online, forceOffline, setForceOffline, outbox, sync, syncing, error, stop, setStop, close } = useStore();
  const [tab, setTab] = useState<Tab>("chart");
  if (!bundle || !view) return null;
  const here = bundle.stops[stop];
  return (
    <View style={{ flex: 1, backgroundColor: color.canvas }}>
      <View style={{ backgroundColor: color.surface, paddingTop: 44, paddingHorizontal: 14, paddingBottom: 10, borderBottomWidth: 1, borderColor: color.line }}>
        <View style={s.row}>
          <Pressable onPress={close} accessibilityRole="button" accessibilityLabel="Back to duties">
            <Text style={{ color: color.accent, fontSize: 15 }}>‹ Duties</Text>
          </Pressable>
          <Text style={{ flex: 1, fontWeight: "700", fontSize: 16, color: color.ink }} numberOfLines={1}>
            {bundle.train} {bundle.name}
          </Text>
          <Pill text={online ? "Online" : "Offline"} tone={online ? "ok" : "warn"} />
        </View>
        <Muted>
          Left origin {bundle.origin_date} · {bundle.chart ? `chart ${bundle.chart.version}` : "live list (no chart yet)"} · {bundle.passengers.length} passengers
        </Muted>
        <View style={[s.row, { marginTop: 8 }]}>
          <Text style={{ fontSize: 13, color: color.ink }}>Work offline</Text>
          <Switch value={forceOffline} onValueChange={setForceOffline} accessibilityLabel="Work offline" />
          <View style={{ flex: 1 }} />
          <Text style={{ fontSize: 13, color: outbox.length ? color.warn : color.muted }}>
            {outbox.length ? `${outbox.length} to sync` : "All synced"}
          </Text>
          <Button title="Sync" small kind="secondary" onPress={() => sync()} busy={syncing} disabled={!online || syncing} />
        </View>
        {error && <Text style={{ color: color.danger, fontSize: 12, marginTop: 4 }}>{error}</Text>}
        <View style={[s.row, { marginTop: 8 }]}>
          <Button title="‹" small kind="secondary" onPress={() => setStop(Math.max(0, stop - 1))} disabled={stop === 0} />
          <View style={{ flex: 1, alignItems: "center" }}>
            <Text style={{ fontWeight: "600", color: color.ink }}>
              At / past {here?.name} ({here?.code})
            </Text>
            <Muted>
              stop {stop + 1} of {bundle.stops.length}
            </Muted>
          </View>
          <Button title="›" small kind="secondary" onPress={() => setStop(Math.min(bundle.stops.length - 1, stop + 1))} disabled={stop === bundle.stops.length - 1} />
        </View>
      </View>
      <View style={{ flex: 1 }}>
        {tab === "chart" && <ChartTab />}
        {tab === "scan" && <ScanTab />}
        {tab === "rac" && <RACTab />}
        {tab === "fine" && <FineTab />}
        {tab === "more" && <MoreTab />}
      </View>
      <View style={{ flexDirection: "row", backgroundColor: color.surface, borderTopWidth: 1, borderColor: color.line }}>
        {(
          [
            ["chart", "Chart"],
            ["scan", "Scan"],
            ["rac", "RAC"],
            ["fine", "Fine"],
            ["more", "More"],
          ] as Array<[Tab, string]>
        ).map(([k, label]) => (
          <Pressable key={k} onPress={() => setTab(k)} style={{ flex: 1, paddingVertical: 12, alignItems: "center" }} accessibilityRole="tab" accessibilityLabel={label}>
            <Text style={{ color: tab === k ? color.accent : color.muted, fontWeight: tab === k ? "700" : "500" }}>{label}</Text>
          </Pressable>
        ))}
      </View>
    </View>
  );
}

// ── Chart ──────────────────────────────────────────────────────────────────

function ChartTab() {
  const { bundle, view, stop } = useStore();
  const classes = useMemo(() => [...new Set(bundle!.coaches.map((c) => c.class))], [bundle]);
  const [cls, setCls] = useState(classes.includes("SL") ? "SL" : classes[0]!);
  const coaches = bundle!.coaches.filter((c) => c.class === cls);
  const [coach, setCoach] = useState(coaches[0]?.coach ?? "");
  const [picked, setPicked] = useState<number | null>(null);
  const c = coaches.find((x) => x.coach === coach) ?? coaches[0];
  if (!c) return null;

  // Who is on each berth at the current stop.
  const onBerth = (berth: number) =>
    view!.passengers.filter((p) => p.coach === c.coach && p.berth === berth && p.from_idx <= stop && stop < p.to_idx && p.status !== "WL");
  const tone = (berth: number) => {
    const ps = onBerth(berth);
    if (ps.length === 0) return { bg: color.canvas, border: color.line, fg: color.muted };
    if (ps.some((p) => p.status === "NOSHOW")) return { bg: color.dangerSoft, border: color.danger, fg: color.danger };
    if (ps.some((p) => p.status === "RAC")) return { bg: color.warnSoft, border: color.warn, fg: color.warn };
    if (ps.every((p) => p.boarded_at)) return { bg: color.okSoft, border: color.ok, fg: color.ok };
    return { bg: color.surface, border: color.ink, fg: color.ink };
  };

  return (
    <ScrollView contentContainerStyle={{ padding: 14 }}>
      <Chips items={classes.map((k) => ({ key: k, label: k }))} value={cls} onChange={(k) => { setCls(k); setCoach(bundle!.coaches.find((x) => x.class === k)?.coach ?? ""); setPicked(null); }} />
      <Chips items={coaches.map((x) => ({ key: x.coach, label: x.coach }))} value={c.coach} onChange={(k) => { setCoach(k); setPicked(null); }} label={(k) => `Coach ${k}`} />
      <View style={[s.row, { flexWrap: "wrap", marginBottom: 8 }]}>
        <Pill text="boarded" tone="ok" />
        <Pill text="expected" />
        <Pill text="RAC" tone="warn" />
        <Pill text="no-show" tone="danger" />
        <Muted>grey = vacant here</Muted>
      </View>
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
        {c.berths.map((b) => {
          const t = tone(b.berth);
          return (
            <Pressable key={b.berth} onPress={() => setPicked(b.berth)} accessibilityRole="button" accessibilityLabel={`Berth ${b.berth} ${b.type}`}
              style={{ width: 52, height: 48, borderRadius: 8, borderWidth: picked === b.berth ? 2.5 : 1, borderColor: picked === b.berth ? color.accent : t.border, backgroundColor: t.bg, alignItems: "center", justifyContent: "center" }}>
              <Text style={{ fontWeight: "700", color: t.fg }}>{b.berth}</Text>
              <Text style={{ fontSize: 10, color: t.fg }}>{b.type}</Text>
            </Pressable>
          );
        })}
      </View>
      {picked !== null && <BerthPanel coach={c.coach} berth={picked} cls={cls} type={c.berths.find((b) => b.berth === picked)?.type ?? ""} />}
    </ScrollView>
  );
}

function PassengerRow({ p, actions }: { p: TTEPassenger; actions?: React.ReactNode }) {
  const { bundle } = useStore();
  return (
    <View style={{ paddingVertical: 8, borderTopWidth: 1, borderColor: color.line }}>
      <View style={s.row}>
        <Text style={{ flex: 1, fontWeight: "600", color: color.ink }}>
          {p.name} <Text style={{ fontWeight: "400", color: color.muted }}>{p.age}{p.gender}</Text>
        </Text>
        <Pill
          text={p.status === "RAC" ? `RAC ${p.rac_no ?? ""}` : p.status === "NOSHOW" ? "No-show" : p.status === "WL" ? `WL ${p.wl_no ?? ""}` : p.boarded_at ? "Boarded" : "CNF"}
          tone={p.status === "NOSHOW" ? "danger" : p.status === "RAC" ? "warn" : p.boarded_at ? "ok" : "neutral"}
        />
      </View>
      <Muted>
        PNR {p.pnr} · {bundle!.stops[p.from_idx]?.code} → {bundle!.stops[p.to_idx]?.code} · {p.quota} {place(p) && `· ${place(p)}`}
        {p.source === "background" ? " · simulated" : ""}
      </Muted>
      {actions && <View style={[s.row, { marginTop: 6, flexWrap: "wrap" }]}>{actions}</View>}
    </View>
  );
}

function BerthPanel({ coach, berth, cls, type }: { coach: string; berth: number; cls: string; type: string }) {
  const { bundle, view, stop, record } = useStore();
  const [msg, setMsg] = useState<string | null>(null);
  const all = view!.passengers.filter((p) => p.coach === coach && p.berth === berth && p.status !== "WL").sort((a, b) => a.from_idx - b.from_idx);
  const freeNow = berthFree(view!, bundle!, coach, berth, stop, bundle!.stops.length - 1);
  const here = bundle!.stops[stop]!.code;
  return (
    <Card style={{ marginTop: 12 }}>
      <H>
        {coach}/{berth} {type}
      </H>
      {all.length === 0 && <Muted>Nobody booked on this berth.</Muted>}
      {all.map((p) => {
        const check = canNoShow(bundle!, p, stop);
        const heir = check.ok ? nextRAC(view!, bundle!, cls, coach, berth, stop, p.id) : undefined;
        return (
          <PassengerRow
            key={p.id}
            p={p}
            actions={
              p.status === "CNF" && !p.boarded_at ? (
                <>
                  <Button title="Boarded" small onPress={() => { record("board", { passenger_id: p.id, station: here }); setMsg(`${p.name} boarded`); }} />
                  <Button
                    title={heir ? `No-show → RAC ${heir.rac_no} ${heir.name}` : "No-show"}
                    small
                    kind="danger"
                    disabled={!check.ok}
                    onPress={() => {
                      record("no_show", { passenger_id: p.id, station: here, assign_to: heir?.id ?? "" });
                      setMsg(heir ? `${p.name} marked no-show; ${heir.name} (RAC ${heir.rac_no}) moved to ${coach}/${berth}` : `${p.name} marked no-show; berth vacant`);
                    }}
                  />
                </>
              ) : undefined
            }
          />
        );
      })}
      {all.some((p) => p.status === "CNF" && !p.boarded_at && !canNoShow(bundle!, p, stop).ok) && (
        <Muted>No-show opens once the train has passed 2 stations beyond the boarding point.</Muted>
      )}
      {freeNow && <AllotRAC coach={coach} berth={berth} cls={cls} onDone={setMsg} />}
      {msg && <Text style={{ color: color.accent, marginTop: 8 }}>{msg} — saved on this phone, syncs when online.</Text>}
    </Card>
  );
}

function AllotRAC({ coach, berth, cls, onDone }: { coach: string; berth: number; cls: string; onDone: (m: string) => void }) {
  const { bundle, view, stop, record } = useStore();
  const here = bundle!.stops[stop]!.code;
  const racs = view!.passengers.filter((p) => p.status === "RAC" && p.class === cls && p.to_idx > stop).sort((a, b) => (a.rac_no ?? 0) - (b.rac_no ?? 0));
  if (racs.length === 0) return <Muted>Vacant from here. No RAC passengers waiting in {cls}.</Muted>;
  return (
    <View style={{ marginTop: 8 }}>
      <Muted>Vacant from here — give it to a RAC passenger:</Muted>
      {racs.slice(0, 4).map((q) => (
        <PassengerRow key={q.id} p={q} actions={
          <Button title={`Move to ${coach}/${berth}`} small kind="secondary" onPress={() => { record("assign_berth", { passenger_id: q.id, coach, berth, station: here }); onDone(`${q.name} moved to ${coach}/${berth}`); }} />
        } />
      ))}
    </View>
  );
}

// ── Scan ───────────────────────────────────────────────────────────────────

function ScanTab() {
  const { bundle, view, stop, record } = useStore();
  const [perm, requestPerm] = useCameraPermissions();
  const [camera, setCamera] = useState(false);
  const [paste, setPaste] = useState("");
  const [result, setResult] = useState<{ ok: boolean; why?: string; ticket?: TicketPayload } | null>(null);
  const last = useRef("");
  const check = (token: string) => {
    if (!token || token === last.current) return;
    last.current = token;
    const v = verifyTicket(token, bundle!.public_key);
    setResult(v.ok ? { ok: true, ticket: v.ticket } : { ok: false, why: v.why });
    setCamera(false);
  };
  const t = result?.ticket;
  const wrongTrain = t && t.t !== bundle!.train;
  const pax = t ? view!.passengers.filter((p) => p.pnr === t.p) : [];
  const here = bundle!.stops[stop]!.code;
  return (
    <ScrollView contentContainerStyle={{ padding: 14 }}>
      <Card>
        <H>Check a ticket</H>
        <Muted>Signatures are checked on the phone with the railway&apos;s public key: no network needed.</Muted>
        {camera && perm?.granted ? (
          <View style={{ height: 280, marginTop: 10, borderRadius: 10, overflow: "hidden" }}>
            <CameraView style={{ flex: 1 }} facing="back" barcodeScannerSettings={{ barcodeTypes: ["qr"] }} onBarcodeScanned={(r) => check(r.data)} />
          </View>
        ) : (
          <View style={{ marginTop: 10 }}>
            <Button title="Scan QR with camera" onPress={async () => { if (!perm?.granted) { const r = await requestPerm(); if (!r.granted) return; } last.current = ""; setCamera(true); }} />
          </View>
        )}
        <View style={{ marginTop: 12 }}>
          <Field label="Or paste the ticket code" value={paste} onChangeText={setPaste} placeholder="YX1.…" autoCapitalize="none" multiline />
          <Button title="Verify" kind="secondary" onPress={() => { last.current = ""; check(paste.trim()); }} disabled={!paste.trim()} />
        </View>
      </Card>
      {result && !result.ok && (
        <Card style={{ backgroundColor: color.dangerSoft, borderColor: color.danger }}>
          <Text style={{ color: color.danger, fontWeight: "700" }}>Invalid ticket</Text>
          <Text style={{ color: color.danger }}>{result.why}</Text>
        </Card>
      )}
      {t && (
        <Card style={{ borderColor: wrongTrain ? color.danger : color.ok }}>
          <View style={s.row}>
            <Text style={{ fontWeight: "700", color: wrongTrain ? color.danger : color.ok }}>{wrongTrain ? "Genuine ticket, but for another train" : "Genuine ticket"}</Text>
          </View>
          <Muted>
            PNR {t.p} · {t.t} · {t.f} → {t.to} · {t.c} · {t.d}
          </Muted>
          {pax.length === 0 && !wrongTrain && <Muted>Not on this chart (cancelled, or booked after the download — sync to refresh).</Muted>}
          {pax.map((p) => (
            <PassengerRow key={p.id} p={p} actions={p.status !== "NOSHOW" && !p.boarded_at ? <Button title="Mark boarded" small onPress={() => record("board", { passenger_id: p.id, station: here })} /> : undefined} />
          ))}
          {pax.length > 1 && pax.some((p) => !p.boarded_at && p.status !== "NOSHOW") && (
            <View style={{ marginTop: 8 }}>
              <Button title="Mark everyone boarded" onPress={() => pax.forEach((p) => !p.boarded_at && p.status !== "NOSHOW" && record("board", { passenger_id: p.id, station: here }))} />
            </View>
          )}
        </Card>
      )}
    </ScrollView>
  );
}

// ── RAC and waitlist ───────────────────────────────────────────────────────

function RACTab() {
  const { bundle, view, stop, record } = useStore();
  const [cls, setCls] = useState<string | null>(null);
  const here = bundle!.stops[stop]!.code;
  const racs = view!.passengers.filter((p) => (p.status === "RAC" || p.status === "WL") && p.to_idx > stop && (!cls || p.class === cls))
    .sort((a, b) => a.class.localeCompare(b.class) || (a.status === b.status ? (a.rac_no ?? a.wl_no ?? 0) - (b.rac_no ?? b.wl_no ?? 0) : a.status === "RAC" ? -1 : 1));
  const classes = [...new Set(view!.passengers.filter((p) => p.status === "RAC" || p.status === "WL").map((p) => p.class))];
  return (
    <FlatList
      contentContainerStyle={{ padding: 14 }}
      data={racs}
      keyExtractor={(p) => p.id}
      ListHeaderComponent={
        <>
          <Chips items={[{ key: "", label: "All" }, ...classes.map((k) => ({ key: k, label: k }))]} value={cls ?? ""} onChange={(k) => setCls(k || null)} />
          <Muted>RAC passengers share a side-lower until a berth frees up. Vacant berths from {here}:</Muted>
          <View style={{ height: 8 }} />
        </>
      }
      renderItem={({ item: q }) => {
        const free = q.status === "RAC" ? vacantBerths(view!, bundle!, q.class, Math.max(stop, q.from_idx), q.to_idx).slice(0, 3) : [];
        return (
          <Card>
            <PassengerRow p={q} actions={free.length ? free.map((b) => (
              <Button key={b.coach + b.berth} title={`→ ${b.coach}/${b.berth} ${b.type}`} small kind="secondary" onPress={() => record("assign_berth", { passenger_id: q.id, coach: b.coach, berth: b.berth, station: here })} />
            )) : undefined} />
            {q.status === "RAC" && free.length === 0 && <Muted>No berth free for their whole remaining journey yet.</Muted>}
          </Card>
        );
      }}
      ListEmptyComponent={<Muted>No RAC or waitlisted passengers still travelling.</Muted>}
    />
  );
}

// ── Excess-fare ticket ─────────────────────────────────────────────────────

function FineTab() {
  const { bundle, view, stop, record } = useStore();
  const classes = [...new Set(bundle!.coaches.map((c) => c.class))];
  const [name, setName] = useState("");
  const [age, setAge] = useState("");
  const [gender, setGender] = useState<"M" | "F" | "O">("M");
  const [to, setTo] = useState(bundle!.stops.length - 1);
  const [cls, setCls] = useState(classes.includes("SL") ? "SL" : classes[0]!);
  const [reason, setReason] = useState<"ticketless" | "wrong_class" | "beyond_destination">("ticketless");
  const [paid, setPaid] = useState(classes.includes("SL") ? "SL" : classes[0]!);
  const [berth, setBerth] = useState<string>("");
  const [receipt, setReceipt] = useState<string | null>(null);
  const from = stop;
  let fare = fareFor(bundle!, cls, from, to);
  if (reason === "wrong_class") fare = Math.max(0, fare - fareFor(bundle!, paid, from, to));
  const total = fare + bundle!.penalty;
  const vacant = to > from ? vacantBerths(view!, bundle!, cls, from, to).slice(0, 12) : [];
  const ok = name.trim() && Number(age) > 0 && to > from;
  return (
    <ScrollView contentContainerStyle={{ padding: 14 }}>
      <Card>
        <H>Excess-fare ticket</H>
        <Chips items={[{ key: "ticketless", label: "Ticketless" }, { key: "wrong_class", label: "Wrong class" }, { key: "beyond_destination", label: "Beyond destination" }]} value={reason} onChange={setReason} />
        <Field label="Passenger name" value={name} onChangeText={setName} />
        <View style={s.row}>
          <View style={{ width: 90 }}>
            <Field label="Age" value={age} onChangeText={setAge} keyboardType="number-pad" />
          </View>
          <Chips items={[{ key: "M", label: "Male" }, { key: "F", label: "Female" }, { key: "O", label: "Other" }]} value={gender} onChange={setGender} />
        </View>
        <Text style={s.label}>From {bundle!.stops[from]!.name} to</Text>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginBottom: 10 }}>
          <Chips items={bundle!.stops.filter((x) => x.seq > from).map((x) => ({ key: x.seq, label: x.code }))} value={to} onChange={setTo} />
        </ScrollView>
        <Text style={s.label}>Class</Text>
        <Chips items={classes.map((k) => ({ key: k, label: k }))} value={cls} onChange={(k) => { setCls(k); setBerth(""); }} />
        {reason === "wrong_class" && (
          <>
            <Text style={s.label}>Class on the ticket</Text>
            <Chips items={classes.map((k) => ({ key: k, label: k }))} value={paid} onChange={setPaid} />
          </>
        )}
        {vacant.length > 0 && (
          <>
            <Text style={s.label}>Allot a vacant berth (optional)</Text>
            <Chips items={[{ key: "", label: "No berth" }, ...vacant.map((b) => ({ key: `${b.coach}/${b.berth}`, label: `${b.coach}/${b.berth} ${b.type}` }))]} value={berth} onChange={setBerth} />
          </>
        )}
        <View style={[s.row, { marginVertical: 8 }]}>
          <Text style={{ fontSize: 22, fontWeight: "700", color: color.ink }}>₹{total}</Text>
          <Muted>
            ₹{fare} fare + ₹{bundle!.penalty} penalty
          </Muted>
        </View>
        <Button
          title="Collect and issue receipt"
          disabled={!ok}
          onPress={() => {
            const [coach, b] = berth ? berth.split("/") : ["", "0"];
            record("fine", { name: name.trim(), age: Number(age), gender, from: bundle!.stops[from]!.code, to: bundle!.stops[to]!.code, class: cls, reason,
              paid_class: reason === "wrong_class" ? paid : "", coach, berth: Number(b) });
            setReceipt(`₹${total} collected from ${name.trim()}${berth ? `, berth ${berth}` : ""}. The receipt number arrives with the next sync.`);
            setName("");
            setAge("");
            setBerth("");
          }}
        />
        {receipt && <Text style={{ color: color.accent, marginTop: 8 }}>{receipt}</Text>}
      </Card>
      <Card>
        <H>Issued on this run</H>
        {view!.fines.length === 0 && <Muted>None yet.</Muted>}
        {view!.fines.map((f) => (
          <View key={f.receipt_no} style={{ paddingVertical: 6, borderTopWidth: 1, borderColor: color.line }}>
            <View style={s.row}>
              <Text style={{ flex: 1, fontWeight: "600", color: color.ink }}>{f.name}</Text>
              <Text style={{ fontWeight: "700", color: color.ink }}>₹{f.total}</Text>
            </View>
            <Muted>
              {f.receipt_no.startsWith("PENDING") ? "receipt pending sync" : f.receipt_no} · {f.from} → {f.to} · {f.class} · {f.reason.replace("_", " ")}
              {f.coach ? ` · ${f.coach}/${f.berth}` : ""}
            </Muted>
          </View>
        ))}
      </Card>
    </ScrollView>
  );
}

// ── Complaints, hand-off, sync log ─────────────────────────────────────────

function MoreTab() {
  const { bundle, view, stop, record, outbox, log } = useStore();
  const [cat, setCat] = useState("cleanliness");
  const [text, setText] = useState("");
  const [coach, setCoach] = useState("");
  const [to, setTo] = useState("");
  const [note, setNote] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  const here = bundle!.stops[stop]!.code;
  return (
    <ScrollView contentContainerStyle={{ padding: 14 }}>
      <Card>
        <H>Report a complaint or incident</H>
        <Chips items={["cleanliness", "food", "security", "medical", "staff", "other"].map((k) => ({ key: k, label: k }))} value={cat} onChange={setCat} />
        <Field label="Coach (optional)" value={coach} onChangeText={setCoach} autoCapitalize="characters" />
        <Field label="What happened" value={text} onChangeText={setText} multiline />
        <Button title="Report" disabled={!text.trim()} onPress={() => { record("complaint", { category: cat, description: text.trim(), coach }); setText(""); setMsg("Complaint saved; it goes to RailMadad on sync."); }} />
      </Card>
      <Card>
        <H>Hand the chart over</H>
        <Muted>At a crew-change station, pass the chart (with everything done so far) to the next TTE.</Muted>
        <Field label="Next TTE's employee ID" value={to} onChangeText={setTo} autoCapitalize="characters" placeholder="TTE00002" />
        <Field label="Note" value={note} onChangeText={setNote} placeholder="2 no-shows in S4, RAC 3 moved to S4/23" />
        <Button title={`Hand over at ${here}`} kind="secondary" disabled={!to.trim()} onPress={() => { record("handoff", { to_employee_id: to.trim(), station: here, note }); setMsg(`Hand-off to ${to.trim()} saved`); setTo(""); setNote(""); }} />
        {bundle!.handoffs.map((h, i) => (
          <Muted key={i}>
            {h.from} → {h.to} at {h.station} {h.note ? `· ${h.note}` : ""}
          </Muted>
        ))}
      </Card>
      {msg && <Text style={{ color: color.accent, marginBottom: 10 }}>{msg}</Text>}
      <Card>
        <H>Waiting to sync ({outbox.length})</H>
        {outbox.length === 0 && <Muted>Nothing waiting.</Muted>}
        {outbox.map((o) => (
          <Muted key={o.op_id}>
            {new Date(o.at).toLocaleTimeString("en-IN", { hour12: false })} · {o.kind.replace("_", " ")}
          </Muted>
        ))}
        {view!.notes.map((n, i) => (
          <Muted key={i}>{n}</Muted>
        ))}
      </Card>
      <Card>
        <H>Sync results</H>
        {log.length === 0 && <Muted>No syncs yet.</Muted>}
        {log.slice(0, 30).map((r) => (
          <View key={r.op_id + r.status} style={{ paddingVertical: 5, borderTopWidth: 1, borderColor: color.line }}>
            <View style={s.row}>
              <Pill text={r.status} tone={r.status === "applied" ? "ok" : r.status === "resolved" ? "warn" : r.status === "rejected" ? "danger" : "neutral"} />
              <Text style={{ flex: 1, fontSize: 13, color: color.ink }}>{r.message}</Text>
            </View>
          </View>
        ))}
      </Card>
    </ScrollView>
  );
}
