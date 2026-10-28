"use client";

import type { Complaint } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { useCallback, useEffect, useState } from "react";
import { hhmm } from "./live";
import { Alert, Badge, Button, Card, CardTitle, Input, cn } from "./primitives";
import { useYatrax } from "./session";

const priorityTone = { urgent: "danger", high: "saffron", normal: "neutral" } as const;
const statusText = { open: "New", in_progress: "Being handled", resolved: "Resolved" } as const;
export const departmentName: Record<string, string> = {
  housekeeping: "Housekeeping", catering: "Catering", rpf: "RPF", medical: "Medical", electrical: "Electrical",
  mechanical: "Mechanical", commercial: "Commercial",
};

/** Minutes between two times, as "12 min" or "2 h 5 min". */
export function since(from: string, to?: string) {
  const m = Math.max(0, Math.round(((to ? new Date(to) : new Date()).getTime() - new Date(from).getTime()) / 60000));
  return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h ${m % 60} min`;
}

function Row({ c, onChange }: { c: Complaint; onChange: () => void }) {
  const { client } = useYatrax();
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const act = (action: "acknowledge" | "resolve") =>
    client
      .actOnComplaint(c.id, action, text)
      .then(onChange)
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not update"));
  return (
    <li className={cn("rounded-lg border p-3", c.priority === "urgent" && c.status !== "resolved" ? "border-danger" : "border-line")}>
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="font-mono text-xs text-muted">#{c.id}</span>
        <Badge tone={priorityTone[c.priority]}>{c.priority}</Badge>
        <Badge tone="brand">{departmentName[c.department] ?? c.department}</Badge>
        <span className="font-medium">{c.category.replace("_", " ")}</span>
        {c.source === "sos" && <Badge tone="danger">SOS</Badge>}
        <span className="ml-auto text-xs text-muted">
          {statusText[c.status]} · {c.status === "resolved" && c.resolved_at ? `closed in ${since(c.created_at, c.resolved_at)}` : `${since(c.created_at)} ago`}
        </span>
      </div>
      <p className="mt-1 text-sm">{c.description}</p>
      <div className="mt-1 text-xs text-muted">
        {c.train && (
          <>
            <span className="font-mono">{c.train}</span> {c.train_name} ·{" "}
          </>
        )}
        {c.coach && `${c.coach}${c.berth ? `/${c.berth}` : ""} · `}
        {c.station_name && `act at ${c.station_name} · `}
        {c.pnr && `PNR ${c.pnr} · `}
        reported {hhmm(c.created_at)}
        {c.reported_by && ` by ${c.reported_by}`}
        {c.assigned_to && ` · assigned to ${c.assigned_to}`}
      </div>
      {c.resolution && (
        <p className="mt-1 text-xs">
          <span className="font-medium text-ok">Done:</span> {c.resolution} {c.resolved_by && <span className="text-muted">— {c.resolved_by}</span>}
        </p>
      )}
      {c.status !== "resolved" && (
        <div className="mt-2 flex flex-wrap items-center gap-2">
          {c.status === "open" && (
            <Button size="sm" variant="secondary" onClick={() => act("acknowledge")}>
              Acknowledge
            </Button>
          )}
          {open ? (
            <>
              <Input className="h-8 max-w-72 text-sm" aria-label="What was done" placeholder="What was done" value={text} onChange={(e) => setText(e.target.value)} />
              <Button size="sm" disabled={text.trim().length < 3} onClick={() => act("resolve")}>
                Mark resolved
              </Button>
            </>
          ) : (
            <Button size="sm" onClick={() => setOpen(true)}>
              Resolve…
            </Button>
          )}
        </div>
      )}
      {error && <div className="mt-1 text-xs text-danger">{error}</div>}
    </li>
  );
}

/**
 * The RailMadad complaints queue: urgent first. Station masters see their own station's
 * (the server enforces it); controllers see the whole network.
 */
export function ComplaintQueue({ title = "Complaints (RailMadad)", hint }: { title?: string; hint?: string }) {
  const { client } = useYatrax();
  const [resolved, setResolved] = useState(false);
  const [list, setList] = useState<Complaint[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(
    () => client.complaintQueue({ resolved }).then(setList).catch((e) => setError(e instanceof ApiError ? e.message : "Could not load")),
    [client, resolved],
  );
  useEffect(() => {
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [load]);
  const open = (list ?? []).filter((c) => c.status !== "resolved");
  const urgent = open.filter((c) => c.priority === "urgent").length;
  return (
    <Card>
      <CardTitle
        hint={
          <span className="inline-flex items-center gap-2">
            {urgent > 0 && <Badge tone="danger">{urgent} urgent</Badge>}
            {hint}
            <label className="inline-flex items-center gap-1">
              <input type="checkbox" checked={resolved} onChange={(e) => setResolved(e.target.checked)} /> show resolved
            </label>
          </span>
        }
      >
        {title}
      </CardTitle>
      {error && <Alert>{error}</Alert>}
      {list && list.length === 0 && <p className="text-sm text-muted">Nothing to handle. Complaints and SOS alerts from trains arriving here appear at once.</p>}
      <ul className="space-y-2">
        {(list ?? []).map((c) => (
          <Row key={c.id} c={c} onChange={load} />
        ))}
      </ul>
    </Card>
  );
}
