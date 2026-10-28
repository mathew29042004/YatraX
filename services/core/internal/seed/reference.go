package seed

// TrainType mirrors the train_types table.
type TrainType struct {
	Code           string
	Name           string
	AvgSpeedKmph   int
	FareMultiplier float64
	Superfast      bool
	FlexiFare      bool
	StopImportance int // stops at stations with importance <= this
	MajorHaltMin   int
	Pantry         bool
}

var trainTypes = []TrainType{
	{"RAJDHANI", "Rajdhani Express", 78, 1.30, true, true, 1, 5, true},
	{"DURONTO", "Duronto Express", 76, 1.20, true, true, 1, 5, true},
	{"VANDE_BHARAT", "Vande Bharat Express", 82, 1.40, true, false, 1, 2, false},
	{"SHATABDI", "Shatabdi Express", 74, 1.25, true, true, 1, 3, false},
	{"JAN_SHATABDI", "Jan Shatabdi Express", 62, 1.00, true, false, 2, 3, false},
	{"GARIB_RATH", "Garib Rath Express", 64, 0.80, true, false, 1, 5, false},
	{"SUPERFAST", "Superfast Express", 58, 1.00, true, false, 2, 5, true},
	{"MAIL_EXPRESS", "Mail / Express", 50, 1.00, false, false, 3, 5, false},
	{"PASSENGER", "Passenger / MEMU", 34, 0.60, false, false, 3, 2, false},
}

func trainType(code string) TrainType {
	for _, t := range trainTypes {
		if t.Code == code {
			return t
		}
	}
	panic("unknown train type " + code)
}

// CoachClass mirrors coach_classes. Fares are close to Indian Railways' published
// per-km base fares; Tatkal rules follow the real percentage + min/max caps.
// Minimum chargeable distances are set by migration 0002.
type CoachClass struct {
	Code, Name        string
	Bookable          bool
	Seats             int
	Layout            string
	Prefix            string
	Sort              int
	AC                bool
	FarePerKm         float64
	ReservationCharge int
	SuperfastCharge   int
	GSTPct            float64
	TatkalPct         *float64
	TatkalMin         *int
	TatkalMax         *int
}

func f(v float64) *float64 { return &v }
func i(v int) *int         { return &v }

var coachClasses = []CoachClass{
	{"1A", "First AC", true, 24, "sleeper_4", "H", 1, true, 3.50, 60, 75, 5, nil, nil, nil},
	{"EC", "Executive Chair Car", true, 56, "sitting_4", "E", 2, true, 2.60, 60, 75, 5, f(30), i(400), i(500)},
	{"2A", "AC 2 Tier", true, 48, "sleeper_6", "A", 3, true, 2.10, 50, 45, 5, f(30), i(400), i(500)},
	{"3A", "AC 3 Tier", true, 72, "sleeper_8", "B", 4, true, 1.45, 40, 45, 5, f(30), i(300), i(400)},
	{"3E", "AC 3 Economy", true, 80, "sleeper_8", "M", 5, true, 1.35, 40, 45, 5, f(30), i(300), i(400)},
	{"CC", "AC Chair Car", true, 78, "sitting_5", "C", 6, true, 1.30, 40, 45, 5, f(30), i(100), i(200)},
	{"SL", "Sleeper", true, 72, "sleeper_8", "S", 7, false, 0.55, 20, 30, 0, f(10), i(100), i(200)},
	{"2S", "Second Sitting", true, 108, "sitting_6", "D", 8, false, 0.30, 15, 15, 0, f(10), i(10), i(15)},
	{"GN", "General (Unreserved)", false, 90, "none", "GEN", 9, false, 0.20, 0, 0, 0, nil, nil, nil},
	{"PC", "Pantry Car", false, 0, "none", "PC", 10, false, 0, 0, 0, 0, nil, nil, nil},
	{"SLR", "Seating cum Luggage Rake", false, 30, "none", "SLR", 11, false, 0, 0, 0, 0, nil, nil, nil},
	{"EOG", "End-on Generator Car", false, 0, "none", "EOG", 12, false, 0, 0, 0, 0, nil, nil, nil},
}

// minDistanceKm is the shortest distance a ticket in each class is charged for.
var minDistanceKm = map[string]int{"1A": 300, "EC": 150, "2A": 300, "3A": 300, "3E": 300, "CC": 150, "SL": 200, "2S": 50}

// fareTaper: the per-km rate drops on long journeys.
var fareTaper = []struct {
	From, To int
	Factor   float64
}{
	{1, 300, 1.000},
	{301, 700, 0.920},
	{701, 1200, 0.850},
	{1201, 2000, 0.780},
	{2001, 99999, 0.700},
}

// composition gives the coach classes and counts for each train type, in rake order.
// Ranges are [min, max]; the generator picks a count per train.
type coachGroup struct {
	Class    string
	Min, Max int
}

var compositions = map[string][]coachGroup{
	"RAJDHANI":     {{"EOG", 1, 1}, {"1A", 1, 1}, {"2A", 3, 5}, {"PC", 1, 1}, {"3A", 8, 11}, {"EOG", 1, 1}},
	"DURONTO":      {{"EOG", 1, 1}, {"1A", 0, 1}, {"2A", 2, 3}, {"3A", 6, 9}, {"PC", 1, 1}, {"SL", 2, 4}, {"EOG", 1, 1}},
	"VANDE_BHARAT": {{"CC", 3, 7}, {"EC", 1, 2}, {"CC", 3, 7}},
	"SHATABDI":     {{"EOG", 1, 1}, {"EC", 1, 2}, {"CC", 7, 11}, {"EOG", 1, 1}},
	"JAN_SHATABDI": {{"SLR", 1, 1}, {"GN", 1, 2}, {"CC", 1, 3}, {"2S", 6, 10}, {"SLR", 1, 1}},
	"GARIB_RATH":   {{"EOG", 1, 1}, {"3E", 12, 16}, {"CC", 0, 2}, {"EOG", 1, 1}},
	"SUPERFAST":    {{"SLR", 1, 1}, {"GN", 2, 2}, {"1A", 0, 1}, {"2A", 1, 2}, {"3A", 4, 6}, {"3E", 0, 2}, {"PC", 1, 1}, {"SL", 6, 9}, {"GN", 1, 2}, {"SLR", 1, 1}},
	"MAIL_EXPRESS": {{"SLR", 1, 1}, {"GN", 2, 3}, {"2A", 1, 2}, {"3A", 2, 5}, {"SL", 7, 11}, {"GN", 1, 2}, {"SLR", 1, 1}},
	"PASSENGER":    {{"SLR", 1, 1}, {"2S", 2, 4}, {"GN", 4, 7}, {"SLR", 1, 1}},
}

// stateLanguage is the regional language used for announcements and as the
// most likely mother tongue of people living near a station.
var stateLanguage = map[string]string{
	"Tamil Nadu": "ta", "Puducherry": "ta", "Kerala": "ml", "Karnataka": "kn",
	"Andhra Pradesh": "te", "Telangana": "te", "Maharashtra": "mr", "Goa": "kok",
	"Gujarat": "gu", "Rajasthan": "hi", "Madhya Pradesh": "hi", "Uttar Pradesh": "hi",
	"Delhi": "hi", "Haryana": "hi", "Punjab": "pa", "Chandigarh": "pa",
	"Himachal Pradesh": "hi", "Jammu & Kashmir": "ur", "Uttarakhand": "hi", "Bihar": "hi",
	"Jharkhand": "hi", "Chhattisgarh": "hi", "West Bengal": "bn", "Odisha": "or",
	"Assam": "as", "Tripura": "bn", "Nagaland": "en", "Arunachal Pradesh": "en",
}
