"use client";

import type { Booking, Complaint, ComplaintCategory, SOSResult } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, departmentName, since, useYatrax } from "@yatrax/ui";
import { useCallback, useEffect, useState } from "react";

const categories: Array<[ComplaintCategory, string]> = [
  ["cleanliness", "Cleanliness"],
  ["food", "Food"],
  ["water", "No water"],
  ["electrical", "Lights, fans, AC, charging"],
  ["staff", "Staff behaviour"],
  ["security", "Security"],
  ["medical", "Medical"],
  ["women_safety", "Women's safety"],
  ["other", "Something else"],
];

const sos = [
  { kind: "medical", label: "Medical emergency", hint: "A doctor meets the train at the next halt" },
  { kind: "security", label: "Security alert (RPF)", hint: "The RPF boards at the next halt" },
  { kind: "female_tte", label: "Ask for a female TTE", hint: "The nearest one on duty comes to your berth" },
] as const;

/** On-board help: SOS for emergencies, and RailMadad complaints routed to whoever can fix them. */
export function HelpCard({ b }: { b: Booking }) {
  const { client } = useYatrax();
  const [mine, setMine] = useState<Complaint[]>([]);
  const [confirming, setConfirming] = useState<(typeof sos)[number]["kind"] | null>(null);
  const [result, setResult] = useState<SOSResult | null>(null);
  const [category, setCategory] = useState<ComplaintCategory>("cleanliness");
  const [text, setText] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => client.myComplaints(b.id).then(setMine).catch(() => undefined), [client, b.id]);
  useEffect(() => {
    load();
  }, [load]);

  async function raise(kind: (typeof sos)[number]["kind"]) {
    setBusy(true);
    setError(null);
    try {
      setResult(await client.raiseSOS(b.id, kind));
      setConfirming(null);
      load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not raise the alert. Call 139.");
    } finally {
      setBusy(false);
    }
  }

  async function file() {
    setBusy(true);
    setError(null);
    try {
      const c = await client.fileComplaint(b.id, category, text.trim());
      setNotice(`Complaint #${c.id} sent to ${departmentName[c.department] ?? c.department}${c.station_name ? `, who will act at ${c.station_name}` : ""}.`);
      setText("");
      load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not send the complaint");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardTitle hint="Railway helpline 139 · women's helpline 1091">Help on board</CardTitle>
      <div className="grid gap-2 sm:grid-cols-3">
        {sos.map((s) =>
          confirming === s.kind ? (
            <div key={s.kind} className="rounded-lg border border-danger p-3">
              <div className="text-sm font-semibold">{s.label}?</div>
              <div className="mt-2 flex gap-2">
                <Button size="sm" variant="danger" disabled={busy} onClick={() => raise(s.kind)}>
                  Yes, send
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setConfirming(null)}>
                  Cancel
                </Button>
              </div>
            </div>
          ) : (
            <button
              key={s.kind}
              onClick={() => setConfirming(s.kind)}
              className="rounded-lg border border-line p-3 text-left hover:border-danger"
            >
              <div className="text-sm font-semibold text-danger">{s.label}</div>
              <div className="text-xs text-muted">{s.hint}</div>
            </button>
          ),
        )}
      </div>
      {result && (
        <div className="mt-3" role="status">
          <Alert tone="info">
            <span className="font-semibold">Alert #{result.complaint.id} sent.</span> {result.message} If it's urgent, also call {result.helpline}.
          </Alert>
        </div>
      )}

      <div className="mt-4 border-t border-line pt-3">
        <div className="mb-2 text-sm font-medium">Report a problem</div>
        <div className="flex flex-wrap gap-2">
          <select aria-label="What's wrong" className="h-10 rounded-lg border border-line bg-surface px-2 text-sm" value={category} onChange={(e) => setCategory(e.target.value as ComplaintCategory)}>
            {categories.map(([k, label]) => (
              <option key={k} value={k}>
                {label}
              </option>
            ))}
          </select>
          <textarea
            aria-label="Describe the problem"
            className="min-h-10 flex-1 rounded-lg border border-line bg-surface px-3 py-2 text-sm"
            rows={2}
            placeholder="e.g. Toilet in B2 has no water since Salem"
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
          <Button onClick={file} disabled={busy || text.trim().length < 5}>
            Send
          </Button>
        </div>
        <p className="mt-1 text-xs text-muted">It goes straight to the right team (housekeeping, catering, RPF…) at the next halt, with your coach and berth.</p>
      </div>
      {notice && <div className="mt-2"><Alert tone="info">{notice}</Alert></div>}
      {error && <div className="mt-2"><Alert>{error}</Alert></div>}

      {mine.length > 0 && (
        <ul className="mt-4 space-y-1.5 text-sm">
          {mine.map((c) => (
            <li key={c.id} className="flex flex-wrap items-center gap-2 rounded-md bg-canvas px-3 py-2">
              <span className="font-mono text-xs text-muted">#{c.id}</span>
              <span>{c.category.replace("_", " ")}</span>
              <Badge tone={c.status === "resolved" ? "ok" : c.status === "in_progress" ? "brand" : "saffron"}>
                {c.status === "resolved" ? `Resolved in ${since(c.created_at, c.resolved_at)}` : c.status === "in_progress" ? "Being handled" : "Sent"}
              </Badge>
              {c.assigned_to && <span className="text-xs text-muted">{c.assigned_to} is coming</span>}
              {c.resolution && <span className="w-full text-xs text-muted">{c.resolution}</span>}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
