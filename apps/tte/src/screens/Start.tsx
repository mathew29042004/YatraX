import type { DemoAccount, TTEDuty } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { useEffect, useState } from "react";
import { FlatList, Pressable, ScrollView, Text, View } from "react-native";
import { client, useStore } from "../store";
import { Button, Card, Field, H, Muted, Pill, color, s } from "../ui";

const istTime = (iso: string) =>
  new Date(iso).toLocaleTimeString("en-IN", { timeZone: "Asia/Kolkata", hour: "2-digit", minute: "2-digit", hour12: false });
const istDay = (iso: string) => new Date(iso).toLocaleDateString("en-IN", { timeZone: "Asia/Kolkata", weekday: "short", day: "numeric", month: "short" });

export function Login() {
  const [id, setId] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [demo, setDemo] = useState<DemoAccount[]>([]);
  useEffect(() => {
    client.demoAccounts().then((d) => setDemo(d.filter((a) => a.role === "tte"))).catch(() => undefined);
  }, []);
  const go = async (identifier: string, pw: string) => {
    setBusy(true);
    setError(null);
    try {
      const sess = await client.login(identifier, pw);
      if (sess.user.role !== "tte" && sess.user.role !== "admin") {
        await client.logout();
        setError("This app is for travelling ticket examiners.");
      }
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Can't reach YatraX. Signing in needs a network; after that the app works offline.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <ScrollView contentContainerStyle={{ padding: 20, paddingTop: 60 }}>
      <Text style={{ fontSize: 22, fontWeight: "700", color: color.ink }}>
        YatraX <Text style={{ color: color.accent }}>TTE</Text>
      </Text>
      <Muted>The chart, ticket checks and on-board work — offline when there&apos;s no signal.</Muted>
      <Card style={{ marginTop: 20 }}>
        <Field label="Employee ID" value={id} onChangeText={setId} autoCapitalize="characters" placeholder="TTE00001" />
        <Field label="Password" value={password} onChangeText={setPassword} secureTextEntry />
        {error && <Text style={{ color: color.danger, marginBottom: 10 }}>{error}</Text>}
        <Button title="Sign in" onPress={() => go(id, password)} busy={busy} disabled={!id || !password} />
      </Card>
      {demo.length > 0 && (
        <Card>
          <H>Demo accounts (synthetic data)</H>
          {demo.map((a) => (
            <Pressable key={a.employee_id} onPress={() => go(a.employee_id ?? "", a.password ?? "")} style={{ paddingVertical: 8 }} accessibilityRole="button" accessibilityLabel={`Sign in as ${a.full_name}`}>
              <Text style={{ fontWeight: "600", color: color.ink }}>{a.full_name}</Text>
              <Muted>
                {a.designation} · {a.station} · {a.employee_id}
              </Muted>
            </Pressable>
          ))}
        </Card>
      )}
    </ScrollView>
  );
}

export function Duties() {
  const { session, open, signOut, online } = useStore();
  const [duties, setDuties] = useState<TTEDuty[] | null>(null);
  const [station, setStation] = useState("");
  const [train, setTrain] = useState("");
  const [date, setDate] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = (st?: string) =>
    client
      .tteDuties(st)
      .then((d) => {
        setDuties(d.duties);
        setStation(d.station_name ?? d.station);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Can't load duties offline"));
  useEffect(() => {
    load();
  }, []);
  const download = async (t: string, d: string) => {
    setBusy(t + d);
    setError(null);
    try {
      await open({ train: t, date: d });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Download failed — you need a network to fetch the chart");
    } finally {
      setBusy(null);
    }
  };
  return (
    <View style={{ flex: 1, backgroundColor: color.canvas }}>
      <View style={[s.row, { padding: 16, paddingTop: 48, backgroundColor: color.surface, borderBottomWidth: 1, borderColor: color.line }]}>
        <View style={{ flex: 1 }}>
          <Text style={{ fontSize: 17, fontWeight: "700", color: color.ink }}>{session?.user.full_name}</Text>
          <Muted>
            {session?.user.employee_id} · {station || "…"}
          </Muted>
        </View>
        <Button title="Sign out" kind="ghost" small onPress={signOut} />
      </View>
      <FlatList
        contentContainerStyle={{ padding: 16 }}
        data={duties ?? []}
        keyExtractor={(d) => d.train + d.origin_date}
        ListHeaderComponent={
          <>
            {!online && (
              <Card style={{ backgroundColor: color.warnSoft }}>
                <Text style={{ color: color.warn }}>Offline. Download charts while you have a network; you can work them offline after.</Text>
              </Card>
            )}
            <Card>
              <H>Any train</H>
              <View style={[s.row, { alignItems: "flex-end" }]}>
                <View style={{ flex: 1 }}>
                  <Field label="Train" value={train} onChangeText={setTrain} placeholder="12623" keyboardType="number-pad" />
                </View>
                <View style={{ flex: 1.3 }}>
                  <Field label="Leaves origin (YYYY-MM-DD)" value={date} onChangeText={setDate} placeholder="2026-11-02" />
                </View>
              </View>
              <Button title="Download chart" onPress={() => download(train.trim(), date.trim())} disabled={!train || !date} busy={busy === train.trim() + date.trim()} />
            </Card>
            {error && <Text style={{ color: color.danger, marginBottom: 10 }}>{error}</Text>}
            <H>Trains at {station || "your station"} soon</H>
          </>
        }
        renderItem={({ item: d }) => (
          <Card>
            <View style={s.row}>
              <Text style={{ fontWeight: "700", fontSize: 15, color: color.ink }}>{d.train}</Text>
              <Text style={{ flex: 1, color: color.ink }} numberOfLines={1}>
                {d.name}
              </Text>
              {d.charted ? <Pill text="Chart ready" tone="ok" /> : <Pill text="Live list" />}
            </View>
            <Muted>
              {d.from} → {d.to} · here {istDay(d.at)} {istTime(d.at)} · left origin {d.origin_date}
            </Muted>
            <View style={{ marginTop: 8 }}>
              <Button title="Download chart" small kind="secondary" onPress={() => download(d.train, d.origin_date)} busy={busy === d.train + d.origin_date} />
            </View>
          </Card>
        )}
        ListEmptyComponent={duties ? <Muted>No trains call at your station in the next day and a half.</Muted> : <Muted>Loading…</Muted>}
      />
    </View>
  );
}
