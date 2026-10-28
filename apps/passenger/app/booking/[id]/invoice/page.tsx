"use client";

import type { Invoice } from "@yatrax/api-client";
import { Alert, Button, Card, RequireRole, Spinner, useAsync, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { useParams } from "next/navigation";
import { rupees } from "@/components/format";
import { SiteHeader } from "@/components/site-header";

function InvoiceView() {
  const { client } = useYatrax();
  const { id } = useParams<{ id: string }>();
  const { data: inv, error } = useAsync<Invoice>(() => client.invoice(id), [client, id]);
  if (error) return <Alert>{error}</Alert>;
  if (!inv) return <Spinner label="Preparing the invoice" />;
  const row = (label: string, amount: number, strong = false) =>
    amount !== 0 && (
      <tr className={strong ? "border-t border-line font-semibold" : ""}>
        <td colSpan={3} className="py-1 text-right">
          {label}
        </td>
        <td className="py-1 text-right tabular-nums">{rupees(amount)}</td>
      </tr>
    );
  return (
    <div className="space-y-4">
      <Card className="print:border-0 print:shadow-none">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h1 className="text-xl font-semibold">Tax invoice</h1>
            <div className="text-sm text-muted">{inv.supplier}</div>
            <div className="text-xs text-muted">
              GSTIN {inv.supplier_gstin} · {inv.supplier_state}
            </div>
          </div>
          <div className="text-right text-sm">
            <div className="font-mono font-semibold">{inv.number}</div>
            <div className="text-muted">{new Date(inv.date).toLocaleDateString("en-IN", { dateStyle: "long" })}</div>
            {inv.cancelled && <div className="font-semibold text-danger">Ticket since cancelled</div>}
          </div>
        </div>
        <dl className="mt-4 grid gap-2 text-sm sm:grid-cols-2">
          <div>
            <dt className="text-xs text-muted">Billed to</dt>
            <dd>{inv.customer}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted">Place of supply</dt>
            <dd>{inv.place_of_supply}</dd>
          </div>
          <div className="sm:col-span-2">
            <dt className="text-xs text-muted">PNR {inv.pnr} · SAC {inv.sac} (passenger transport by rail)</dt>
            <dd>{inv.journey}</dd>
          </div>
        </dl>
        <table className="mt-4 w-full text-sm">
          <thead className="border-b border-line text-left text-xs text-muted">
            <tr>
              <th className="py-1">Item</th>
              <th className="py-1 text-right">Passengers</th>
              <th className="py-1 text-right">Each</th>
              <th className="py-1 text-right">Amount</th>
            </tr>
          </thead>
          <tbody>
            {inv.lines.map((l) => (
              <tr key={l.description} className="border-b border-line/60">
                <td className="py-1">{l.description}</td>
                <td className="py-1 text-right tabular-nums">{l.qty}</td>
                <td className="py-1 text-right tabular-nums">{rupees(l.rate)}</td>
                <td className="py-1 text-right tabular-nums">{rupees(l.amount)}</td>
              </tr>
            ))}
            {row("Taxable value", inv.taxable, true)}
            {row(`CGST @ ${inv.gst_rate / 2}%`, inv.cgst)}
            {row(`SGST @ ${inv.gst_rate / 2}%`, inv.sgst)}
            {row(`IGST @ ${inv.gst_rate}%`, inv.igst)}
            {row("Travel insurance (collected for the insurer)", inv.insurance)}
            {row("Total", inv.total, true)}
          </tbody>
        </table>
        <p className="mt-4 text-xs text-muted">{inv.note}</p>
      </Card>
      <div className="flex gap-3 print:hidden">
        <Button onClick={() => window.print()}>Print or save as PDF</Button>
        <Link href={`/booking/${id}`} className="self-center text-sm text-[var(--accent)] hover:underline">
          ← Back to the ticket
        </Link>
      </div>
    </div>
  );
}

export default function Page() {
  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1} className="mx-auto max-w-3xl px-4 py-6">
        <RequireRole>{() => <InvoiceView />}</RequireRole>
      </main>
    </>
  );
}
