// Command botswarm stress-tests the Tatkal waiting room: thousands of simulated
// people — real-looking humans and bot farms — join a drill room, fill in their
// bookings, wait for their turn and book the moment they're admitted.
//
//	go run ./cmd/botswarm -users 5000 -bots 0.3
//
// Humans have their own device and network, take seconds to fill the form and type
// irregularly. Bots share devices and networks in farms, fill the form in
// milliseconds with machine-regular typing, use scripted user agents and poll the
// queue fast. At the end it prints what happened and checks integrity: no berth
// sold twice, no money taken without a ticket.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/waitroom"
)

var (
	api     = flag.String("api", "http://localhost:8080", "core API")
	gw      = flag.String("gateway", "http://localhost:8081", "payment gateway")
	users   = flag.Int("users", 5000, "simulated people")
	botFrac = flag.Float64("bots", 0.3, "share of them that are bots")
	agentFr = flag.Float64("agents", 0.05, "share that are travel agents: several accounts per phone, typing like people")
	journey = flag.String("journey", "", "origin date (default: a random day 5–60 days out, so repeated drills don't find last run's berths sold)")
	opensIn = flag.Duration("opens", 75*time.Second, "time until the room opens (people log in meanwhile)")
	rate    = flag.Int("rate", 120, "admissions per second")
	pay     = flag.Bool("pay", true, "humans pay for their held bookings")
	conc    = flag.Int("concurrency", 400, "max parallel logins")
)

var client = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConns: 2000, MaxIdleConnsPerHost: 2000, MaxConnsPerHost: 300}}

type person struct {
	n                    int
	bot                  bool
	token, device, ip    string
	ua                   string
	journey              journeyChoice
	outcome              string
	bookLatency          time.Duration
	admittedAt, joinedAt time.Time
	challenged, blocked  bool
}

type journeyChoice struct {
	Train, From, To string
}

// netErrors counts transport failures by cause, so the report can say why requests never reached the API.
var netErrors sync.Map

func call(method, path, token string, body any, p *person, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	u := path
	if !strings.HasPrefix(path, "http") {
		u = *api + path
	}
	req, _ := http.NewRequest(method, u, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if p != nil {
		req.Header.Set("User-Agent", p.ua)
		req.Header.Set("X-Forwarded-For", p.ip)
		req.Header.Set("X-Device-Id", p.device)
	}
	res, err := client.Do(req)
	for try := 0; err != nil && try < 3 && strings.Contains(err.Error(), "refused"); try++ {
		// the local port proxy dropped a new connection: back off and redial, as a browser would
		time.Sleep(time.Duration(100<<try) * time.Millisecond)
		if body != nil {
			b, _ := json.Marshal(body)
			req.Body = io.NopCloser(bytes.NewReader(b))
		}
		res, err = client.Do(req)
	}
	if err != nil {
		msg := err.Error()
		if i := strings.LastIndex(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
		n, _ := netErrors.LoadOrStore(msg, new(atomic.Int64))
		n.(*atomic.Int64).Add(1)
		return 0, err
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	} else {
		io.Copy(io.Discard, res.Body)
	}
	return res.StatusCode, nil
}

func login(p *person) error {
	phone := fmt.Sprintf("%d%09d", 6+rand.IntN(4), rand.IntN(1_000_000_000))
	var otp struct {
		DevOTP string `json:"dev_otp"`
	}
	if _, err := call("POST", "/v1/auth/otp/request", "", map[string]string{"phone": phone}, nil, &otp); err != nil {
		return err
	}
	var s struct {
		AccessToken string `json:"access_token"`
	}
	if _, err := call("POST", "/v1/auth/otp/verify", "", map[string]string{"phone": phone, "otp": otp.DevOTP, "full_name": fmt.Sprintf("Swarm %d", p.n)}, nil, &s); err != nil {
		return err
	}
	if s.AccessToken == "" {
		return fmt.Errorf("login failed")
	}
	p.token = s.AccessToken
	return nil
}

type status struct {
	State     string `json:"state"`
	Position  int64  `json:"position"`
	Admitted  bool   `json:"admitted"`
	Action    string `json:"action"`
	Challenge *struct {
		Prefix string `json:"prefix"`
		Bits   int    `json:"bits"`
	} `json:"challenge"`
}

func (p *person) signals() map[string]any {
	if p.bot {
		iv := make([]int, 8)
		for i := range iv {
			iv[i] = 20 + rand.IntN(2)
		}
		return map[string]any{"device": p.device, "form_ms": 100 + rand.IntN(300), "key_intervals": iv, "pointer_moves": 0}
	}
	iv := make([]int, 10+rand.IntN(20))
	for i := range iv {
		iv[i] = 80 + rand.IntN(280)
	}
	return map[string]any{"device": p.device, "form_ms": 8000 + rand.IntN(32000), "key_intervals": iv, "pointer_moves": 20 + rand.IntN(200)}
}

func (p *person) run(room string, opens time.Time, journeyDate string, wg *sync.WaitGroup, stats *counters) {
	defer wg.Done()
	// Humans drift in over the minute before opening; bot farms join in a burst just before it.
	var joinAt time.Time
	if p.bot {
		joinAt = opens.Add(-time.Duration(1500+rand.IntN(500)) * time.Millisecond)
	} else {
		joinAt = opens.Add(-time.Duration(rand.IntN(60_000)) * time.Millisecond)
		if rand.IntN(10) == 0 {
			joinAt = opens.Add(time.Duration(rand.IntN(5000)) * time.Millisecond) // a few arrive late
		}
	}
	time.Sleep(time.Until(joinAt))
	p.joinedAt = time.Now()
	var st status
	if code, err := call("POST", "/v1/waitrooms/"+room+"/join", p.token, p.signals(), p, &st); err != nil || code != 200 {
		p.outcome = fmt.Sprintf("join_failed_%d", code)
		return
	}
	draft := map[string]any{
		"train": p.journey.Train, "from": p.journey.From, "to": p.journey.To, "date": journeyDate, "class": "SL", "quota": "TQ",
		"passengers": []map[string]any{{"name": fmt.Sprintf("Swarm %d", p.n), "age": 25 + rand.IntN(40), "gender": []string{"M", "F"}[rand.IntN(2)], "berth_pref": ""}},
		"together":   false, "only_confirmed": false, "signals": p.signals(),
	}
	if code, err := call("PUT", "/v1/waitrooms/"+room+"/draft", p.token, draft, p, &st); err != nil || code != 200 {
		p.outcome = fmt.Sprintf("draft_failed_%d", code)
		return
	}
	poll := 2 * time.Second
	if p.bot {
		poll = 100 * time.Millisecond
	}
	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		switch st.Action {
		case "block":
			p.blocked = true
			p.outcome = "blocked"
			stats.blocked.Add(1)
			return
		case "challenge":
			if !p.challenged {
				p.challenged = true
				stats.challenged.Add(1)
			}
			if st.Challenge != nil {
				if !p.bot {
					time.Sleep(time.Duration(800+rand.IntN(1500)) * time.Millisecond) // a browser takes a moment
				}
				nonce := waitroom.SolvePoW(st.Challenge.Prefix, st.Challenge.Bits)
				call("POST", "/v1/waitrooms/"+room+"/challenge", p.token, map[string]string{"nonce": nonce}, p, &st)
				continue
			}
		}
		if st.Admitted {
			p.admittedAt = time.Now()
			var b struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  string `json:"error"`
				Msg    string `json:"message"`
			}
			t0 := time.Now()
			code, err := call("POST", "/v1/waitrooms/"+room+"/book", p.token, nil, p, &b)
			p.bookLatency = time.Since(t0)
			switch {
			case err != nil:
				p.outcome = "book_error"
			case code == 201:
				p.outcome = "held"
				if *pay && !p.bot {
					if payFor(p, b.ID) {
						p.outcome = "paid"
					}
				}
			case strings.Contains(b.Msg, "sold out"):
				p.outcome = "sold_out"
			case code == 403:
				p.outcome = "blocked"
			default:
				p.outcome = fmt.Sprintf("book_%d_%s", code, b.Error)
			}
			return
		}
		time.Sleep(poll)
		call("GET", "/v1/waitrooms/"+room+"/status", p.token, nil, p, &st)
	}
	p.outcome = "timed_out"
}

// gwTransport reuses connections to the gateway, like the API client does.
var gwTransport = &http.Transport{MaxIdleConns: 300, MaxIdleConnsPerHost: 300, MaxConnsPerHost: 300}

func payFor(p *person, bookingID string) bool {
	var r struct {
		CheckoutURL string `json:"checkout_url"`
	}
	if code, _ := call("POST", "/v1/bookings/"+bookingID+"/pay", p.token, nil, p, &r); code != 200 {
		return false
	}
	order := r.CheckoutURL[strings.LastIndex(r.CheckoutURL, "/")+1:]
	nr := &http.Client{Timeout: 20 * time.Second, Transport: gwTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := nr.PostForm(*gw+"/checkout/"+order+"/pay", url.Values{"method": {"upi"}, "outcome": {"success"}})
	for try := 0; err != nil && try < 3 && strings.Contains(err.Error(), "refused"); try++ {
		time.Sleep(time.Duration(100<<try) * time.Millisecond)
		res, err = nr.PostForm(*gw+"/checkout/"+order+"/pay", url.Values{"method": {"upi"}, "outcome": {"success"}})
	}
	if err != nil {
		return false
	}
	res.Body.Close()
	loc, _ := url.Parse(res.Header.Get("Location"))
	if loc == nil {
		return false
	}
	q := loc.Query()
	var b struct {
		Status string `json:"status"`
	}
	call("POST", "/v1/bookings/"+bookingID+"/verify", p.token,
		map[string]string{"order_id": q.Get("order_id"), "payment_id": q.Get("payment_id"), "signature": q.Get("signature")}, p, &b)
	return b.Status == "CONFIRMED"
}

type counters struct {
	blocked, challenged atomic.Int64
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	return d[int(float64(len(d)-1)*p)]
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)
	jd := *journey
	if jd == "" {
		jd = time.Now().AddDate(0, 0, 5+rand.IntN(56)).Format(time.DateOnly)
	}

	// Admin: pick journeys and open a drill room.
	var adm struct {
		AccessToken string `json:"access_token"`
	}
	call("POST", "/v1/auth/login", "", map[string]string{"identifier": "ADM00001", "password": "yatrax123"}, nil, &adm)
	if adm.AccessToken == "" {
		log.Fatal("admin login failed — is the stack up?")
	}
	var journeys []journeyChoice
	for _, rt := range [][2]string{{"MAS", "ERS"}, {"NDLS", "HWH"}, {"CSMT", "MAS"}, {"SBC", "NDLS"}, {"MAS", "NDLS"}, {"HWH", "MAS"}} {
		var res struct {
			Trains []struct {
				Number, Type string
				From, To     struct{ Code string }
				RunsOnDate   bool `json:"runs_on_date"`
				Classes      []struct{ Code string }
			}
		}
		call("GET", fmt.Sprintf("/v1/search?from=%s&to=%s&date=%s&class=SL", rt[0], rt[1], jd), "", nil, nil, &res)
		for _, t := range res.Trains {
			if !t.RunsOnDate || t.From.Code != rt[0] {
				continue
			}
			// The room is for trains leaving their first station on jd, so the train must start here
			// (many real trains only pass through, a day or more into their journey).
			var tr struct{ Origin string }
			call("GET", "/v1/trains/"+t.Number, "", nil, nil, &tr)
			if tr.Origin == rt[0] {
				journeys = append(journeys, journeyChoice{t.Number, t.From.Code, t.To.Code})
				break
			}
		}
	}
	if len(journeys) == 0 {
		log.Fatal("no Tatkal journeys found for " + jd)
	}
	var room struct {
		ID      string    `json:"id"`
		OpensAt time.Time `json:"opens_at"`
	}
	call("POST", "/v1/admin/waitrooms/drill", adm.AccessToken, map[string]any{
		"journey_date": jd, "ac": false, "opens_in_sec": int(opensIn.Seconds()), "admit_rate": *rate, "rush_minutes": 15,
	}, nil, &room)
	if room.ID == "" {
		log.Fatal("could not create the drill room")
	}
	log.Printf("room %s opens at %s; %d people (%.0f%% bots) will chase Tatkal SL on %d trains: %v",
		room.ID, room.OpensAt.Local().Format("15:04:05"), *users, *botFrac*100, len(journeys), journeys)

	// People: bots in farms of 10 sharing a device and network.
	people := make([]*person, *users)
	agents := 0
	for i := range people {
		p := &person{n: i, bot: rand.Float64() < *botFrac, journey: journeys[rand.IntN(len(journeys))]}
		if !p.bot && rand.Float64() < *agentFr/(1-*botFrac) {
			// A travel agent: four accounts on one phone, but a real person typing.
			agency := agents / 4
			agents++
			p.device, p.ip = fmt.Sprintf("agency-%d", agency), fmt.Sprintf("103.21.%d.%d", agency/250, agency%250)
			p.ua = "Mozilla/5.0 (Linux; Android 13) Chrome/129 Mobile"
		} else if p.bot {
			farm := i / 10
			p.device, p.ip = fmt.Sprintf("farm-%d", farm), fmt.Sprintf("10.66.%d.%d", farm/250, farm%250)
			p.ua = []string{"Go-http-client/1.1", "python-requests/2.32", "Mozilla/5.0 HeadlessChrome/130"}[rand.IntN(3)]
		} else {
			p.device, p.ip = fmt.Sprintf("dev-%d-%d", i, rand.IntN(1e9)), fmt.Sprintf("49.%d.%d.%d", rand.IntN(200), rand.IntN(250), 1+rand.IntN(250))
			p.ua = "Mozilla/5.0 (Linux; Android 14) Chrome/130 Mobile"
		}
		people[i] = p
	}
	log.Printf("logging in %d people…", len(people))
	sem := make(chan struct{}, *conc)
	var lw sync.WaitGroup
	var loginFail atomic.Int64
	for _, p := range people {
		lw.Add(1)
		sem <- struct{}{}
		go func() {
			defer lw.Done()
			defer func() { <-sem }()
			if err := login(p); err != nil {
				loginFail.Add(1)
			}
		}()
	}
	lw.Wait()
	log.Printf("logged in (%d failed); %s until opening", loginFail.Load(), time.Until(room.OpensAt).Round(time.Second))
	if time.Until(room.OpensAt) < 3*time.Second {
		log.Printf("warning: logins took longer than the lead time; use a larger -opens")
	}

	stats := &counters{}
	var wg sync.WaitGroup
	for _, p := range people {
		if p.token == "" {
			continue
		}
		wg.Add(1)
		go p.run(room.ID, room.OpensAt, jd, &wg, stats)
	}
	wg.Wait()
	elapsed := time.Since(room.OpensAt)

	// Report.
	outcomes := map[string]map[string]int{"human": {}, "bot": {}}
	var lat []time.Duration
	for _, p := range people {
		kind := "human"
		if p.bot {
			kind = "bot"
		}
		outcomes[kind][p.outcome]++
		if p.bookLatency > 0 {
			lat = append(lat, p.bookLatency)
		}
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	var mon struct {
		Stats      map[string]int64   `json:"stats"`
		SoldOut    map[string]float64 `json:"sold_out_seconds"`
		Blocked    int64              `json:"blocked"`
		Challenged int64              `json:"challenged"`
		Verified   int64              `json:"verified"`
		TopReasons map[string]int64   `json:"top_reasons"`
	}
	call("GET", "/v1/admin/waitrooms/"+room.ID+"/monitor", adm.AccessToken, nil, nil, &mon)
	var integ map[string]any
	call("GET", "/v1/admin/integrity", adm.AccessToken, nil, nil, &integ)

	fmt.Println()
	fmt.Println("════════ Tatkal drill report ════════")
	netErrors.Range(func(k, v any) bool {
		fmt.Printf("net error     %dx %s\n", v.(*atomic.Int64).Load(), k)
		return true
	})
	fmt.Printf("room          %s   (everyone done %.0fs after opening)\n", room.ID, elapsed.Seconds())
	for _, kind := range []string{"human", "bot"} {
		keys := make([]string, 0)
		for k := range outcomes[kind] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := []string{}
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s %d", k, outcomes[kind][k]))
		}
		fmt.Printf("%-13s %s\n", kind+"s", strings.Join(parts, " · "))
	}
	fmt.Printf("bot detector  %d blocked, %d verifying, %d verified; top reasons %v\n", mon.Blocked, mon.Challenged, mon.Verified, mon.TopReasons)
	so := []string{}
	for k, v := range mon.SoldOut {
		so = append(so, fmt.Sprintf("%s in %.0fs", k, v))
	}
	sort.Strings(so)
	fmt.Printf("sold out      %s\n", strings.Join(so, ", "))
	fmt.Printf("book latency  p50 %v  p95 %v  p99 %v  (%d bookings attempted)\n",
		pct(lat, 0.5).Round(time.Millisecond), pct(lat, 0.95).Round(time.Millisecond), pct(lat, 0.99).Round(time.Millisecond), len(lat))
	fmt.Printf("integrity     %v\n", integ)
	if v, ok := integ["overlapping_allocations"].(float64); !ok || v != 0 {
		fmt.Println("FAIL: a berth was sold twice")
		os.Exit(1)
	}
	if v, ok := integ["paid_without_ticket"].(float64); !ok || v != 0 {
		fmt.Println("FAIL: money taken without a ticket")
		os.Exit(1)
	}
	fmt.Println("PASS: zero double bookings, no money taken without a ticket")
}
