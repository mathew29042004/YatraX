import type {
  SpecialTrain,
  Group, GroupRequest, Stay, StayOption, StayUnit,
  RouteAlternative,
  Invoice, Loyalty,
  Complaint, ComplaintCategory, SOSResult,
  ChaosDrill, ChaosRun, ReplayDay,
  AnalyticsOccupancy, AnalyticsOverview, AnalyticsPunctuality, AnalyticsScope, AnalyticsZones, FraudReport,
  FoodOrder, FoodOrderRequest, FoodStatus, FoodStop, MenuEdit, MenuItem, VendorPayouts,
  DisruptionInput, DisruptionResult, DisruptionRow, ExtraCoachesResult, LongWaitlist, NetworkConflict, Ripple, SectionChart,
  CoachPosition, StationAnnouncements, StationFacilities, StationPlatforms,
  TTEBundle, TTEDuty, TTEOp, TTEResult,
  LiveBrief, LiveIncident, LiveStatus, SimClock, SimClockChange, StationBoard,
  BotFlag, BotSignals, Integrity, RoomMonitor, RoomStatus, WaitRoom,
  AppNotification, CancelQuote, CancelResult, Chart, Finance, Promotion, ReconcileReport, TDR, Wallet,
  Booking, CalendarResult, CoachLayout, DemoAccount, FareQuote, Festival, HoldRequest, NetworkStats, Quota, SavedPassenger,
  SearchParams, SearchResult, SeatMap, Session, Station, StationCall, TrainDetail, TrainSummary, User, VendorProfile, Zone,
} from "./types";

export * from "./types";

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
  }
}

const SESSION_KEY = "yatrax.session";

function query(params: Record<string, string | number | boolean | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "" && v !== false) q.set(k, String(v));
  }
  return q.toString();
}

function readSession(): Session | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(SESSION_KEY);
    return raw ? (JSON.parse(raw) as Session) : null;
  } catch {
    return null;
  }
}

function writeSession(s: Session | null) {
  if (typeof window === "undefined") return;
  try {
    if (s) window.localStorage.setItem(SESSION_KEY, JSON.stringify(s));
    else window.localStorage.removeItem(SESSION_KEY);
  } catch {
    // Storage can be unavailable (private mode); the session then lasts for this page only.
  }
}

type Listener = (s: Session | null) => void;

/**
 * Client for the core API. Holds the session, attaches the access token,
 * and transparently refreshes it once when a request comes back 401.
 */
export class YatraxClient {
  private session: Session | null = readSession();
  private refreshing: Promise<boolean> | null = null;
  private listeners = new Set<Listener>();

  constructor(private baseUrl: string) {}

  get current(): Session | null {
    return this.session;
  }

  /** Restores a session saved elsewhere (React Native has no localStorage; the TTE app keeps it in AsyncStorage). */
  restore(s: Session | null) {
    this.setSession(s);
  }

  subscribe(fn: Listener): () => void {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  }

  private setSession(s: Session | null) {
    this.session = s;
    writeSession(s);
    this.listeners.forEach((fn) => fn(s));
  }

  private async raw<T>(method: string, path: string, body?: unknown, auth = false, extra?: Record<string, string>): Promise<T> {
    const headers: Record<string, string> = { "Content-Type": "application/json", ...extra };
    if (auth && this.session) headers.Authorization = `Bearer ${this.session.access_token}`;
    const res = await fetch(this.baseUrl + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (res.status === 204) return undefined as T;
    const data = await res.json().catch(() => null);
    if (!res.ok) {
      throw new ApiError(res.status, data?.error ?? "http_error", data?.message ?? `Request failed (${res.status})`);
    }
    return data as T;
  }

  private async authed<T>(method: string, path: string, body?: unknown, extra?: Record<string, string>): Promise<T> {
    try {
      return await this.raw<T>(method, path, body, true, extra);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401 && (await this.refresh())) {
        return this.raw<T>(method, path, body, true, extra);
      }
      throw e;
    }
  }

  /** Rotates the refresh token. Concurrent callers share one refresh. */
  refresh(): Promise<boolean> {
    if (!this.session) return Promise.resolve(false);
    this.refreshing ??= this.raw<Session>("POST", "/v1/auth/refresh", { refresh_token: this.session.refresh_token })
      .then((s) => {
        this.setSession(s);
        return true;
      })
      .catch(() => {
        this.setSession(null);
        return false;
      })
      .finally(() => {
        this.refreshing = null;
      });
    return this.refreshing;
  }

  // ── Auth ────────────────────────────────────────────────────────────────
  requestOtp(phone: string) {
    return this.raw<{ sent: boolean; expires_in: number; dev_otp?: string }>("POST", "/v1/auth/otp/request", { phone });
  }
  async verifyOtp(phone: string, otp: string, fullName?: string) {
    const s = await this.raw<Session>("POST", "/v1/auth/otp/verify", { phone, otp, full_name: fullName ?? "" });
    this.setSession(s);
    return s;
  }
  async login(identifier: string, password: string) {
    const s = await this.raw<Session>("POST", "/v1/auth/login", { identifier, password });
    this.setSession(s);
    return s;
  }
  async logout() {
    const s = this.session;
    this.setSession(null);
    if (s) await this.raw("POST", "/v1/auth/logout", { refresh_token: s.refresh_token }).catch(() => undefined);
  }
  demoAccounts() {
    return this.raw<DemoAccount[]>("GET", "/v1/auth/demo-accounts");
  }
  me() {
    return this.authed<User>("GET", "/v1/me");
  }

  // ── Network ─────────────────────────────────────────────────────────────
  stats() {
    return this.raw<NetworkStats>("GET", "/v1/network/stats");
  }
  zones() {
    return this.raw<Zone[]>("GET", "/v1/zones");
  }
  stations(q = "", limit = 50) {
    return this.raw<Station[]>("GET", `/v1/stations?q=${encodeURIComponent(q)}&limit=${limit}`);
  }
  station(code: string) {
    return this.raw<Station>("GET", `/v1/stations/${encodeURIComponent(code)}`);
  }
  stationTrains(code: string) {
    return this.raw<StationCall[]>("GET", `/v1/stations/${encodeURIComponent(code)}/trains`);
  }
  trains(q = "", type = "", limit = 50) {
    return this.raw<TrainSummary[]>("GET", `/v1/trains?q=${encodeURIComponent(q)}&type=${type}&limit=${limit}`);
  }
  train(number: string) {
    return this.raw<TrainDetail>("GET", `/v1/trains/${encodeURIComponent(number)}`);
  }

  coach(train: string, coachNo: string) {
    return this.raw<CoachLayout>("GET", `/v1/trains/${encodeURIComponent(train)}/coaches/${encodeURIComponent(coachNo)}`);
  }

  // ── Search, fares, availability ─────────────────────────────────────────
  /** Split journeys and connections when direct trains are full. */
  alternatives(from: string, to: string, date: string, cls: string) {
    return this.raw<RouteAlternative[]>("GET", `/v1/search/alternatives?${query({ from, to, date, class: cls })}`);
  }
  search(p: SearchParams) {
    return this.raw<SearchResult>("GET", `/v1/search?${query({ ...p, nearby: p.nearby ? 1 : undefined })}`);
  }
  calendar(train: string, from: string, to: string, cls: string, quota: Quota, start: string, days = 30) {
    return this.raw<CalendarResult>("GET", `/v1/availability?${query({ train, from, to, class: cls, quota, start, days })}`);
  }
  fare(train: string, from: string, to: string, cls: string, quota: Quota) {
    return this.raw<FareQuote>("GET", `/v1/fare?${query({ train, from, to, class: cls, quota })}`);
  }
  festivals() {
    return this.raw<Festival[]>("GET", "/v1/festivals");
  }

  // ── Booking ─────────────────────────────────────────────────────────────
  seatMap(p: { train: string; from: string; to: string; date: string; class: string; quota: Quota }) {
    return this.authed<SeatMap>("GET", `/v1/seatmap?${query(p)}`);
  }
  /** Holds berths for 10 minutes. Reuse the same idempotency key when retrying the same attempt. */
  createBooking(req: HoldRequest, idempotencyKey: string) {
    return this.authed<Booking>("POST", "/v1/bookings", req, { "Idempotency-Key": idempotencyKey });
  }
  bookings() {
    return this.authed<Booking[]>("GET", "/v1/bookings");
  }
  booking(id: string) {
    return this.authed<Booking>("GET", `/v1/bookings/${id}`);
  }
  pay(id: string) {
    return this.authed<{ checkout_url: string }>("POST", `/v1/bookings/${id}/pay`);
  }
  verifyPayment(id: string, p: { order_id: string; payment_id: string; signature: string }) {
    return this.authed<Booking>("POST", `/v1/bookings/${id}/verify`, p);
  }
  release(id: string) {
    return this.authed<void>("POST", `/v1/bookings/${id}/release`);
  }
  ticket(id: string) {
    return this.authed<{ booking: Booking; qr: string }>("GET", `/v1/bookings/${id}/ticket`);
  }
  pnr(pnr: string) {
    return this.raw<Booking>("GET", `/v1/pnr/${encodeURIComponent(pnr)}`);
  }
  savedPassengers() {
    return this.authed<SavedPassenger[]>("GET", "/v1/me/passengers");
  }
  addSavedPassenger(p: Omit<SavedPassenger, "id">) {
    return this.authed<SavedPassenger>("POST", "/v1/me/passengers", p);
  }
  deleteSavedPassenger(id: string) {
    return this.authed<void>("DELETE", `/v1/me/passengers/${id}`);
  }

  // ── Cancellation, refunds, wallet ───────────────────────────────────────
  cancelQuote(id: string, passengers: number[] = []) {
    return this.authed<CancelQuote>("GET", `/v1/bookings/${id}/cancel-quote?passengers=${passengers.join(",")}`);
  }
  cancel(id: string, passengers: number[], refundTo: "source" | "wallet") {
    return this.authed<CancelResult>("POST", `/v1/bookings/${id}/cancel`, { passengers, refund_to: refundTo });
  }
  payWithWallet(id: string) {
    return this.authed<Booking>("POST", `/v1/bookings/${id}/pay-wallet`);
  }
  changeBoarding(id: string, station: string) {
    return this.authed<Booking>("POST", `/v1/bookings/${id}/boarding`, { station });
  }
  fileTDR(id: string, reason: string, details: string) {
    return this.authed<TDR>("POST", `/v1/bookings/${id}/tdr`, { reason, details });
  }
  tdrReasons() {
    return this.raw<Record<string, string>>("GET", "/v1/tdr-reasons");
  }
  wallet() {
    return this.authed<Wallet>("GET", "/v1/me/wallet");
  }
  notifications() {
    return this.authed<{ unread: number; items: AppNotification[] }>("GET", "/v1/me/notifications");
  }
  readNotifications() {
    return this.authed<void>("POST", "/v1/me/notifications/read");
  }

  // ── Operations ──────────────────────────────────────────────────────────
  finance() {
    return this.authed<Finance>("GET", "/v1/admin/finance");
  }
  tdrs(status = "") {
    return this.authed<TDR[]>("GET", `/v1/admin/tdrs?status=${status}`);
  }
  decideTDR(id: string, approve: boolean, refund: number) {
    return this.authed<TDR>("POST", `/v1/admin/tdrs/${id}/decide`, { approve, refund });
  }
  prepareChart(train: string, originDate: string, version: 1 | 2) {
    return this.authed<Chart>("POST", "/v1/admin/runs/chart", { train, origin_date: originDate, version });
  }
  chart(train: string, originDate: string, version: 1 | 2 = 1) {
    return this.authed<Chart>("GET", `/v1/charts?${query({ train, origin_date: originDate, version })}`);
  }
  simulateCancellations(train: string, originDate: string, cls: string, count: number) {
    return this.authed<{ cancelled: number; promotions: Promotion[] }>("POST", "/v1/admin/runs/simulate-cancellations",
      { train, origin_date: originDate, class: cls, count });
  }
  reconcile() {
    return this.authed<ReconcileReport>("POST", "/v1/admin/reconcile");
  }

  // ── Tatkal waiting room ─────────────────────────────────────────────────
  waitrooms() {
    return this.raw<WaitRoom[]>("GET", "/v1/waitrooms");
  }
  joinRoom(id: string, signals: BotSignals) {
    return this.authed<RoomStatus>("POST", `/v1/waitrooms/${id}/join`, signals);
  }
  saveDraft(id: string, draft: HoldRequest, signals: BotSignals) {
    return this.authed<RoomStatus>("PUT", `/v1/waitrooms/${id}/draft`, { ...draft, signals });
  }
  solveChallenge(id: string, nonce: string) {
    return this.authed<RoomStatus>("POST", `/v1/waitrooms/${id}/challenge`, { nonce });
  }
  roomStatus(id: string) {
    return this.authed<RoomStatus>("GET", `/v1/waitrooms/${id}/status`);
  }
  bookFromRoom(id: string) {
    return this.authed<Booking>("POST", `/v1/waitrooms/${id}/book`);
  }
  /** WebSocket URL for live position updates (the token rides in the query string). */
  roomSocketURL(id: string) {
    const base = this.baseUrl.replace(/^http/, "ws");
    return `${base}/v1/waitrooms/${id}/ws?access_token=${encodeURIComponent(this.session?.access_token ?? "")}`;
  }
  createDrill(journeyDate: string, ac: boolean, opensInSec: number, admitRate: number) {
    return this.authed<WaitRoom>("POST", "/v1/admin/waitrooms/drill", { journey_date: journeyDate, ac, opens_in_sec: opensInSec, admit_rate: admitRate });
  }
  roomMonitor(id: string) {
    return this.authed<RoomMonitor>("GET", `/v1/admin/waitrooms/${id}/monitor`);
  }
  roomBots(id: string) {
    return this.authed<BotFlag[]>("GET", `/v1/admin/waitrooms/${id}/bots`);
  }
  integrity() {
    return this.authed<Integrity>("GET", "/v1/admin/integrity");
  }

  // ── Live trains and the simulation clock ────────────────────────────────
  simClock() {
    return this.raw<SimClock>("GET", "/v1/sim/clock");
  }
  setSimClock(change: SimClockChange) {
    return this.authed<SimClock>("POST", "/v1/admin/sim/clock", change);
  }
  /** Live status of a run; without a date, the run that matters now. */
  liveTrain(number: string, originDate?: string) {
    return this.raw<LiveStatus>("GET", `/v1/live/trains/${number}${originDate ? `?date=${originDate}` : ""}`);
  }
  liveMap(opts: { zone?: string; late?: boolean } = {}) {
    return this.raw<{ sim_now: string; trains: LiveBrief[] }>("GET", `/v1/live/map?${query({ zone: opts.zone, late: opts.late ? 1 : undefined })}`);
  }
  stationBoard(code: string, hours = 4) {
    return this.raw<StationBoard>("GET", `/v1/live/stations/${code}?hours=${hours}`);
  }
  /** Every station as [code, lat, lon, importance], for drawing the network. */
  stationsGeo() {
    return this.raw<Array<[string, number, number, number]>>("GET", "/v1/live/stations-geo");
  }
  addIncident(train: string, originDate: string, station: string, delayMin: number, reason: string) {
    return this.authed<LiveStatus>("POST", "/v1/admin/live/incidents", { train, origin_date: originDate, station, delay_min: delayMin, reason });
  }
  incidents() {
    return this.authed<LiveIncident[]>("GET", "/v1/admin/live/incidents");
  }
  /** WebSocket URL: one run (`{ train, date }`) or the live map (`{ map: true, zone }`). Live status is public. */
  liveSocketURL(p: { train?: string; date?: string; map?: boolean; zone?: string; late?: boolean }) {
    const base = this.baseUrl.replace(/^http/, "ws");
    return `${base}/v1/live/ws?${query({ train: p.train, date: p.date, map: p.map ? 1 : undefined, zone: p.zone, late: p.late ? 1 : undefined })}`;
  }

  // ── TTE app ──────────────────────────────────────────────────────────────
  tteDuties(station?: string) {
    return this.authed<{ station: string; station_name?: string; duties: TTEDuty[] }>("GET", `/v1/tte/duties${station ? `?station=${station}` : ""}`);
  }
  /** Everything needed to work a run offline. */
  tteBundle(train: string, originDate: string) {
    return this.authed<TTEBundle>("GET", `/v1/tte/runs/${train}/${originDate}`);
  }
  /** Sends offline operations; the server applies them in order and returns a fresh bundle. */
  tteSync(train: string, originDate: string, ops: TTEOp[]) {
    return this.authed<{ results: TTEResult[]; bundle: TTEBundle }>("POST", `/v1/tte/runs/${train}/${originDate}/sync`, { ops });
  }

  // ── Station app ──────────────────────────────────────────────────────────
  stationAnnouncements(code: string) {
    return this.raw<StationAnnouncements>("GET", `/v1/stations/${code}/announcements`);
  }
  coachPosition(code: string, train: string, originDate?: string) {
    return this.raw<CoachPosition>("GET", `/v1/stations/${code}/coach-position/${train}${originDate ? `?date=${originDate}` : ""}`);
  }
  stationPlatforms(code: string, hours = 4) {
    return this.authed<StationPlatforms>("GET", `/v1/station-master/${code}/platforms?hours=${hours}`);
  }
  /** Put a run on a platform at a station; platform 0 puts it back on its timetabled one. */
  assignPlatform(code: string, train: string, originDate: string, platform: number) {
    return this.authed<unknown>("POST", `/v1/station-master/${code}/platforms`, { train, origin_date: originDate, platform });
  }
  stationFacilities(code: string) {
    return this.authed<StationFacilities>("GET", `/v1/station-master/${code}/facilities`);
  }
  addLostItem(code: string, item: string, description: string, foundAt: string) {
    return this.authed<{ id: number }>("POST", `/v1/station-master/${code}/lost-found`, { item, description, found_at: foundAt });
  }
  returnLostItem(code: string, id: number, returnedTo: string) {
    return this.authed<void>("POST", `/v1/station-master/${code}/lost-found/${id}/return`, { returned_to: returnedTo });
  }
  addWheelchair(code: string, passenger: string, pnr: string, train: string, neededAt: string) {
    return this.authed<{ id: number }>("POST", `/v1/station-master/${code}/wheelchair`, { passenger, pnr, train, needed_at: neededAt });
  }
  updateWheelchair(code: string, id: number, status: "assigned" | "done", attendant = "") {
    return this.authed<void>("POST", `/v1/station-master/${code}/wheelchair/${id}`, { status, attendant });
  }

  // ── Control centre ───────────────────────────────────────────────────────
  sectionChart(from: string, to: string, hours = 4) {
    return this.authed<SectionChart>("GET", `/v1/control/section?${query({ from, to, hours })}`);
  }
  /** Simulates holding a train: who else it delays, and how many passengers. Nothing is saved. */
  whatIf(train: string, originDate: string, station: string, delayMin: number) {
    return this.authed<Ripple>("POST", "/v1/control/what-if", { train, origin_date: originDate, station, delay_min: delayMin });
  }
  networkConflicts(zone?: string, hours = 2) {
    return this.authed<NetworkConflict[]>("GET", `/v1/control/conflicts?${query({ zone, hours })}`);
  }
  longWaitlists(days = 14) {
    return this.authed<LongWaitlist[]>("GET", `/v1/control/waitlists?days=${days}`);
  }
  disruptions() {
    return this.authed<DisruptionRow[]>("GET", "/v1/control/disruptions");
  }
  /** Cancel, short-terminate or divert a run: passengers are refunded, rebooked and told. */
  disruptRun(train: string, originDate: string, d: DisruptionInput) {
    return this.authed<DisruptionResult>("POST", "/v1/control/runs/disrupt", { train, origin_date: originDate, ...d });
  }
  addExtraCoaches(train: string, originDate: string, cls: string, count = 1) {
    return this.authed<ExtraCoachesResult>("POST", "/v1/control/runs/extra-coaches", { train, origin_date: originDate, class: cls, count });
  }

  // ── Catering ─────────────────────────────────────────────────────────────
  /** Stations on a booking's journey where food can be delivered, with their vendors. */
  foodOptions(bookingId: string) {
    return this.authed<FoodStop[]>("GET", `/v1/bookings/${bookingId}/food`);
  }
  vendorMenu(vendorId: string) {
    return this.authed<MenuItem[]>("GET", `/v1/vendors/${vendorId}/menu`);
  }
  placeFoodOrder(o: FoodOrderRequest) {
    return this.authed<FoodOrder>("POST", "/v1/food-orders", o);
  }
  myFoodOrders(bookingId?: string) {
    return this.authed<FoodOrder[]>("GET", `/v1/food-orders${bookingId ? `?booking_id=${bookingId}` : ""}`);
  }
  cancelFoodOrder(id: string) {
    return this.authed<FoodOrder>("POST", `/v1/food-orders/${id}/cancel`);
  }
  rateFoodOrder(id: string, stars: number, review = "", hygieneComplaint = false) {
    return this.authed<FoodOrder>("POST", `/v1/food-orders/${id}/rate`, { stars, review, hygiene_complaint: hygieneComplaint });
  }
  /** Vendors: open orders, or closed ones (delivered, rejected, cancelled, missed). */
  vendorOrders(closed = false) {
    return this.authed<FoodOrder[]>("GET", `/v1/vendor/orders${closed ? "?closed=1" : ""}`);
  }
  advanceFoodOrder(id: string, status: FoodStatus, note = "") {
    return this.authed<FoodOrder>("POST", `/v1/vendor/orders/${id}/status`, { status, note });
  }
  myMenu() {
    return this.authed<MenuItem[]>("GET", "/v1/vendor/menu");
  }
  addMenuItem(m: MenuEdit) {
    return this.authed<{ id: number }>("POST", "/v1/vendor/menu", m);
  }
  editMenuItem(id: number, m: MenuEdit) {
    return this.authed<void>("PATCH", `/v1/vendor/menu/${id}`, m);
  }
  vendorPayouts() {
    return this.authed<VendorPayouts>("GET", "/v1/vendor/payouts");
  }
  requestPayout() {
    return this.authed<{ amount: number; reference: string }>("POST", "/v1/vendor/payouts");
  }

  /** Two waitlisted holds for one trip: the first to confirm keeps it, the other is refunded in full. */
  createGuarantee(first: HoldRequest, second: HoldRequest, idempotencyKey?: string) {
    return this.authed<{ first: Booking; second: Booking }>("POST", "/v1/bookings/guarantee", { first, second },
      idempotencyKey ? { "Idempotency-Key": idempotencyKey } : undefined);
  }

  // ── Retiring rooms, dormitories and cloak rooms ──────────────────────────
  stayOptions(station: string, from: string, hours = 24) {
    return this.raw<StayOption[]>("GET", `/v1/stations/${station}/stays?${query({ from, hours })}`);
  }
  bookStay(r: { booking_id: string; station: string; unit: StayUnit; units: number; check_in: string; hours: number; payment: "wallet" | "counter" }) {
    return this.authed<Stay>("POST", "/v1/stays", r);
  }
  myStays() {
    return this.authed<Stay[]>("GET", "/v1/me/stays");
  }
  cancelStay(id: number) {
    return this.authed<Stay>("POST", `/v1/stays/${id}/cancel`);
  }
  stayDesk(station: string) {
    return this.authed<Stay[]>("GET", `/v1/station-master/${station}/stays`);
  }
  advanceStay(station: string, id: number, action: "check_in" | "check_out") {
    return this.authed<Stay>("POST", `/v1/station-master/${station}/stays/${id}`, { action });
  }

  // ── Group booking desk ───────────────────────────────────────────────────
  requestGroup(g: GroupRequest) {
    return this.authed<Group>("POST", "/v1/groups", g);
  }
  myGroups() {
    return this.authed<Group[]>("GET", "/v1/me/groups");
  }
  cancelGroup(id: number) {
    return this.authed<Group>("POST", `/v1/groups/${id}/cancel`);
  }
  /** Commercial desk: a station master's own departures; controllers and admins see all. */
  groupQueue() {
    return this.authed<Group[]>("GET", "/v1/group-desk");
  }
  decideGroup(id: number, approve: boolean, note = "") {
    return this.authed<Group>("POST", `/v1/group-desk/${id}`, { approve, note });
  }

  // ── Invoices and loyalty ─────────────────────────────────────────────────
  invoice(bookingId: string) {
    return this.authed<Invoice>("GET", `/v1/bookings/${bookingId}/invoice`);
  }
  loyalty() {
    return this.authed<Loyalty>("GET", "/v1/me/loyalty");
  }
  redeemPoints(points: number) {
    return this.authed<Loyalty>("POST", "/v1/me/loyalty/redeem", { points });
  }

  // ── RailMadad complaints ─────────────────────────────────────────────────
  fileComplaint(bookingId: string, category: ComplaintCategory, description: string, coach = "", berth = 0) {
    return this.authed<Complaint>("POST", `/v1/bookings/${bookingId}/complaints`, { category, description, coach, berth });
  }
  /** Medical emergency, security alert to the RPF, or a female TTE to the passenger's berth. */
  raiseSOS(bookingId: string, kind: "medical" | "security" | "female_tte") {
    return this.authed<SOSResult>("POST", `/v1/bookings/${bookingId}/sos`, { kind });
  }
  myComplaints(bookingId?: string) {
    return this.authed<Complaint[]>("GET", `/v1/me/complaints${bookingId ? `?booking_id=${bookingId}` : ""}`);
  }
  /** Staff queue: a station master's own station; controllers and admins see all (or one station). */
  complaintQueue(opts: { station?: string; resolved?: boolean } = {}) {
    return this.authed<Complaint[]>("GET", `/v1/complaints?${query({ station: opts.station, resolved: opts.resolved ? 1 : undefined })}`);
  }
  actOnComplaint(id: number, action: "acknowledge" | "resolve", resolution = "") {
    return this.authed<Complaint>("POST", `/v1/complaints/${id}`, { action, resolution });
  }

  // ── Chaos mode and the digital twin ─────────────────────────────────────
  chaosDrills() {
    return this.authed<ChaosDrill[]>("GET", "/v1/chaos/drills");
  }
  chaosRuns() {
    return this.authed<ChaosRun[]>("GET", "/v1/chaos/runs");
  }
  /** Starts a drill in the background; poll chaosRuns to follow it. */
  startChaos(kind: string) {
    return this.authed<ChaosRun>("POST", "/v1/chaos/runs", { kind });
  }
  specialTrains() {
    return this.authed<SpecialTrain[]>("GET", "/v1/control/specials");
  }
  /** Runs a special train (0xxxx) on a date; it opens for booking at once. */
  runSpecial(train: string, date: string, reason = "") {
    return this.authed<SpecialTrain>("POST", "/v1/control/specials", { train, date, reason });
  }
  replayDay(date: string) {
    return this.authed<ReplayDay>("GET", `/v1/control/replay?date=${date}`);
  }

  // ── Analytics ────────────────────────────────────────────────────────────
  analyticsZones() {
    return this.authed<AnalyticsZones>("GET", "/v1/analytics/zones");
  }
  analyticsOverview(sc: AnalyticsScope) {
    return this.authed<AnalyticsOverview>("GET", `/v1/analytics/overview?${query({ zone: sc.zone, division: sc.division || undefined })}`);
  }
  analyticsOccupancy(sc: AnalyticsScope, days = 14) {
    return this.authed<AnalyticsOccupancy>("GET", `/v1/analytics/occupancy?${query({ zone: sc.zone, division: sc.division || undefined, days })}`);
  }
  analyticsPunctuality(sc: AnalyticsScope, days = 7) {
    return this.authed<AnalyticsPunctuality>("GET", `/v1/analytics/punctuality?${query({ zone: sc.zone, division: sc.division || undefined, days })}`);
  }
  fraudReport() {
    return this.authed<FraudReport>("GET", "/v1/analytics/fraud");
  }

  // ── Role-specific ───────────────────────────────────────────────────────
  adminUsers(role = "", q = "", limit = 50) {
    return this.authed<User[]>("GET", `/v1/admin/users?role=${role}&q=${encodeURIComponent(q)}&limit=${limit}`);
  }
  vendorMe() {
    return this.authed<VendorProfile>("GET", "/v1/vendor/me");
  }
}
