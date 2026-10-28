// Offline ticket check: a QR ticket is `YX1.<payload>.<signature>` (base64url), signed
// with the railway's Ed25519 key. The public key comes with the downloaded bundle, so
// verification needs no network.
import * as ed from "@noble/ed25519";
import { sha512 } from "@noble/hashes/sha2.js";

ed.hashes.sha512 = sha512;

export interface TicketPayload {
  p: string; // PNR
  t: string; // train
  d: string; // boarding date
  f: string; // from
  to: string;
  c: string; // class
  q: string; // quota
  ps: string[][]; // [name, age+gender, status, coach/berth]
  i: number; // issued (unix seconds)
}

const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

function b64decode(s: string): Uint8Array {
  const clean = s.replace(/-/g, "+").replace(/_/g, "/").replace(/=+$/, "");
  const out: number[] = [];
  let bits = 0;
  let value = 0;
  for (const ch of clean) {
    const v = B64.indexOf(ch);
    if (v < 0) throw new Error("bad base64");
    value = (value << 6) | v;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out.push((value >> bits) & 0xff);
    }
  }
  return new Uint8Array(out);
}

const utf8 = (b: Uint8Array) => new TextDecoder().decode(b);
const bytes = (s: string) => new TextEncoder().encode(s);

export type Verdict = { ok: true; ticket: TicketPayload } | { ok: false; why: string };

export function verifyTicket(token: string, publicKeyB64: string): Verdict {
  const parts = token.trim().split(".");
  if (parts.length !== 3) return { ok: false, why: "Not a YatraX ticket" };
  try {
    const [prefix, body, sig] = parts as [string, string, string];
    const good = ed.verify(b64decode(sig), bytes(`${prefix}.${body}`), b64decode(publicKeyB64));
    if (!good) return { ok: false, why: "Signature does not match: this ticket was altered or forged" };
    return { ok: true, ticket: JSON.parse(utf8(b64decode(body))) as TicketPayload };
  } catch {
    return { ok: false, why: "Could not read the ticket" };
  }
}
