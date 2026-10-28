// The app's state: who is signed in, the run being worked, its downloaded bundle, and
// the outbox of operations made since the last sync. Everything is kept in AsyncStorage,
// so the app keeps working (and remembers its work) with no network and across restarts.
import AsyncStorage from "@react-native-async-storage/async-storage";
import NetInfo from "@react-native-community/netinfo";
import type { Session, TTEBundle, TTEOp, TTEOpKind, TTEResult } from "@yatrax/api-client";
import { YatraxClient } from "@yatrax/api-client";
import { randomUUID } from "expo-crypto";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { applyLocal, guessStop, type View } from "./logic";

const API_URL = process.env.EXPO_PUBLIC_API_URL ?? "http://localhost:8080";
export const client = new YatraxClient(API_URL);

const K = {
  session: "tte.session",
  run: "tte.run", // { train, date }
  bundle: (r: Run) => `tte.bundle.${r.train}.${r.date}`,
  outbox: (r: Run) => `tte.outbox.${r.train}.${r.date}`,
  log: (r: Run) => `tte.log.${r.train}.${r.date}`,
  stop: (r: Run) => `tte.stop.${r.train}.${r.date}`,
  offline: "tte.forceOffline",
};

export type Run = { train: string; date: string };

type Ctx = {
  ready: boolean;
  session: Session | null;
  online: boolean; // the network is reachable and the TTE hasn't switched to offline mode
  forceOffline: boolean;
  setForceOffline: (v: boolean) => void;
  run: Run | null;
  bundle: TTEBundle | null;
  view: View | null;
  outbox: TTEOp[];
  log: TTEResult[]; // results of past syncs, newest first
  stop: number; // where the TTE says the train is
  setStop: (k: number) => void;
  open: (r: Run) => Promise<void>;
  close: () => void;
  record: (kind: TTEOpKind, payload: Record<string, unknown>) => void;
  sync: () => Promise<TTEResult[] | null>;
  syncing: boolean;
  error: string | null;
  signOut: () => Promise<void>;
};

const C = createContext<Ctx | null>(null);

async function load<T>(key: string): Promise<T | null> {
  try {
    const raw = await AsyncStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : null;
  } catch {
    return null;
  }
}
const save = (key: string, v: unknown) => AsyncStorage.setItem(key, JSON.stringify(v)).catch(() => undefined);

export function StoreProvider({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false);
  const [session, setSession] = useState<Session | null>(null);
  const [netUp, setNetUp] = useState(true);
  const [forceOffline, setForceOfflineState] = useState(false);
  const [run, setRun] = useState<Run | null>(null);
  const [bundle, setBundle] = useState<TTEBundle | null>(null);
  const [outbox, setOutbox] = useState<TTEOp[]>([]);
  const [log, setLog] = useState<TTEResult[]>([]);
  const [stop, setStopState] = useState(0);
  const [syncing, setSyncing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const outboxRef = useRef<TTEOp[]>([]);
  outboxRef.current = outbox;

  // Restore everything saved on the device.
  useEffect(() => {
    (async () => {
      const s = await load<Session>(K.session);
      if (s) client.restore(s);
      setSession(client.current);
      setForceOfflineState((await load<boolean>(K.offline)) ?? false);
      const r = await load<Run>(K.run);
      if (r) {
        const b = await load<TTEBundle>(K.bundle(r));
        if (b) {
          setRun(r);
          setBundle(b);
          setOutbox((await load<TTEOp[]>(K.outbox(r))) ?? []);
          setLog((await load<TTEResult[]>(K.log(r))) ?? []);
          setStopState((await load<number>(K.stop(r))) ?? guessStop(b, new Date(b.sim_now)));
        }
      }
      setReady(true);
    })();
    const unsub = client.subscribe((s) => {
      setSession(s);
      save(K.session, s);
    });
    const net = NetInfo.addEventListener((st) => setNetUp(st.isConnected !== false && st.isInternetReachable !== false));
    return () => {
      unsub();
      net();
    };
  }, []);

  const online = netUp && !forceOffline;

  const setForceOffline = (v: boolean) => {
    setForceOfflineState(v);
    save(K.offline, v);
  };

  const open = useCallback(async (r: Run) => {
    setError(null);
    const b = await client.tteBundle(r.train, r.date);
    await save(K.bundle(r), b);
    await save(K.run, r);
    const pending = (await load<TTEOp[]>(K.outbox(r))) ?? [];
    setRun(r);
    setBundle(b);
    setOutbox(pending);
    setLog((await load<TTEResult[]>(K.log(r))) ?? []);
    setStopState((await load<number>(K.stop(r))) ?? guessStop(b, new Date(b.sim_now)));
  }, []);

  const close = () => {
    setRun(null);
    setBundle(null);
    setOutbox([]);
    AsyncStorage.removeItem(K.run).catch(() => undefined);
  };

  const record = useCallback(
    (kind: TTEOpKind, payload: Record<string, unknown>) => {
      if (!run) return;
      const op: TTEOp = { op_id: randomUUID(), kind, at: new Date().toISOString(), payload };
      setOutbox((xs) => {
        const next = [...xs, op];
        save(K.outbox(run), next);
        return next;
      });
    },
    [run],
  );

  const sync = useCallback(async () => {
    if (!run || syncing) return null;
    const ops = outboxRef.current;
    setSyncing(true);
    setError(null);
    try {
      const { results, bundle: fresh } = await client.tteSync(run.train, run.date, ops);
      // Drop what the server has seen; keep anything recorded while the sync was in flight.
      const seen = new Set(results.map((r) => r.op_id));
      setOutbox((xs) => {
        const rest = xs.filter((o) => !seen.has(o.op_id));
        save(K.outbox(run), rest);
        return rest;
      });
      setBundle(fresh);
      save(K.bundle(run), fresh);
      setLog((xs) => {
        const next = [...results.slice().reverse(), ...xs].slice(0, 100);
        save(K.log(run), next);
        return next;
      });
      return results;
    } catch (e) {
      setError(e instanceof Error ? e.message : "Sync failed");
      return null;
    } finally {
      setSyncing(false);
    }
  }, [run, syncing]);

  // Back online with work queued: sync by itself (and keep the chart fresh every 20 s).
  useEffect(() => {
    if (!online || !run) return;
    if (outbox.length > 0) sync();
    const t = setInterval(() => sync(), 20000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, run?.train, run?.date, outbox.length > 0]);

  const setStop = (k: number) => {
    setStopState(k);
    if (run) save(K.stop(run), k);
  };

  const view = useMemo(() => (bundle ? applyLocal(bundle, outbox) : null), [bundle, outbox]);

  const signOut = async () => {
    await client.logout().catch(() => undefined);
    client.restore(null);
    close();
  };

  return (
    <C.Provider
      value={{ ready, session, online, forceOffline, setForceOffline, run, bundle, view, outbox, log, stop, setStop,
        open, close, record, sync, syncing, error, signOut }}
    >
      {children}
    </C.Provider>
  );
}

export function useStore() {
  const c = useContext(C);
  if (!c) throw new Error("useStore outside StoreProvider");
  return c;
}
