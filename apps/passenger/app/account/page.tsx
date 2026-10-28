"use client";

import { AppShell, Card, CardTitle, NetworkStatsGrid, ProfileCard, RequireRole, TrainLookup } from "@yatrax/ui";
import Link from "next/link";
import { LoyaltyCard } from "@/components/loyalty-card";
import { SavedPassengers } from "@/components/saved-passengers";
import { WalletCard } from "@/components/wallet-card";

export default function AccountPage() {
  return (
    <RequireRole>
      {(session) => (
        <AppShell session={session}>
          <ProfileCard
            session={session}
            extra={
              <Link href="/" className="text-sm font-medium text-[var(--accent)] hover:underline">
                Search trains →
              </Link>
            }
          />
          <div className="grid gap-5 md:grid-cols-2">
            <WalletCard />
            <LoyaltyCard />
            <SavedPassengers />
            <Card>
              <CardTitle>Quick links</CardTitle>
              <ul className="space-y-1.5 text-sm">
                {[
                  ["/trips", "My trips", "tickets, food orders, invoices and help on board"],
                  ["/tatkal", "Tatkal waiting room", "a fair queue that opens at 10:00 sharp"],
                  ["/live", "Live train status", "where your train is, its delay and your platform"],
                  ["/pnr", "PNR status", "check any ticket"],
                  ["/groups", "Group booking desk", "7 to 60 passengers for a wedding or pilgrimage"],
                  ["/guarantee", "Confirmation guarantee", "hold two waitlisted trains, keep the first to confirm"],
                ].map(([href, title, hint]) => (
                  <li key={href}>
                    <Link href={href!} className="font-medium text-[var(--accent)] hover:underline">
                      {title}
                    </Link>{" "}
                    <span className="text-muted">— {hint}</span>
                  </li>
                ))}
              </ul>
            </Card>
          </div>
          <NetworkStatsGrid />
          <TrainLookup initial="12623" />
        </AppShell>
      )}
    </RequireRole>
  );
}
