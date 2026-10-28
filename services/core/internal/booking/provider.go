package booking

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/inventory"
)

// Provider answers availability from real inventory for runs that exist, and falls
// back to the demand model for runs nobody has opened yet.
type Provider struct{ S *Service }

func (p Provider) Status(ctx context.Context, r availability.Request, now time.Time) availability.Status {
	if st, ok := availability.Gate(p.S.Net, r, now); !ok {
		return st
	}
	runID, ok := p.S.runID(r.Train.Number, r.OriginDate)
	if !ok {
		return p.S.Sim.Status(ctx, r, now)
	}
	if d := p.S.disruptionFor(runID); d != nil {
		if hit, _ := d.affects(r.FromIdx, r.ToIdx, r.Train); hit {
			return availability.Status{Code: availability.NotRunning, Label: "Cancelled"}
		}
	}
	cat, err := p.S.catalog(p.S.runTrain(runID, r.Train), r.Class)
	if err != nil {
		return p.S.Sim.Status(ctx, r, now)
	}
	occ, queue, err := p.S.cachedOccupancy(ctx, runID, r.Class)
	if err != nil {
		slog.Warn("availability: falling back to simulation", "run", runID, "err", err)
		return p.S.Sim.Status(ctx, r, now)
	}
	if free := cat.FreeCount(occ, r.Quota, r.FromIdx, r.ToIdx); free > 0 {
		return availability.AvailableStatus(free)
	}
	if p.S.chartPrepared(runID) {
		if free := cat.FreeCount(occ, r.Quota, r.FromIdx, r.ToIdx) + p.anyQuotaFree(cat, occ, r); free > 0 {
			return availability.AvailableStatus(free)
		}
		return availability.Status{Code: availability.Regret, Label: "Chart prepared"}
	}
	q := queue[r.Quota]
	if r.Quota == "GN" && cat.FreeRAC(occ, r.FromIdx, r.ToIdx) > 0 {
		return availability.RACStatus(q.rac + 1)
	}
	capacity := float64(cat.Capacity(r.Quota))
	wl := q.wl + 1
	if wl > availability.WaitlistLimit(r.Quota, capacity) {
		return availability.Status{Code: availability.Regret, Label: "Regret"}
	}
	daysOut := r.OriginDate.Sub(availability.Midnight(now)).Hours() / 24
	_, racCap := p.S.Sim.Capacity(r.Train, r.Class, r.Quota)
	return availability.WaitlistStatus(wl, capacity, racCap, daysOut)
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// pnrMultiplier is prime and shares no factor with pnrSpace, so pnrFrom is a
// bijection on [0, pnrSpace): every sequence number maps to a distinct PNR.
const (
	pnrSpace      = 9_000_000_000
	pnrMultiplier = 7_368_787
	pnrOffset     = 1_000_000_000
)

func pnrFrom(seq int64) string {
	v := (seq%pnrSpace)*pnrMultiplier%pnrSpace + pnrOffset
	return formatPNR(v)
}

func formatPNR(v int64) string {
	b := make([]byte, 10)
	for i := 9; i >= 0; i-- {
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b)
}

func nextPNRs(ctx context.Context, tx pgx.Tx, n int) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT nextval('pnr_seq') FROM generate_series(1, $1)`, n)
	if err != nil {
		return nil, err
	}
	seqs, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	out := make([]string, len(seqs))
	for i, s := range seqs {
		out[i] = pnrFrom(s)
	}
	return out, nil
}

// anyQuotaFree counts other quotas' berths, which are open to everyone once the chart is out.
func (p Provider) anyQuotaFree(cat *inventory.Catalog, occ inventory.Occupancy, r availability.Request) int {
	n := 0
	for _, q := range availability.Quotas {
		if q != r.Quota {
			n += cat.FreeCount(occ, q, r.FromIdx, r.ToIdx)
		}
	}
	return n
}
