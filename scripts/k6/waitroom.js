// k6 load test for the Tatkal waiting room: many browsers join a drill room, save their
// booking, poll their place in the queue, and book when admitted.
//
//   k6 run scripts/k6/waitroom.js                       # 300 people
//   k6 run -e USERS=1000 -e OPENS_IN=60 scripts/k6/waitroom.js
//
// The Go bot swarm (services/core/cmd/botswarm) adds bots and agents and checks
// integrity; this script measures latency and errors under plain human load.
import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Trend } from "k6/metrics";

const API = __ENV.API_URL || "http://localhost:8080";
const USERS = Number(__ENV.USERS || 300);
const OPENS_IN = Number(__ENV.OPENS_IN || 45);
const UA = "Mozilla/5.0 (Linux; Android 14) Chrome/130 Mobile";

const statusTime = new Trend("waitroom_status_ms", true);
const bookTime = new Trend("waitroom_book_ms", true);
const outcomes = new Counter("waitroom_outcomes");

export const options = {
  setupTimeout: "5m",
  scenarios: {
    rush: { executor: "per-vu-iterations", vus: USERS, iterations: 1, maxDuration: `${OPENS_IN + 180}s` },
  },
  thresholds: {
    http_req_failed: ["rate<0.01"], // sold-out and not-yet answers are expected; transport errors and 5xx are not
    waitroom_status_ms: ["p(95)<300"],
    waitroom_book_ms: ["p(99)<2000"],
    "checks{step:join}": ["rate>0.99"],
  },
};

// 409 (not yet / sold out) and 403 (blocked) are normal answers in a rush, not failures.
http.setResponseCallback(http.expectedStatuses({ min: 200, max: 299 }, 403, 409));

const json = (body) => ({ headers: { "Content-Type": "application/json", "User-Agent": UA }, body: JSON.stringify(body) });
const authed = (tok, device) => ({ headers: { "Content-Type": "application/json", Authorization: `Bearer ${tok}`, "User-Agent": UA, "X-Device-Id": device } });

function journeyDate() {
  const d = new Date(Date.now() + (20 + Math.floor(Math.random() * 35)) * 86400_000);
  return d.toISOString().slice(0, 10);
}

export function setup() {
  const adm = http.post(`${API}/v1/auth/login`, JSON.stringify({ identifier: "ADM00001", password: "yatrax123" }), json({})).json();
  const date = journeyDate();
  const room = http
    .post(`${API}/v1/admin/waitrooms/drill`, JSON.stringify({ journey_date: date, ac: false, opens_in_sec: OPENS_IN, admit_rate: 100 }), {
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${adm.access_token}` },
    })
    .json();
  const tokens = [];
  for (let i = 0; i < USERS; i++) {
    const phone = `7${String(Math.floor(Math.random() * 1e9)).padStart(9, "0")}`;
    const otp = http.post(`${API}/v1/auth/otp/request`, JSON.stringify({ phone }), json({})).json();
    const s = http.post(`${API}/v1/auth/otp/verify`, JSON.stringify({ phone, otp: otp.dev_otp, full_name: `K6 Person ${i}` }), json({})).json();
    tokens.push(s.access_token);
  }
  console.log(`room ${room.id} opens in ${OPENS_IN}s; ${tokens.length} people signed in`);
  return { room: room.id, date, tokens, admin: adm.access_token };
}

const journeys = [
  ["12304", "NDLS", "HWH"],
  ["12627", "SBC", "NDLS"],
  ["12695", "MAS", "ERN"],
  ["22159", "CSMT", "MAS"],
];

export default function (data) {
  const tok = data.tokens[__VU - 1];
  const device = `k6-${__VU}-${Math.random().toString(36).slice(2, 8)}`;
  const p = authed(tok, device);
  const base = `${API}/v1/waitrooms/${data.room}`;
  // People arrive over the half-minute before opening, typing like people.
  sleep(Math.random() * Math.max(1, OPENS_IN - 10));
  const signals = {
    device,
    form_ms: 8000 + Math.floor(Math.random() * 20000),
    key_intervals: Array.from({ length: 12 }, () => 80 + Math.floor(Math.random() * 280)),
    pointer_moves: 20 + Math.floor(Math.random() * 150),
  };
  const join = http.post(`${base}/join`, JSON.stringify(signals), p);
  check(join, { joined: (r) => r.status === 200 }, { step: "join" });
  const [train, from, to] = journeys[__VU % journeys.length];
  const draft = http.put(
    `${base}/draft`,
    JSON.stringify({
      train, from, to, date: data.date, class: "SL", quota: "TQ", together: false, only_confirmed: false, signals,
      passengers: [{ name: `K6 Person ${__VU}`, age: 25 + (__VU % 40), gender: __VU % 2 ? "M" : "F", berth_pref: "" }],
    }),
    p,
  );
  check(draft, { "draft saved": (r) => r.status === 200 }, { step: "draft" });

  const until = Date.now() + (OPENS_IN + 150) * 1000;
  while (Date.now() < until) {
    const st = http.get(`${base}/status`, p);
    statusTime.add(st.timings.duration);
    const s = st.status === 200 ? st.json() : null;
    if (s && s.admitted && s.has_draft) {
      const b = http.post(`${base}/book`, null, p);
      bookTime.add(b.timings.duration);
      const outcome = b.status === 201 ? "held" : b.status === 409 ? "sold_out" : `http_${b.status}`;
      outcomes.add(1, { outcome });
      check(b, { "booked or told sold out": (r) => r.status === 201 || r.status === 409 }, { step: "book" });
      return;
    }
    if (s && s.state === "closed") break;
    sleep(2);
  }
  outcomes.add(1, { outcome: "timed_out" });
}

export function teardown(data) {
  const integ = http.get(`${API}/v1/admin/integrity`, { headers: { Authorization: `Bearer ${data.admin}` } }).json();
  console.log(`integrity: ${JSON.stringify(integ)}`);
  if (integ.overlapping_allocations !== 0 || integ.paid_without_ticket !== 0 || integ.trial_balance !== 0) {
    throw new Error("integrity check failed");
  }
}
