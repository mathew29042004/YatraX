// Package catering is food to your berth: vendors' menus, orders placed for delivery at
// a station on the journey and timed by the train's live ETA, the order flow from
// placed to delivered, ratings and hygiene complaints, and the vendors' payout ledger.
package catering

import (
	"context"
	"slices"
)

type item struct {
	name, desc, category, diet string
	price, prep                int
}

// A starter menu for each cuisine; vendors edit theirs from there. Prices include GST.
var templates = map[string][]item{
	"Biryani": {
		{"Chicken Dum Biryani", "Seeraga samba rice, raita, brinjal curry", "meal", "nonveg", 220, 30},
		{"Mutton Biryani", "Slow-cooked with onion raita", "meal", "nonveg", 280, 35},
		{"Egg Biryani", "Two boiled eggs, raita", "meal", "egg", 160, 25},
		{"Veg Biryani", "Seasonal vegetables, raita", "meal", "veg", 140, 25},
	},
	"South Indian": {
		{"Idli Vada Combo", "Three idlis, one vada, sambar, two chutneys", "meal", "veg", 90, 15},
		{"Masala Dosa", "With sambar and coconut chutney", "meal", "veg", 110, 15},
		{"South Indian Meals", "Rice, sambar, rasam, poriyal, curd, papad", "meal", "veg", 150, 20},
		{"Filter Coffee", "", "beverage", "veg", 30, 5},
	},
	"Chettinad": {
		{"Chettinad Chicken Meals", "Rice, chicken chettinad, rasam, curd", "meal", "nonveg", 230, 30},
		{"Kothu Parotta", "Shredded parotta with egg and salna", "meal", "egg", 150, 20},
	},
	"Kerala": {
		{"Kerala Meals", "Matta rice, sambar, avial, thoran, pickle", "meal", "veg", 160, 20},
		{"Appam with Stew", "Two appams, vegetable stew", "meal", "veg", 120, 20},
		{"Beef Fry with Parotta", "", "meal", "nonveg", 190, 25},
	},
	"Seafood": {
		{"Fish Curry Meals", "Rice, fish curry, fry, thoran", "meal", "nonveg", 240, 30},
		{"Prawn Fry", "", "snack", "nonveg", 260, 25},
	},
	"Fish": {
		{"Macher Jhol with Rice", "Rohu in light curry", "meal", "nonveg", 200, 30},
	},
	"Andhra": {
		{"Andhra Meals", "Rice, pappu, gongura, rasam, curd", "meal", "veg", 150, 20},
		{"Pesarattu", "Green-gram dosa with ginger chutney", "meal", "veg", 90, 15},
	},
	"Udupi": {
		{"Udupi Thali", "Rice, saaru, palya, kosambari, payasa", "meal", "veg", 140, 20},
		{"Set Dosa", "Three soft dosas, sagu", "meal", "veg", 90, 15},
	},
	"North Indian": {
		{"Veg Thali", "Two rotis, dal, sabzi, rice, salad", "meal", "veg", 150, 20},
		{"Paneer Butter Masala with Roti", "", "meal", "veg", 190, 25},
		{"Chicken Curry with Rice", "", "meal", "nonveg", 210, 30},
		{"Masala Chai", "", "beverage", "veg", 20, 5},
	},
	"Punjabi": {
		{"Chole Bhature", "", "meal", "veg", 120, 15},
		{"Amritsari Kulcha", "With chole and pickle", "meal", "veg", 130, 20},
	},
	"Tandoor": {
		{"Tandoori Chicken (half)", "", "meal", "nonveg", 260, 35},
		{"Paneer Tikka", "", "snack", "veg", 200, 25},
	},
	"Maharashtrian": {
		{"Vada Pav (2)", "", "snack", "veg", 50, 10},
		{"Misal Pav", "", "meal", "veg", 90, 15},
	},
	"Gujarati": {
		{"Gujarati Thali", "Rotli, dal, shaak, kadhi, rice, farsan", "meal", "veg", 170, 20},
		{"Dhokla", "", "snack", "veg", 60, 10},
	},
	"Jain": {
		{"Jain Thali", "No onion, garlic or root vegetables", "meal", "jain", 170, 25},
		{"Jain Sev Puri", "", "snack", "jain", 70, 10},
	},
	"Bengali": {
		{"Bengali Veg Thali", "Rice, dal, aloo posto, begun bhaja", "meal", "veg", 150, 20},
		{"Kosha Mangsho with Luchi", "", "meal", "nonveg", 260, 35},
	},
	"Odia": {
		{"Dalma with Rice", "", "meal", "veg", 120, 20},
	},
	"Assamese": {
		{"Assamese Thali", "Rice, dal, khar, aloo pitika", "meal", "veg", 160, 25},
	},
	"Goan": {
		{"Goan Fish Curry Rice", "", "meal", "nonveg", 250, 30},
	},
	"Kashmiri": {
		{"Rajma Chawal", "", "meal", "veg", 140, 20},
	},
	"Wazwan": {
		{"Rogan Josh with Rice", "", "meal", "nonveg", 300, 40},
	},
	"North East": {
		{"Steamed Momos (8)", "", "snack", "veg", 100, 15},
	},
	"Chaat": {
		{"Pani Puri", "", "snack", "veg", 50, 10},
		{"Aloo Tikki Chaat", "", "snack", "veg", 70, 10},
	},
	"Snacks": {
		{"Samosa (2)", "", "snack", "veg", 40, 10},
		{"Veg Sandwich", "", "snack", "veg", 60, 10},
	},
	"Sweets": {
		{"Rasgulla (2)", "", "sweet", "veg", 50, 5},
		{"Gulab Jamun (2)", "", "sweet", "veg", 50, 5},
	},
}

var staples = []item{
	{"Packaged Drinking Water (1 L)", "Rail Neer", "beverage", "veg", 15, 1},
	{"Curd Rice", "", "meal", "veg", 80, 10},
}

// EnsureMenu gives a vendor a starter menu from their cuisines the first time it's needed.
func (s *Service) EnsureMenu(ctx context.Context, vendorID string) error {
	var n int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM menu_items WHERE vendor_id = $1`, vendorID).Scan(&n); err != nil || n > 0 {
		return err
	}
	var cuisines []string
	var vegOnly bool
	if err := s.DB.QueryRow(ctx, `SELECT cuisines, veg_only FROM vendors WHERE id = $1`, vendorID).Scan(&cuisines, &vegOnly); err != nil {
		return err
	}
	var items []item
	seen := map[string]bool{}
	for _, c := range append(cuisines, "") {
		list := templates[c]
		if c == "" {
			list = staples
		}
		for _, it := range list {
			if seen[it.name] || (vegOnly && (it.diet == "nonveg" || it.diet == "egg")) {
				continue
			}
			seen[it.name] = true
			items = append(items, it)
		}
	}
	slices.SortStableFunc(items, func(a, b item) int {
		order := map[string]int{"meal": 0, "snack": 1, "sweet": 2, "beverage": 3}
		return order[a.category] - order[b.category]
	})
	for _, it := range items {
		if _, err := s.DB.Exec(ctx, `INSERT INTO menu_items (vendor_id, name, description, category, diet, price, prep_min) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			vendorID, it.name, it.desc, it.category, it.diet, it.price, it.prep); err != nil {
			return err
		}
	}
	return nil
}
