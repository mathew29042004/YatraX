"use client";

import type { SpecialTrain } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, useYatrax } from "@yatrax/ui";
import { useCallback, useEffect, useState } from "react";

/** Special trains (0xxxx): a busy train's route, two hours behind it, on the dates you pick. */
export function SpecialsCard() {
  const { client } = useYatrax();
  const [list, setList] = useState<SpecialTrain[] | null>(null);
  const [dates, setDates] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => client.specialTrains().then(setList).catch((e: Error) => setError(e.message)), [client]);
  useEffect(() => {
    load();
  }, [load]);
  async function run(sp: SpecialTrain) {
    const d = dates[sp.number];
    if (!d) return;
    setError(null);
    try {
      await client.runSpecial(sp.number, d, "Long waitlist");
      setMsg(`${sp.number} ${sp.name} runs on ${d}: it's open for booking now.`);
      load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not add the special");
    }
  }
  return (
    <Card>
      <CardTitle hint="clear a long waitlist with a whole extra train">Special trains</CardTitle>
      {error && <Alert>{error}</Alert>}
      {msg && <div className="mb-2"><Alert tone="info">{msg}</Alert></div>}
      <table className="w-full text-sm">
        <thead className="text-left text-xs text-muted">
          <tr>
            <th className="py-1">Special</th>
            <th className="py-1">Follows</th>
            <th className="py-1">Leaves</th>
            <th className="py-1">Runs on</th>
            <th className="py-1 text-right">Add a date</th>
          </tr>
        </thead>
        <tbody>
          {(list ?? []).map((sp) => (
            <tr key={sp.number} className="border-t border-line align-top">
              <td className="py-1.5">
                <span className="font-mono font-semibold">{sp.number}</span> <span className="text-muted">{sp.from}→{sp.to}</span>
              </td>
              <td className="py-1.5 text-muted">
                {sp.base} {sp.base_name}
              </td>
              <td className="py-1.5 font-mono">{sp.departure}</td>
              <td className="py-1.5">
                {sp.runs.length === 0 ? <span className="text-xs text-muted">—</span> : sp.runs.map((d) => <Badge key={d} tone="brand" className="mb-0.5 mr-1">{d.slice(5)}</Badge>)}
              </td>
              <td className="py-1.5">
                <div className="flex justify-end gap-1.5">
                  <Input
                    type="date"
                    aria-label={`Date for ${sp.number}`}
                    className="h-8 w-36 text-sm"
                    value={dates[sp.number] ?? ""}
                    onChange={(e) => setDates({ ...dates, [sp.number]: e.target.value })}
                  />
                  <Button size="sm" disabled={!dates[sp.number]} onClick={() => run(sp)}>
                    Run
                  </Button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  );
}
