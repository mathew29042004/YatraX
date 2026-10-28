"use client";

import { ApiError, type DemoAccount, type Role } from "@yatrax/api-client";
import { useRouter } from "next/navigation";
import { useEffect, useState, type FormEvent } from "react";
import { Alert, Badge, Button, Card, Input, Label, Logo, cn } from "./primitives";
import { nextPath, roleAllowed, useYatrax } from "./session";

export const roleLabel: Record<Role, string> = {
  passenger: "Passenger",
  tte: "TTE",
  station: "Station Master",
  vendor: "Catering Vendor",
  controller: "Controller",
  admin: "Admin",
};

type Mode = "otp" | "staff";

export function LoginScreen() {
  const { client, ready, app } = useYatrax();
  const router = useRouter();
  const passengerApp = app.roles.includes("passenger");
  const [mode, setMode] = useState<Mode>(passengerApp ? "otp" : "staff");
  const [demos, setDemos] = useState<DemoAccount[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [phone, setPhone] = useState("");
  const [otp, setOtp] = useState("");
  const [otpSent, setOtpSent] = useState(false);
  const [devOtp, setDevOtp] = useState<string | null>(null);
  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");

  // Someone who arrives already signed in goes straight to their home screen.
  // Only on first load: a fresh sign-in navigates from finish(), and two
  // concurrent replace() calls can cancel each other.
  useEffect(() => {
    if (ready && client.current && roleAllowed(app, client.current.user.role)) router.replace(nextPath(app));
  }, [ready, client, app, router]);

  useEffect(() => {
    client
      .demoAccounts()
      .then((all) => setDemos(all.filter((d) => roleAllowed(app, d.role))))
      .catch(() => setDemos([]));
  }, [client, app]);

  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Cannot reach YatraX. Is the core service running?");
    } finally {
      setBusy(false);
    }
  }

  async function finish() {
    const s = client.current;
    if (s && !roleAllowed(app, s.user.role)) {
      await client.logout();
      throw new ApiError(403, "wrong_app", `This app is for ${app.roles.map((r) => roleLabel[r]).join(" / ")} accounts.`);
    }
    router.replace(nextPath(app));
  }

  const sendOtp = (e?: FormEvent) => {
    e?.preventDefault();
    return run(async () => {
      const r = await client.requestOtp(phone);
      setOtpSent(true);
      setDevOtp(r.dev_otp ?? null);
    });
  };

  const verify = (e: FormEvent) => {
    e.preventDefault();
    return run(async () => {
      await client.verifyOtp(phone, otp);
      await finish();
    });
  };

  const staffLogin = (e: FormEvent) => {
    e.preventDefault();
    return run(async () => {
      await client.login(identifier, password);
      await finish();
    });
  };

  const signInDemo = (d: DemoAccount) =>
    run(async () => {
      if (d.phone) {
        setMode("otp");
        setPhone(d.phone);
        const r = await client.requestOtp(d.phone);
        if (!r.dev_otp) throw new ApiError(400, "no_dev_otp", "Demo sign-in needs the core service in development mode.");
        await client.verifyOtp(d.phone, r.dev_otp);
      } else if (d.employee_id && d.password) {
        setMode("staff");
        setIdentifier(d.employee_id);
        setPassword(d.password);
        await client.login(d.employee_id, d.password);
      }
      await finish();
    });

  // Admins can use every app, so the passenger app also offers staff sign-in.
  const showTabs = passengerApp;

  return (
    <main className="flex min-h-screen items-center justify-center px-4 py-10">
      <div className="w-full max-w-md">
        <div className="mb-6 flex items-center gap-3">
          <Logo className="h-10 w-10" />
          <div>
            <h1 className="text-lg font-semibold leading-tight">
              YatraX <span className="text-[var(--accent)]">{app.name}</span>
            </h1>
            <div className="text-sm text-muted">{app.tagline}</div>
          </div>
        </div>

        <Card className="p-6">
          {showTabs && (
            <div className="mb-5 grid grid-cols-2 gap-1 rounded-lg bg-canvas p-1 text-sm">
              {(["otp", "staff"] as Mode[]).map((m) => (
                <button
                  key={m}
                  onClick={() => setMode(m)}
                  className={cn("rounded-md py-1.5 font-medium", mode === m ? "bg-surface shadow-sm" : "text-muted")}
                >
                  {m === "otp" ? "Mobile OTP" : "Staff ID"}
                </button>
              ))}
            </div>
          )}

          {mode === "otp" ? (
            <form onSubmit={otpSent ? verify : sendOtp} className="space-y-4">
              <div>
                <Label htmlFor="phone">Mobile number</Label>
                <div className="flex gap-2">
                  <span className="flex h-10 items-center rounded-lg border border-line bg-canvas px-3 text-sm text-muted">+91</span>
                  <Input
                    id="phone"
                    inputMode="numeric"
                    autoComplete="tel-national"
                    placeholder="98765 43210"
                    value={phone}
                    onChange={(e) => {
                      setPhone(e.target.value);
                      setOtpSent(false);
                    }}
                  />
                </div>
              </div>
              {otpSent && (
                <div>
                  <Label htmlFor="otp">6-digit OTP</Label>
                  <Input
                    id="otp"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    maxLength={6}
                    value={otp}
                    onChange={(e) => setOtp(e.target.value)}
                    autoFocus
                  />
                  {devOtp && (
                    <p className="mt-1.5 text-xs text-muted">
                      Mock SMS (dev only): your OTP is <span className="font-mono font-semibold text-ink">{devOtp}</span>
                    </p>
                  )}
                </div>
              )}
              {error && <Alert>{error}</Alert>}
              <Button type="submit" className="w-full" disabled={busy}>
                {otpSent ? "Verify and sign in" : "Send OTP"}
              </Button>
              {otpSent && (
                <button type="button" onClick={() => sendOtp()} className="w-full text-xs text-muted hover:text-ink">
                  Resend OTP
                </button>
              )}
            </form>
          ) : (
            <form onSubmit={staffLogin} className="space-y-4">
              <div>
                <Label htmlFor="ident">Employee ID</Label>
                <Input id="ident" autoComplete="username" placeholder="e.g. SM00001" value={identifier} onChange={(e) => setIdentifier(e.target.value)} />
              </div>
              <div>
                <Label htmlFor="pw">Password</Label>
                <Input id="pw" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
              </div>
              {error && <Alert>{error}</Alert>}
              <Button type="submit" className="w-full" disabled={busy}>
                Sign in
              </Button>
            </form>
          )}
        </Card>

        {demos.length > 0 && (
          <div className="mt-5">
            <div className="mb-2 text-xs font-medium uppercase tracking-wide text-muted">Demo accounts · synthetic data</div>
            <div className="space-y-2">
              {demos.map((d) => (
                <button
                  key={d.role}
                  disabled={busy}
                  onClick={() => signInDemo(d)}
                  className="flex w-full items-center justify-between rounded-lg border border-line bg-surface px-3 py-2.5 text-left text-sm hover:border-[var(--accent)] disabled:opacity-50"
                >
                  <span>
                    <span className="font-medium">{d.full_name}</span>
                    <span className="block text-xs text-muted">
                      {d.designation || "Passenger"}
                      {d.station ? ` · ${d.station}` : ""} · {d.employee_id ?? `+91 ${d.phone}`}
                    </span>
                  </span>
                  <Badge tone={d.role === "admin" ? "saffron" : "brand"}>{roleLabel[d.role]}</Badge>
                </button>
              ))}
            </div>
          </div>
        )}
      </div>
    </main>
  );
}
