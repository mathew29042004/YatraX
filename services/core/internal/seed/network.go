package seed

import "github.com/yatrax/yatrax/services/core/internal/network"

// Network builds the in-memory network for a generated world without a database,
// matching what network.Load reads back after Write.
func Network(w *World) *network.Network {
	n := network.New()
	for _, s := range w.Stations {
		n.Stations[s.Code] = &network.Station{Code: s.Code, Name: s.Name, State: s.State, Zone: s.Zone,
			Division: s.Division, Language: s.Language, Lat: s.Lat, Lon: s.Lon, Importance: s.Importance, Platforms: s.Platforms}
	}
	for _, t := range trainTypes {
		n.TrainTypes[t.Code] = &network.TrainType{Code: t.Code, Name: t.Name, FareMultiplier: t.FareMultiplier,
			Superfast: t.Superfast, FlexiFare: t.FlexiFare}
	}
	for _, c := range coachClasses {
		n.Classes[c.Code] = &network.CoachClass{Code: c.Code, Name: c.Name, Bookable: c.Bookable, AC: c.AC, Seats: c.Seats,
			Layout: c.Layout, Sort: c.Sort, FarePerKm: c.FarePerKm, ReservationCharge: c.ReservationCharge,
			SuperfastCharge: c.SuperfastCharge, GSTPct: c.GSTPct, TatkalPct: c.TatkalPct, TatkalMin: c.TatkalMin,
			TatkalMax: c.TatkalMax, MinDistanceKm: minDistanceKm[c.Code]}
	}
	for _, b := range fareTaper {
		n.Taper = append(n.Taper, network.TaperBand{FromKm: b.From, ToKm: b.To, Factor: b.Factor})
	}
	for _, t := range w.Trains {
		nt := &network.Train{Number: t.Number, Name: t.Name, Type: t.Type, Origin: t.Origin, Destination: t.Destination,
			RunningDays: t.Days, DistKm: t.DistKm, Pantry: t.Pantry, Return: t.Return}
		for i, s := range t.Stops {
			nt.Stops = append(nt.Stops, network.Stop{Seq: i + 1, Station: s.Station, Arr: s.Arr, Dep: s.Dep, DistKm: s.DistKm, Platform: s.Platform})
		}
		for _, c := range t.Coaches {
			nt.Coaches = append(nt.Coaches, network.Coach{No: c.No, Class: c.Class, Position: c.Pos})
		}
		n.Add(nt)
	}
	return n
}
