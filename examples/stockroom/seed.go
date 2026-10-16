package main

import (
	"log"
	"math/rand"
	"time"
)

// The fixture. A stockroom for an internal operations team: five aisles, forty
// lines, and enough history that every screen has something to say on a fresh
// install.
//
// Seeding matters more here than in most sample apps, because this one lives on
// scratch storage by default — the register is rebuilt on every deploy, so an
// empty first screen would be the *normal* first screen, and an empty table is
// the worst possible introduction to a product about counting things.
type fixture struct {
	sku      string
	name     string
	category string
	shelf    int
	bin      int
	supplier string
	qty      int // where the ledger has to land
	reorder  int
	cost     int // cents per unit
	status   string
	notes    string
}

var aisles = map[string]string{
	"Electronics": "A", "Consumables": "B", "Tools": "C", "Safety": "D", "Spares": "E",
}

var fixtures = []fixture{
	// Electronics — aisle A
	{"EL-1001", "USB-C cable, 2 m, 100 W", "Electronics", 1, 4, "Dalstrom Supply", 46, 20, 890, "active", ""},
	{"EL-1002", "HDMI 2.1 cable, 3 m", "Electronics", 1, 6, "Dalstrom Supply", 18, 12, 1240, "active", ""},
	{"EL-1003", "Wireless mouse, silent", "Electronics", 2, 1, "Northgate IT", 9, 10, 2450, "active", "Standard issue for new starters."},
	{"EL-1004", "Laptop dock, dual DP", "Electronics", 2, 3, "Northgate IT", 4, 6, 18900, "on_order", "PO 4471, due end of month."},
	{"EL-1005", "Mechanical keyboard, UK", "Electronics", 2, 5, "Northgate IT", 22, 8, 6400, "active", ""},
	{"EL-1006", "1080p webcam with shutter", "Electronics", 3, 2, "Northgate IT", 11, 6, 4900, "active", ""},
	{"EL-1007", "Surge-protected power strip, 6-way", "Electronics", 3, 5, "Dalstrom Supply", 27, 10, 1590, "active", ""},
	{"EL-1008", "NVMe SSD, 1 TB", "Electronics", 4, 1, "Northgate IT", 6, 4, 8700, "active", "Bench spares only — not for issue."},

	// Consumables — aisle B
	{"CO-2001", "Nitrile gloves, medium, box of 100", "Consumables", 1, 2, "Vessel Hygiene", 34, 12, 1180, "active", ""},
	{"CO-2002", "Isopropyl wipes, tub of 200", "Consumables", 1, 4, "Vessel Hygiene", 8, 10, 940, "active", ""},
	{"CO-2003", "Kapton tape, 12 mm", "Consumables", 1, 7, "Ferrule & Co", 21, 8, 620, "active", ""},
	{"CO-2004", "Solder wire, 0.7 mm, 250 g", "Consumables", 2, 2, "Ferrule & Co", 5, 6, 2870, "active", ""},
	{"CO-2005", "Label tape, 12 mm black on white", "Consumables", 2, 4, "Ferrule & Co", 40, 15, 1450, "active", "Fits the bench printer and the handheld."},
	{"CO-2006", "AA alkaline batteries, pack of 24", "Consumables", 2, 6, "Vessel Hygiene", 12, 10, 1690, "active", ""},
	{"CO-2007", "Fan dust filters, 120 mm", "Consumables", 3, 1, "Ferrule & Co", 0, 8, 340, "active", "Out since the rack refresh."},
	{"CO-2008", "Cable ties, 200 mm, bag of 100", "Consumables", 3, 3, "Dalstrom Supply", 63, 20, 410, "active", ""},

	// Tools — aisle C
	{"TL-3001", "Torque screwdriver, 0.4–2 Nm", "Tools", 1, 1, "Ferrule & Co", 4, 2, 11200, "active", "Calibrated March; sticker on the handle."},
	{"TL-3002", "RJ45 crimp tool", "Tools", 1, 3, "Ferrule & Co", 3, 2, 4300, "active", ""},
	{"TL-3003", "Digital calipers, 150 mm", "Tools", 1, 5, "Ferrule & Co", 2, 2, 5600, "active", ""},
	{"TL-3004", "Heat gun, variable", "Tools", 2, 1, "Ferrule & Co", 2, 1, 7400, "active", ""},
	{"TL-3005", "Multimeter, true RMS", "Tools", 2, 3, "Ferrule & Co", 5, 3, 9800, "active", ""},
	{"TL-3006", "Network cable tester", "Tools", 2, 5, "Northgate IT", 1, 2, 6700, "active", "One is out with the field team."},
	{"TL-3007", "Hex key set, metric", "Tools", 3, 2, "Ferrule & Co", 7, 3, 2200, "active", ""},
	{"TL-3008", "Anti-static bench mat", "Tools", 3, 4, "Vessel Hygiene", 6, 3, 3900, "active", ""},

	// Safety — aisle D
	{"SF-4001", "Safety glasses, clear, anti-fog", "Safety", 1, 1, "Vessel Hygiene", 28, 15, 780, "active", ""},
	{"SF-4002", "Ear defenders, 30 dB", "Safety", 1, 3, "Vessel Hygiene", 9, 6, 2140, "active", ""},
	{"SF-4003", "First aid kit refill pack", "Safety", 1, 5, "Vessel Hygiene", 3, 4, 3250, "active", "Checked monthly against the wall kit."},
	{"SF-4004", "Fire blanket, 1.2 × 1.2 m", "Safety", 2, 1, "Vessel Hygiene", 4, 2, 1870, "active", ""},
	{"SF-4005", "ESD wrist strap, coiled lead", "Safety", 2, 3, "Vessel Hygiene", 14, 8, 960, "active", ""},
	{"SF-4006", "Hi-vis vest, size L", "Safety", 2, 5, "Vessel Hygiene", 17, 10, 640, "active", ""},
	{"SF-4007", "Eyewash bottle, 500 ml", "Safety", 3, 1, "Vessel Hygiene", 6, 6, 1120, "active", ""},
	{"SF-4008", "Chemical spill kit, 20 L", "Safety", 3, 3, "Vessel Hygiene", 2, 1, 8900, "active", ""},

	// Spares — aisle E
	{"SP-5001", "Case fan, 120 mm PWM", "Spares", 1, 2, "Dalstrom Supply", 24, 10, 1340, "active", ""},
	{"SP-5002", "Power supply, 750 W", "Spares", 1, 4, "Dalstrom Supply", 3, 3, 12400, "active", ""},
	{"SP-5003", "DDR4 SODIMM, 16 GB", "Spares", 2, 1, "Northgate IT", 11, 6, 5200, "active", ""},
	{"SP-5004", "Rack rails, 1U sliding", "Spares", 2, 3, "Dalstrom Supply", 8, 4, 6100, "active", ""},
	{"SP-5005", "SFP+ module, 10G SR", "Spares", 2, 5, "Northgate IT", 0, 4, 9400, "on_order", "PO 4468 — the switch refresh took the last four."},
	{"SP-5006", "Caster wheel, braked, 75 mm", "Spares", 3, 2, "Dalstrom Supply", 19, 8, 890, "active", ""},
	{"SP-5007", "Door closer spring", "Spares", 3, 4, "Dalstrom Supply", 5, 2, 2600, "active", ""},
	{"SP-5008", "Shelf bracket, heavy duty", "Spares", 3, 6, "Dalstrom Supply", 12, 6, 1450, "retired", "Superseded by the deeper bracket; use SP-5009 when it lands."},
}

// Who moved it. Roles rather than names, and deliberately so: Toft tells
// an app that a viewer is present and never who they are, so a register that
// showed invented staff names would be claiming an attribution the platform
// does not provide. A shift is something the app genuinely knows.
var seedActors = []string{"goods-in", "day shift", "late shift", "bench", "stock audit"}

var seedNotes = map[string][]string{
	"receipt":    {"PO delivery", "restock from Dalstrom", "back-order arrived", "transferred from the annexe"},
	"issue":      {"issued to the bench", "field kit", "new starter setup", "rack build", "issued to facilities"},
	"adjustment": {"counted on the shelf", "recount after the audit", "damaged, written off", "found behind the racking"},
	"return":     {"returned unused", "came back from the field", "wrong part, returned"},
}

// SeedIfEmpty fills a fresh register and does nothing to one that already has
// rows. It is safe to call on every boot.
func (s *Store) SeedIfEmpty() error {
	if s.count("items") > 0 {
		return nil
	}
	// A fixed seed, so two people looking at two installs of this example are
	// looking at the same stockroom and can talk about the same numbers.
	rng := rand.New(rand.NewSource(20260814))
	now := time.Now().UTC()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	movements := 0
	for _, f := range fixtures {
		bin := aisles[f.category] + itoa2(f.shelf) + "-" + itoa2(f.bin)
		created := now.AddDate(0, 0, -(90 + rng.Intn(400)))
		res, err := tx.Exec(`INSERT INTO items
			(sku, name, category, bin, supplier, quantity, reorder_point, unit_cost_cents, status, notes, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			f.sku, f.name, f.category, bin, f.supplier, f.qty, f.reorder, f.cost,
			f.status, f.notes, created.Format(time.RFC3339), now.Format(time.RFC3339))
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()

		// Walk the last ten weeks, then back-solve the opening count so the
		// ledger sums to the quantity the fixture declares. A seeded history
		// whose lines do not add up to the shelf is worse than no history: the
		// first thing anybody does with a register is check that it balances.
		type line struct {
			kind string
			qty  int
			at   time.Time
		}
		var walk []line
		delta := 0
		for d := 70; d > 0; d-- {
			if rng.Intn(100) >= 22 {
				continue
			}
			at := now.AddDate(0, 0, -d).Add(time.Duration(8+rng.Intn(9)) * time.Hour).
				Add(time.Duration(rng.Intn(60)) * time.Minute)
			var kind string
			var qty int
			switch n := rng.Intn(100); {
			case n < 55:
				kind, qty = "issue", -(1 + rng.Intn(max(1, f.qty/6+1)))
			case n < 82:
				kind, qty = "receipt", 1+rng.Intn(max(1, f.qty/4+2))
			case n < 92:
				kind, qty = "return", 1+rng.Intn(2)
			default:
				kind, qty = "adjustment", rng.Intn(5)-2
			}
			if qty == 0 {
				continue
			}
			walk = append(walk, line{kind, qty, at})
			delta += qty
		}
		opening := f.qty - delta
		if opening < 1 {
			opening = 1 + rng.Intn(3)
			// Re-balance on the last line rather than on the first: the opening
			// count is the one number an auditor would expect to be round.
			if len(walk) > 0 {
				walk[len(walk)-1].qty += f.qty - (opening + delta)
				if walk[len(walk)-1].qty == 0 {
					walk = walk[:len(walk)-1]
				}
			}
		}

		if _, err := tx.Exec(`INSERT INTO movements (item_id, kind, qty, note, actor, at) VALUES (?,?,?,?,?,?)`,
			id, "receipt", opening, "opening count", "stock audit",
			created.Add(2*time.Hour).Format(time.RFC3339)); err != nil {
			return err
		}
		movements++
		for _, l := range walk {
			notes := seedNotes[l.kind]
			if _, err := tx.Exec(`INSERT INTO movements (item_id, kind, qty, note, actor, at) VALUES (?,?,?,?,?,?)`,
				id, l.kind, l.qty, notes[rng.Intn(len(notes))],
				seedActors[rng.Intn(len(seedActors))], l.at.Format(time.RFC3339)); err != nil {
				return err
			}
			movements++
		}
	}

	if _, err := tx.Exec(`INSERT INTO activity (at, actor, action, subject, detail) VALUES (?,?,?,?,?)`,
		now.Format(time.RFC3339), "stockroom", "seeded the register",
		"40 lines across 5 aisles",
		"This install started empty, so the example data was written on first boot."); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("stockroom: seeded %d items and %d movements", len(fixtures), movements)
	return nil
}

func itoa2(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
