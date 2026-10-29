// Shapes returned by the YatraX core API (services/core). Keep in sync with the Go handlers.

export type Role = "passenger" | "tte" | "station" | "vendor" | "controller" | "admin";

export interface User {
  id: string;
  role: Role;
  full_name: string;
  phone?: string;
  employee_id?: string;
  language: string;
  home_station?: string;
  designation?: string;
  zone?: string;
  division?: string;
  station?: string;
}

export interface Session {
  access_token: string;
  access_expires_at: string;
  refresh_token: string;
  refresh_expires_at: string;
  user: User;
}

export interface DemoAccount {
  role: Role;
  full_name: string;
  phone?: string;
  employee_id?: string;
  password?: string;
  designation?: string;
  station?: string;
}

export interface NetworkStats {
  zones: number;
  divisions: number;
  stations: number;
  trains: number;
  stops: number;
  coaches: number;
  vendors: number;
  users: Partial<Record<Role, number>>;
  seeded_at: string;
  seed_random: string;
}

export interface Zone {
  code: string;
  name: string;
  hq: string;
  divisions: string[];
  stations: number;
}

export interface Station {
  code: string;
  name: string;
  state: string;
  zone: string;
  division: string;
  lat: number;
  lon: number;
  importance: 1 | 2 | 3;
  platforms: number;
  language: string;
}

export interface TrainSummary {
  number: string;
  name: string;
  type: string;
  origin: string;
  destination: string;
  departure: string;
  arrival: string;
  duration_min: number;
  distance_km: number;
  running_days: boolean[];
  classes: string[];
  pantry: boolean;
}

export interface Stop {
  seq: number;
  station: string;
  station_name: string;
  arrival?: string;
  departure?: string;
  day: number;
  halt_min: number;
  distance_km: number;
  platform: number;
}

export interface Coach {
  coach_no: string;
  class: string;
  position: number;
  seats: number;
  bookable: boolean;
}

export interface TrainDetail {
  number: string;
  name: string;
  type: string;
  type_name: string;
  origin: string;
  destination: string;
  distance_km: number;
  running_days: boolean[];
  pantry: boolean;
  return_number: string | null;
  stops: Stop[];
  coaches: Coach[];
}

export interface StationCall {
  number: string;
  name: string;
  type: string;
  origin: string;
  destination: string;
  arrival?: string;
  departure?: string;
  platform: number;
  running_days: boolean[];
}

export interface VendorProfile {
  id: string;
  name: string;
  station: string;
  station_name: string;
  cuisines: string[];
  veg_only: boolean;
  fssai_license: string;
  rating: number;
}

// ── search, fares, availability ──────────────────────────────────

export type Quota = "GN" | "TQ" | "PT" | "LD" | "SS" | "HP" | "DF" | "FT";

export type AvailabilityCode =
  | "AVL" | "RAC" | "WL" | "REGRET" | "NOT_OPEN" | "TATKAL_NOT_OPEN" | "DEPARTED" | "NOT_RUNNING" | "NO_QUOTA";

export interface Availability {
  code: AvailabilityCode;
  count?: number;
  label: string;
  opens_at?: string;
  /** Rough 0–100 chance of confirming, from the waitlist position, days left and the train's demand. */
  confirm_chance: number;
}

export interface FareQuote {
  class: string;
  quota: Quota;
  distance_km: number;
  charged_km: number;
  base: number;
  reservation: number;
  superfast: number;
  tatkal: number;
  gst: number;
  total: number;
  catering_included: boolean;
  flexi: boolean;
}

export interface StationRef {
  code: string;
  name: string;
  state: string;
}

export interface DateAvailability {
  date: string;
  availability: Availability;
}

export interface ClassOption {
  code: string;
  name: string;
  fare?: FareQuote;
  availability: Availability;
  by_date?: DateAvailability[];
}

export interface SearchTrain {
  number: string;
  name: string;
  type: string;
  type_name: string;
  from: StationRef;
  to: StationRef;
  departure: string;
  arrival: string;
  duration_min: number;
  distance_km: number;
  from_platform: number;
  running_days: boolean[];
  runs_on_date: boolean;
  pantry: boolean;
  flexi: boolean;
  classes: ClassOption[];
}

export interface SearchResult {
  from: StationRef;
  to: StationRef;
  from_stations: StationRef[];
  to_stations: StationRef[];
  date: string;
  quota: Quota;
  dates?: string[];
  generated_at: string;
  trains: SearchTrain[];
}

export interface SearchParams {
  from: string;
  to: string;
  date: string;
  quota?: Quota;
  class?: string;
  nearby?: boolean;
  flex?: number;
}

export interface CalendarResult {
  train: string;
  from: string;
  to: string;
  class: string;
  quota: Quota;
  fare?: FareQuote;
  days: DateAvailability[];
}

export interface Festival {
  name: string;
  date: string;
  states?: string[];
}

export interface CoachSeat {
  number: number;
  type: "LB" | "MB" | "UB" | "SL" | "SU" | "W" | "M" | "A";
  bay: number;
}

export interface CoachLayout {
  train: string;
  coach_no: string;
  class: string;
  class_name: string;
  position: number;
  bookable: boolean;
  layout: "sleeper_8" | "sleeper_6" | "sleeper_4" | "sitting_6" | "sitting_5" | "sitting_4" | "none";
  seats: CoachSeat[];
}

// ── booking ──────────────────────────────────────────────────────

export type BookingStatus = "HELD" | "PAYMENT_PENDING" | "CONFIRMED" | "EXPIRED" | "FAILED" | "CANCELLED";
export type BerthStatus = "CNF" | "RAC" | "WL" | "CAN" | "NOSHOW";

export interface PassengerInput {
  name: string;
  age: number;
  gender: "M" | "F" | "O";
  berth_pref: string;
  coach?: string;
  berth?: number;
  /** UDID card (divyangjan quota), defence warrant, or passport (foreign tourist). */
  id_number?: string;
}

export interface HoldRequest {
  train: string;
  from: string;
  to: string;
  date: string;
  class: string;
  quota: Quota;
  passengers: PassengerInput[];
  together: boolean;
  only_confirmed: boolean;
  /** Move to a higher class at no extra cost if berths are free at chart time. */
  auto_upgrade?: boolean;
  /** Travel insurance, ₹1 per passenger. */
  insurance?: boolean;
}

export interface BookingPassenger {
  seq: number;
  name: string;
  age: number;
  gender: string;
  berth_pref?: string;
  status: BerthStatus;
  coach?: string;
  berth?: number;
  berth_type?: string;
  rac_no?: number;
  wl_no?: number;
  fare: number;
  /** What the ticket said when issued, e.g. "WL 23". */
  booked_status?: string;
  /** Current RAC/WL position. */
  position?: number;
  refund?: number;
  /** Masked: last four characters only. */
  id_number?: string;
  /** The class before an auto-upgrade at chart time. */
  upgraded_from?: string;
}

export interface Booking {
  id: string;
  pnr?: string;
  status: BookingStatus;
  train: string;
  train_name: string;
  from: string;
  from_name: string;
  to: string;
  to_name: string;
  journey_date: string;
  /** The day the train leaves its first station (identifies the run for live status). */
  origin_date: string;
  departure: string;
  arrival: string;
  distance_km: number;
  class: string;
  class_name: string;
  quota: Quota;
  total_fare: number;
  fare: FareQuote;
  hold_expires_at?: string;
  created_at: string;
  confirmed_at?: string;
  passengers: BookingPassenger[];
  payment?: { order_id: string; status: string; method?: string; amount: number };
  refunds?: RefundInfo[];
  chart_prepared: boolean;
  cancelled_at?: string;
  original_from?: string;
  tdr?: TDR;
  insurance_premium: number;
  auto_upgrade: boolean;
  points_earned: number;
  /** The other booking of a confirmation guarantee. */
  guarantee_with?: string;
  /** Emissions for the whole party, kg CO₂, against flying the same trip. */
  carbon?: { train_kg: number; flight_kg: number; saved_kg: number };
}

export interface Invoice {
  number: string;
  date: string;
  supplier: string;
  supplier_gstin: string;
  supplier_state: string;
  customer: string;
  place_of_supply: string;
  sac: string;
  pnr: string;
  journey: string;
  lines: Array<{ description: string; qty: number; rate: number; amount: number }>;
  taxable: number;
  cgst: number;
  sgst: number;
  igst: number;
  gst_rate: number;
  insurance: number;
  total: number;
  note: string;
  cancelled: boolean;
  passengers: number;
}

export interface Loyalty {
  balance: number;
  redeemable: number;
  credit_value: number;
  rules: string;
  history: Array<{ delta: number; reason: "earned" | "reversed" | "redeemed"; pnr?: string; at: string }>;
}

export interface RefundInfo {
  id: string;
  amount: number;
  destination: "source" | "wallet";
  status: "PENDING" | "PROCESSED" | "FAILED";
  reason: string;
  created_at: string;
  processed_at?: string;
}

export interface SeatState {
  berth: number;
  type: CoachSeat["type"];
  bay: number;
  quota: string;
  rac?: boolean;
  status: "free" | "booked" | "quota";
}

export interface SeatMapCoach {
  coach: string;
  position: number;
  free: number;
  seats: SeatState[];
}

export interface SeatMap {
  train: string;
  class: string;
  quota: Quota;
  from: string;
  to: string;
  layout: CoachLayout["layout"];
  coaches: SeatMapCoach[];
}

export interface SavedPassenger {
  id: string;
  name: string;
  age: number;
  gender: "M" | "F" | "O";
  berth_pref: string;
}

// ── cancellation, refunds, wallet, charts ────────────────────────

export interface PassengerRefund {
  seq: number;
  status: string;
  fare: number;
  deduction: number;
  refund: number;
  rule: string;
}

export interface CancelQuote {
  booking_id: string;
  passengers: PassengerRefund[];
  refund: number;
  deduction: number;
  chart_prepared: boolean;
  paid_by_wallet: boolean;
}

export interface Promotion {
  booking_id: string;
  seq: number;
  from: string;
  to: string;
}

export interface CancelResult {
  quote: CancelQuote;
  booking: Booking;
  refund_id?: string;
  refund_to?: "source" | "wallet";
  promotions: Promotion[];
}

export interface TDR {
  id: string;
  booking_id: string;
  pnr: string;
  train: string;
  journey_date: string;
  reason: string;
  reason_text: string;
  details: string;
  status: "PENDING" | "APPROVED" | "REJECTED";
  refund?: number;
  max_refund: number;
  created_at: string;
  decided_at?: string;
}

export interface Wallet {
  balance: number; // paise
  history: { at: string; kind: string; memo: string; ref: string; amount: number }[];
}

export interface AppNotification {
  id: number;
  kind: string;
  title: string;
  body: string;
  link: string;
  read_at?: string;
  created_at: string;
}

export interface Finance {
  gateway_held: number;
  fares_unearned: number;
  refunds_owed: number;
  cancellation_revenue: number;
  wallets_total: number;
  trial_balance: number;
  refunds_pending: number;
  refunds_processed: number;
  payments_unresolved: number;
  tdrs_pending: number;
}

export interface ChartPassenger {
  coach: string;
  berth: number;
  berth_type: string;
  status: string;
  pnr: string;
  name: string;
  age: number;
  gender: string;
  from: string;
  to: string;
  quota: string;
}

export interface Chart {
  run_id: number;
  train: string;
  origin_date: string;
  version: 1 | 2;
  prepared_at: string;
  classes: { class: string; passengers: ChartPassenger[]; vacant_berths: number }[];
  promotions: number;
  auto_cancelled_bookings: number;
}

export interface ReconcileReport {
  checked: number;
  captured: number;
  failed: number;
  abandoned: number;
  refunds_processed: number;
  refunds_failed: number;
}

// ── Tatkal waiting room ──────────────────────────────────────────

export interface WaitRoom {
  id: string;
  kind: "real" | "drill";
  journey_date: string;
  ac: boolean;
  joins_open: string;
  opens_at: string;
  closes_at: string;
  admit_rate: number;
  state: "upcoming" | "joining" | "open" | "closed";
  queue: number;
  admitted: number;
}

export interface RoomStatus {
  room: string;
  state: WaitRoom["state"];
  joined: boolean;
  position: number;
  queue_size: number;
  admitted: boolean;
  eta_seconds: number;
  opens_in_seconds: number;
  has_draft: boolean;
  action: "allow" | "challenge" | "block";
  challenge?: { prefix: string; bits: number };
  booking_id?: string;
  outcome?: string;
}

export interface BotSignals {
  device: string;
  form_ms?: number;
  key_intervals?: number[];
  pointer_moves?: number;
}

export interface RoomMonitor {
  room: WaitRoom;
  stats: Record<string, number>;
  sold_out_seconds: Record<string, number>;
  waiting: number;
  blocked: number;
  challenged: number;
  verified: number;
  top_reasons: Record<string, number>;
}

export interface BotFlag {
  user_id: string;
  name: string;
  score: number;
  action: string;
  reasons: string[];
  ip: string;
  device: string;
  at: string;
}

export interface Integrity {
  overlapping_allocations: number;
  paid_without_ticket: number;
  trial_balance: number;
  confirmed_passengers: number;
  tatkal_bookings: number;
}

// ── live trains and the simulation clock ─────────────────────────

export interface SimClock {
  sim_now: string;
  real_now: string;
  speed: number;
  paused: boolean;
}

export interface SimClockChange {
  speed?: number;
  paused?: boolean;
  jump_to?: string;
  reset?: boolean;
}

export interface LiveStop {
  seq: number;
  station: string;
  name: string;
  distance_km: number;
  sched_arr?: string;
  sched_dep?: string;
  exp_arr?: string;
  exp_dep?: string;
  arr_delay: number;
  dep_delay: number;
  platform: number;
  platform_changed: boolean;
  state: "departed" | "here" | "upcoming" | "cancelled" | "skipped";
}

export type LiveState = "not_running" | "scheduled" | "running" | "at_station" | "arrived" | "cancelled";

export interface LiveStatus {
  train: string;
  name: string;
  type: string;
  origin_date: string;
  state: LiveState;
  delay_min: number;
  reason?: string;
  last_idx: number;
  next_idx: number;
  lat: number;
  lon: number;
  distance_km: number;
  progress: number;
  message: string;
  /** Set when control cancelled, cut short or diverted the run. */
  disruption?: "cancelled" | "short_terminated" | "diverted";
  sim_now: string;
  stops?: LiveStop[];
}

export interface LiveBrief {
  train: string;
  name: string;
  type: string;
  origin_date: string;
  state: LiveState;
  delay_min: number;
  lat: number;
  lon: number;
  next: string;
  zone: string;
  message: string;
}

export interface BoardRow {
  train: string;
  name: string;
  origin_date: string;
  from: string;
  from_name: string;
  to: string;
  to_name: string;
  sched_arr?: string;
  sched_dep?: string;
  exp_arr?: string;
  exp_dep?: string;
  delay_min: number;
  platform: number;
  platform_changed: boolean;
  state: "expected" | "arriving" | "on_platform" | "departed" | "arrived" | "cancelled";
}

export interface StationBoard {
  station: Station;
  sim_now: string;
  trains: BoardRow[];
}

export interface LiveIncident {
  id: number;
  train: string;
  origin_date: string;
  station: string;
  delay_min: number;
  reason: string;
  by: string;
  at: string;
}

/** A message on the live WebSocket. */
export type LiveMessage =
  | { kind: "status"; clock: SimClock; status: LiveStatus }
  | { kind: "map"; clock: SimClock; trains: LiveBrief[] }
  | { kind: "event"; event: Record<string, unknown> & { type: string; train: string } };

// ── the TTE app ──────────────────────────────────────────────────

export interface TTEDuty {
  train: string;
  name: string;
  origin_date: string;
  from: string;
  to: string;
  at: string;
  charted: boolean;
}

export interface TTEStop {
  seq: number;
  code: string;
  name: string;
  arr?: string;
  dep?: string;
  distance_km: number;
}

export interface TTEBerth {
  berth: number;
  type: string;
  quota: string;
  rac: boolean;
}

export interface TTECoach {
  coach: string;
  class: string;
  position: number;
  berths: TTEBerth[];
}

export interface TTEPassenger {
  id: string;
  booking_id: string;
  pnr: string;
  seq: number;
  name: string;
  age: number;
  gender: string;
  status: "CNF" | "RAC" | "WL" | "NOSHOW";
  coach?: string;
  berth?: number;
  berth_type?: string;
  rac_no?: number;
  wl_no?: number;
  class: string;
  quota: string;
  from_idx: number;
  to_idx: number;
  source: string;
  boarded_at?: string;
  no_show_at?: string;
}

export interface TTEFine {
  receipt_no: string;
  name: string;
  from: string;
  to: string;
  class: string;
  reason: string;
  fare: number;
  penalty: number;
  total: number;
  coach?: string;
  berth?: number;
  created_at: string;
}

export interface TTEBundle {
  run_id: number;
  train: string;
  name: string;
  origin_date: string;
  chart: { version: number; prepared_at: string } | null;
  stops: TTEStop[];
  coaches: TTECoach[];
  passengers: TTEPassenger[];
  /** class → total fare for stops (i, j), i < j, upper triangle row-major. */
  fares: Record<string, number[]>;
  penalty: number;
  fines: TTEFine[];
  handoffs: Array<{ from: string; to: string; to_employee_id: string; station: string; note: string; created_at: string }>;
  ops_applied: number;
  generated_at: string;
  public_key: string;
  sim_now: string;
}

export type TTEOpKind = "board" | "no_show" | "assign_berth" | "fine" | "complaint" | "handoff";

export interface TTEOp {
  op_id: string;
  kind: TTEOpKind;
  at: string;
  payload: Record<string, unknown>;
}

export interface TTEResult {
  op_id: string;
  kind: TTEOpKind;
  status: "applied" | "resolved" | "rejected" | "duplicate";
  message: string;
  data?: Record<string, unknown>;
}

// ── the station app ──────────────────────────────────────────────

export interface AnnouncementLine {
  lang: string;
  bcp47: string;
  text: string;
  /** Train number spelled digit by digit, for speech. */
  speech: string;
}

export interface Announcement {
  id: string;
  kind: "delayed" | "platform_changed" | "arriving" | "on_platform" | "departing";
  train: string;
  name: string;
  at: string;
  lines: AnnouncementLine[];
}

export interface StationAnnouncements {
  station: Station;
  sim_now: string;
  languages: string[];
  missing_regional?: string;
  items: Announcement[];
}

export interface CoachPosition {
  train: string;
  name: string;
  origin_date: string;
  station: string;
  platform: number;
  markers: number;
  engine_toward: string;
  spots: Array<{ marker: number; coach: string; class: string }>;
}

export interface PlatformSlot {
  train: string;
  name: string;
  origin_date: string;
  from: string;
  to: string;
  start: string;
  end: string;
  state: string;
  delay_min: number;
  scheduled_platform: number;
  assigned: boolean;
  boarding: number;
  alighting: number;
  general: number;
  /** No bookings opened for this run yet: boarding and alighting are estimates. */
  estimated: boolean;
}

export interface StationPlatforms {
  station: Station;
  sim_now: string;
  platforms: Array<{ no: number; slots: PlatformSlot[] }>;
  clashes: Array<{ platform: number; a: string; b: string; overlap_min: number; suggest: number }>;
  hourly: Array<{ hour: string; count: number; level: "low" | "busy" | "crowded" }>;
}

export interface StationFacilities {
  retiring_rooms: { total: number; free: number };
  cloak_room: { lockers: number; free: number };
  lost_found: Array<{ id: number; item: string; description: string; found_at: string; status: "held" | "returned"; returned_to?: string; created_at: string; returned_at?: string }>;
  wheelchair: Array<{ id: number; passenger: string; pnr: string; train: string; needed_at: string; status: "requested" | "assigned" | "done"; attendant?: string; created_at: string }>;
}

// ── the control centre ───────────────────────────────────────────

export interface SectionPoint {
  km: number;
  station: string;
  sched_arr?: string;
  sched_dep?: string;
  exp_arr?: string;
  exp_dep?: string;
}

export interface SectionPath {
  train: string;
  name: string;
  type: string;
  origin_date: string;
  direction: "up" | "down";
  delay_min: number;
  state: string;
  now_km?: number;
  points: SectionPoint[];
}

export interface SectionChart {
  from: string;
  to: string;
  stations: Array<{ code: string; name: string; km: number }>;
  start: string;
  end: string;
  sim_now: string;
  trains: SectionPath[];
}

export interface RippleEffect {
  train: string;
  name: string;
  origin_date: string;
  added_min: number;
  station: string;
  cause: string;
  via: string;
  hop: number;
  passengers: number;
}

export interface Ripple {
  train: string;
  name: string;
  origin_date: string;
  station: string;
  delay_min: number;
  arrival_now: string;
  arrival_after: string;
  passengers: number;
  affected: RippleEffect[];
  total_added_min: number;
  total_passengers: number;
}

export interface NetworkConflict {
  station: string;
  station_name: string;
  platform: number;
  a: string;
  b: string;
  b_origin_date: string;
  overlap_min: number;
  suggest: number;
}

export interface LongWaitlist {
  train: string;
  name: string;
  origin_date: string;
  class: string;
  waitlisted: number;
  rac: number;
  extra_coaches: number;
}

export interface DisruptionInput {
  kind: "cancelled" | "short_terminated" | "diverted";
  station?: string;
  skip?: string[];
  extra_delay?: number;
  reason: string;
}

export interface DisruptionResult {
  train: string;
  origin_date: string;
  kind: string;
  bookings: number;
  simulated: number;
  refunded: number;
  rebooked: number;
  no_alternative: number;
  affected: Array<{
    booking_id: string;
    pnr: string;
    passengers: number;
    refund: number;
    partial: boolean;
    alternative?: { booking_id: string; train: string; name: string; class: string; departure: string; hold_until: string; status: string };
  }>;
}

export interface DisruptionRow {
  train: string;
  name: string;
  origin_date: string;
  kind: string;
  station?: string;
  skip: string[];
  reason: string;
  by: string;
  at: string;
}

export interface ExtraCoachesResult {
  train: string;
  origin_date: string;
  class: string;
  coaches: string[];
  promotions: Array<{ booking_id: string; seq: number; from: string; to: string }>;
  still_waitlisted: number;
}

// ── Catering ───────────────────────────────────────────────────────────────

export type Diet = "veg" | "jain" | "egg" | "nonveg";
export type FoodStatus = "placed" | "accepted" | "preparing" | "handed_over" | "delivered" | "rejected" | "cancelled" | "missed";

export interface VendorBrief {
  id: string;
  name: string;
  cuisines: string[];
  veg_only: boolean;
  rating: number;
}

/** A station on the journey where food can be delivered. */
export interface FoodStop {
  seq: number;
  station: string;
  name: string;
  eta: string;
  halt_min: number;
  open: boolean;
  reason?: string;
  vendors: VendorBrief[];
}

export interface MenuItem {
  id: number;
  name: string;
  description: string;
  category: "meal" | "snack" | "beverage" | "sweet";
  diet: Diet;
  price: number;
  prep_min: number;
  available: boolean;
}

export interface FoodLine {
  item_id: number;
  name?: string;
  diet?: Diet;
  qty: number;
  price?: number;
}

export interface FoodOrderRequest {
  booking_id: string;
  vendor_id: string;
  station: string;
  items: FoodLine[];
  payment: "wallet" | "cod";
}

export interface FoodOrder {
  id: string;
  code: string;
  booking_id: string;
  vendor_id: string;
  vendor_name: string;
  train: string;
  train_name: string;
  origin_date: string;
  station: string;
  station_name: string;
  coach: string;
  berth: number;
  passenger: string;
  items: FoodLine[];
  total: number;
  commission: number;
  payment: "wallet" | "cod";
  status: FoodStatus;
  note?: string;
  prep_min: number;
  /** Live expected arrival of the train at the delivery station. */
  eta: string;
  /** Vendors: start cooking by. */
  start_by: string;
  delay_min: number;
  platform: number;
  train_state: string;
  rating?: number;
  review?: string;
  created_at: string;
  delivered_at?: string;
}

export interface MenuEdit {
  name: string;
  description: string;
  category: MenuItem["category"];
  diet: Diet;
  price: number;
  prep_min: number;
  available?: boolean;
}

export interface VendorPayouts {
  /** Rupees YatraX owes the vendor now; negative when the vendor owes commission on cash orders. */
  balance: number;
  delivered: number;
  sales: number;
  commission: number;
  payouts: Array<{ amount: number; reference: string; at: string }>;
  recent: Array<{ kind: string; ref: string; memo: string; amount: number; at: string }>;
}

// ── Analytics (zone and division dashboards) ───────────────────────────────

export interface AnalyticsZone {
  code: string;
  name: string;
  hq: string;
  divisions: Array<{ id: number; name: string }>;
}

export interface AnalyticsScope {
  zone: string;
  division: number;
}

export interface AnalyticsZones {
  zones: AnalyticsZone[];
  /** The zone this user is held to; empty for admins and controllers. */
  home_zone: string;
  on_time_min: number;
}

/** Money in rupees. */
export interface AnalyticsOverview {
  scope: AnalyticsScope;
  kpis: {
    bookings: number;
    passengers: number;
    gross: number;
    refunds: number;
    net: number;
    avg_fare: number;
    cancelled: number;
    waitlisted: number;
    booked_confirm: number;
    tatkal_share: number;
    penalties: number;
    food_sales: number;
    food_commission: number;
  };
  by_class: Array<{ key: string; passengers: number; revenue: number }>;
  by_quota: Array<{ key: string; passengers: number; revenue: number }>;
  top_trains: Array<{ number: string; name: string; passengers: number; revenue: number }>;
  top_routes: Array<{ from: string; to: string; passengers: number; revenue: number }>;
  by_travel_date: Array<{ date: string; passengers: number; revenue: number }>;
  complaints: ComplaintStats;
}

export interface ComplaintStats {
  total: number;
  open: number;
  urgent_open: number;
  avg_ack_min: number;
  avg_resolve_min: number;
  by_category: Array<{ key: string; department: string; count: number; open: number; avg_resolve_min: number }>;
}

export interface OccupancyCell {
  date: string;
  runs: boolean;
  /** Share of berth-segments sold; absent when the run isn't open for booking yet. */
  load?: number;
  wl: number;
  bookings: number;
}

export interface AnalyticsOccupancy {
  scope: AnalyticsScope;
  dates: string[];
  runs: number;
  avg_load: number;
  over_90: number;
  waitlisted: number;
  heatmap: Array<{ number: string; name: string; avg: number; cells: OccupancyCell[] }>;
  by_class: Array<{ class: string; name: string; load: number; berths: number }>;
  busiest: Array<{ number: string; name: string; date: string; load: number; wl: number }>;
  trains: number;
  max_rows: number;
}

export interface PunctualityRow {
  key: string;
  name: string;
  runs: number;
  on_time: number;
  avg_delay: number;
  late_60: number;
}

export interface TrainPunctuality {
  number: string;
  name: string;
  runs: number;
  on_time: number;
  avg_delay: number;
  worst: number;
}

export interface AnalyticsPunctuality {
  scope: AnalyticsScope;
  from: string;
  to: string;
  summary: PunctualityRow;
  cancelled: number;
  zones: PunctualityRow[];
  divisions: PunctualityRow[];
  daily: PunctualityRow[];
  worst: TrainPunctuality[];
  best: TrainPunctuality[];
  reasons: Array<{ key: string; count: number }>;
}

export interface FraudReport {
  bots: {
    checked: number;
    blocked: number;
    challenged: number;
    top_reasons: Array<{ key: string; count: number }>;
    shared_ips: Array<{ key: string; accounts: number; blocked: number }>;
    shared_devices: Array<{ key: string; accounts: number; blocked: number }>;
  };
  touts: Array<{ name: string; age: number; accounts: number; bookings: number }>;
  tatkal_heavy: Array<{ user_id: string; name: string; phone: string; count: number; amount: number; bot_flags: number }>;
  cancellers: Array<{ user_id: string; name: string; phone: string; count: number; amount: number; bot_flags: number }>;
  fine_spots: Array<{ train: string; name: string; ticketless: number; wrong_class: number; collected: number }>;
  tte_cash: Array<{ employee_id: string; name: string; fines: number; collected: number }>;
  vendors: Array<{ name: string; station: string; hygiene_complaints: number; rating: number; rejected: number; delivered: number; missed_rate: number }>;
  payments: { unresolved: number; refunds_failed: number; late_refunds: number };
}

// ── Chaos mode and the digital twin ────────────────────────────────────────

export interface ChaosDrill {
  kind: string;
  title: string;
  breaks: string;
  expect: string;
  deadline_sec: number;
}

export interface ChaosRun {
  id: number;
  kind: string;
  title: string;
  status: "running" | "recovered" | "failed";
  started_by: string;
  started_at: string;
  ended_at?: string;
  /** Seconds from the fault to recovery. */
  recovered_in?: number;
  steps: Array<{ at: string; kind: "info" | "fault" | "ok" | "recovered" | "failed"; text: string }>;
  metrics: Record<string, unknown>;
}

export interface ReplayDay {
  date: string;
  runs: number;
  on_time: number;
  avg_delay: number;
  late_60: number;
  cancelled: number;
  events: Array<{ at: string; kind: "incident" | "breakdown" | "disruption" | "platform" | "late"; train: string; name: string; origin_date: string; text: string }>;
}

// ── RailMadad complaints and SOS ───────────────────────────────────────────

export type ComplaintCategory = "cleanliness" | "food" | "water" | "electrical" | "staff" | "security" | "medical" | "women_safety" | "other";

export interface Complaint {
  id: number;
  pnr?: string;
  booking_id?: string;
  train?: string;
  train_name?: string;
  origin_date?: string;
  coach?: string;
  berth?: number;
  category: string;
  description: string;
  source: "passenger" | "sos" | "tte" | string;
  status: "open" | "in_progress" | "resolved";
  priority: "normal" | "high" | "urgent";
  department: string;
  station?: string;
  station_name?: string;
  reported_by?: string;
  assigned_to?: string;
  created_at: string;
  acknowledged_at?: string;
  resolved_at?: string;
  resolution?: string;
  resolved_by?: string;
}

export interface SOSResult {
  complaint: Complaint;
  message: string;
  helpline: string;
}

// ── Alternate routes ───────────────────────────────────────────────────────

export interface AltLeg {
  train: string;
  name: string;
  from: StationRef;
  to: StationRef;
  date: string;
  departure: string;
  arrival: string;
  class: string;
  fare: number;
  availability: Availability;
}

export interface RouteAlternative {
  kind: "split" | "connection";
  via: StationRef;
  legs: AltLeg[];
  fare: number;
  duration_min: number;
  wait_min?: number;
  note: string;
}

// ── Station stays and group bookings ───────────────────────────────────────

export type StayUnit = "ac_room" | "dorm_bed" | "locker";

export interface StayOption {
  type: StayUnit;
  name: string;
  price: Record<string, number>;
  total: number;
  free: number;
}

export interface Stay {
  id: number;
  code: string;
  station: string;
  station_name: string;
  pnr: string;
  guest: string;
  unit: StayUnit;
  unit_name: string;
  units: number;
  check_in: string;
  check_out: string;
  hours: 12 | 24 | 48;
  amount: number;
  payment: "wallet" | "counter";
  status: "booked" | "checked_in" | "completed" | "cancelled";
  created_at: string;
}

export interface GroupRequest {
  train: string;
  from: string;
  to: string;
  date: string;
  class: string;
  purpose: string;
  contact: string;
  passengers: PassengerInput[];
}

export interface Group extends GroupRequest {
  id: number;
  leader: string;
  train_name: string;
  status: "pending" | "approved" | "rejected" | "cancelled";
  note?: string;
  bookings: string[];
  decided_by?: string;
  decided_at?: string;
  created_at: string;
}

export interface SpecialTrain {
  number: string;
  name: string;
  base: string;
  base_name: string;
  from: string;
  to: string;
  departure: string;
  runs: string[];
}
