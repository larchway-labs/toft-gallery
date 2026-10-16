package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the register itself: one SQLite file, opened once.
//
// modernc.org/sqlite rather than mattn/go-sqlite3 because Toft builds Go
// apps with CGO off. A cgo driver compiles fine on a laptop and then fails the
// deploy with a linker error that says nothing about the cause.
type Store struct{ db *sql.DB }

// Categories is the fixed order every chart, filter and report reads.
//
// Fixed, and never sorted by size at render time: a colour follows the entity,
// not its rank, so a filter that drops one category must not repaint the rest.
var Categories = []string{"Electronics", "Consumables", "Tools", "Safety", "Spares"}

// MovementKinds are the four ways stock moves. Receipt and Return add; Issue
// removes; Adjustment is the count after somebody actually looked at the shelf.
var MovementKinds = []string{"receipt", "issue", "adjustment", "return"}

type Item struct {
	ID            int64
	SKU           string
	Name          string
	Category      string
	Bin           string
	Supplier      string
	Quantity      int
	ReorderPoint  int
	UnitCostCents int
	Status        string // active | on_order | retired
	Notes         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (i Item) ValueCents() int { return i.Quantity * i.UnitCostCents }

// State is the shelf's answer, derived rather than stored: a status column an
// operator could set by hand would drift from the count on the next movement.
func (i Item) State() string {
	switch {
	case i.Status == "retired":
		return "retired"
	case i.Quantity == 0:
		return "out"
	case i.Quantity <= i.ReorderPoint:
		return "low"
	case i.Status == "on_order":
		return "on_order"
	}
	return "ok"
}

type Movement struct {
	ID       int64
	ItemID   int64
	SKU      string
	ItemName string
	Category string
	Kind     string
	Qty      int // signed
	Note     string
	Actor    string
	At       time.Time
}

type Activity struct {
	ID      int64
	At      time.Time
	Actor   string
	Action  string
	Subject string
	Detail  string
}

type Delivery struct {
	ID      int64
	At      time.Time
	Report  string
	Channel string // download | slack | email
	Trigger string // manual | scheduled
	Target  string
	Status  string // accepted | ok | failed — see the note in reports.go
	Detail  string
	SendID  string
	RunID   string
}

const schema = `
CREATE TABLE IF NOT EXISTS items (
  id              INTEGER PRIMARY KEY,
  sku             TEXT    NOT NULL UNIQUE,
  name            TEXT    NOT NULL,
  category        TEXT    NOT NULL,
  bin             TEXT    NOT NULL DEFAULT '',
  supplier        TEXT    NOT NULL DEFAULT '',
  quantity        INTEGER NOT NULL DEFAULT 0,
  reorder_point   INTEGER NOT NULL DEFAULT 0,
  unit_cost_cents INTEGER NOT NULL DEFAULT 0,
  status          TEXT    NOT NULL DEFAULT 'active',
  notes           TEXT    NOT NULL DEFAULT '',
  created_at      TEXT    NOT NULL,
  updated_at      TEXT    NOT NULL
);
CREATE TABLE IF NOT EXISTS movements (
  id      INTEGER PRIMARY KEY,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  kind    TEXT    NOT NULL,
  qty     INTEGER NOT NULL,
  note    TEXT    NOT NULL DEFAULT '',
  actor   TEXT    NOT NULL DEFAULT '',
  at      TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS movements_at  ON movements(at DESC);
CREATE INDEX IF NOT EXISTS movements_item ON movements(item_id);
CREATE TABLE IF NOT EXISTS activity (
  id      INTEGER PRIMARY KEY,
  at      TEXT NOT NULL,
  actor   TEXT NOT NULL DEFAULT '',
  action  TEXT NOT NULL,
  subject TEXT NOT NULL DEFAULT '',
  detail  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS activity_at ON activity(at DESC);
CREATE TABLE IF NOT EXISTS deliveries (
  id      INTEGER PRIMARY KEY,
  at      TEXT NOT NULL,
  report  TEXT NOT NULL,
  channel TEXT NOT NULL,
  trigger TEXT NOT NULL,
  target  TEXT NOT NULL DEFAULT '',
  status  TEXT NOT NULL,
  detail  TEXT NOT NULL DEFAULT '',
  send_id TEXT NOT NULL DEFAULT '',
  run_id  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS deliveries_at ON deliveries(at DESC);
`

func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating the data directory: %w", err)
	}
	path := filepath.Join(dir, "stockroom.db")
	// WAL and a busy timeout, because the scheduled task can be building a
	// report while somebody is editing an item on the same file.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("creating the schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---------- items ----------

// ItemQuery is the Inventory tab's filter, carried in the query string so a
// filtered view is a link somebody can paste into a message.
type ItemQuery struct {
	Search   string
	Category string
	State    string // ok | low | out | on_order | retired
	Sort     string // name | sku | qty | value | category | updated
}

func (s *Store) ListItems(q ItemQuery) ([]Item, error) {
	where := []string{"1=1"}
	var args []any
	if t := strings.TrimSpace(q.Search); t != "" {
		where = append(where, "(LOWER(name) LIKE ? OR LOWER(sku) LIKE ? OR LOWER(bin) LIKE ? OR LOWER(supplier) LIKE ?)")
		like := "%" + strings.ToLower(t) + "%"
		args = append(args, like, like, like, like)
	}
	if q.Category != "" {
		where = append(where, "category = ?")
		args = append(args, q.Category)
	}
	switch q.State {
	case "retired":
		where = append(where, "status = 'retired'")
	case "out":
		where = append(where, "status != 'retired' AND quantity = 0")
	case "low":
		where = append(where, "status != 'retired' AND quantity > 0 AND quantity <= reorder_point")
	case "on_order":
		where = append(where, "status = 'on_order'")
	case "ok":
		where = append(where, "status = 'active' AND quantity > reorder_point")
	}

	order := "name COLLATE NOCASE ASC"
	switch q.Sort {
	case "sku":
		order = "sku ASC"
	case "qty":
		order = "quantity ASC, name COLLATE NOCASE ASC"
	case "value":
		order = "(quantity * unit_cost_cents) DESC, name COLLATE NOCASE ASC"
	case "category":
		order = "category ASC, name COLLATE NOCASE ASC"
	case "updated":
		order = "updated_at DESC"
	}

	rows, err := s.db.Query(`SELECT id, sku, name, category, bin, supplier, quantity,
		reorder_point, unit_cost_cents, status, notes, created_at, updated_at
		FROM items WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func scanItems(rows *sql.Rows) ([]Item, error) {
	out := []Item{}
	for rows.Next() {
		var it Item
		var created, updated string
		if err := rows.Scan(&it.ID, &it.SKU, &it.Name, &it.Category, &it.Bin, &it.Supplier,
			&it.Quantity, &it.ReorderPoint, &it.UnitCostCents, &it.Status, &it.Notes,
			&created, &updated); err != nil {
			return nil, err
		}
		it.CreatedAt, _ = time.Parse(time.RFC3339, created)
		it.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, it)
	}
	return out, rows.Err()
}

var ErrNotFound = errors.New("not found")

func (s *Store) GetItem(id int64) (Item, error) {
	rows, err := s.db.Query(`SELECT id, sku, name, category, bin, supplier, quantity,
		reorder_point, unit_cost_cents, status, notes, created_at, updated_at
		FROM items WHERE id = ?`, id)
	if err != nil {
		return Item{}, err
	}
	defer rows.Close()
	items, err := scanItems(rows)
	if err != nil {
		return Item{}, err
	}
	if len(items) == 0 {
		return Item{}, ErrNotFound
	}
	return items[0], nil
}

func (s *Store) CreateItem(it Item, actor string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`INSERT INTO items
		(sku, name, category, bin, supplier, quantity, reorder_point, unit_cost_cents, status, notes, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.SKU, it.Name, it.Category, it.Bin, it.Supplier, it.Quantity,
		it.ReorderPoint, it.UnitCostCents, it.Status, it.Notes, now, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if it.Quantity != 0 {
		// The opening count is a movement like any other, so the ledger adds up
		// to the quantity on the shelf. A starting balance that appeared from
		// nowhere is the first thing that makes a register untrustworthy.
		_, _ = s.db.Exec(`INSERT INTO movements (item_id, kind, qty, note, actor, at) VALUES (?,?,?,?,?,?)`,
			id, "receipt", it.Quantity, "opening count", actor, now)
	}
	s.Log(actor, "added an item", it.SKU+" · "+it.Name, fmt.Sprintf("%d in %s", it.Quantity, it.Bin))
	return id, nil
}

func (s *Store) UpdateItem(it Item, actor string) error {
	_, err := s.db.Exec(`UPDATE items SET sku=?, name=?, category=?, bin=?, supplier=?,
		reorder_point=?, unit_cost_cents=?, status=?, notes=?, updated_at=? WHERE id=?`,
		it.SKU, it.Name, it.Category, it.Bin, it.Supplier, it.ReorderPoint,
		it.UnitCostCents, it.Status, it.Notes, time.Now().UTC().Format(time.RFC3339), it.ID)
	if err != nil {
		return err
	}
	s.Log(actor, "edited an item", it.SKU+" · "+it.Name, "")
	return nil
}

// DeleteItem removes an item and, by the foreign key, its movements.
func (s *Store) DeleteItem(id int64, actor string) error {
	it, err := s.GetItem(id)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM items WHERE id = ?`, id); err != nil {
		return err
	}
	s.Log(actor, "deleted an item", it.SKU+" · "+it.Name, "its movement history went with it")
	return nil
}

// ---------- movements ----------

// RecordMovement writes the ledger line and moves the count in one transaction.
// Two statements that could half-succeed would leave a shelf count no movement
// explains, which is the one failure this table exists to rule out.
func (s *Store) RecordMovement(m Movement) (Item, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`INSERT INTO movements (item_id, kind, qty, note, actor, at) VALUES (?,?,?,?,?,?)`,
		m.ItemID, m.Kind, m.Qty, m.Note, m.Actor, now); err != nil {
		return Item{}, err
	}
	// MAX(0, …): the shelf cannot hold a negative count, and refusing the whole
	// movement would lose the record of what somebody actually did.
	if _, err := tx.Exec(`UPDATE items SET quantity = MAX(0, quantity + ?), updated_at = ? WHERE id = ?`,
		m.Qty, now, m.ItemID); err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, err
	}

	it, err := s.GetItem(m.ItemID)
	if err != nil {
		return Item{}, err
	}
	s.Log(m.Actor, "recorded a "+m.Kind, it.SKU+" · "+it.Name, fmt.Sprintf("%+d, now %d", m.Qty, it.Quantity))
	return it, nil
}

func (s *Store) ListMovements(since time.Time, itemID int64, kind string, limit int) ([]Movement, error) {
	where := []string{"1=1"}
	var args []any
	if !since.IsZero() {
		where = append(where, "m.at >= ?")
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	if itemID > 0 {
		where = append(where, "m.item_id = ?")
		args = append(args, itemID)
	}
	if kind != "" {
		where = append(where, "m.kind = ?")
		args = append(args, kind)
	}
	if limit <= 0 {
		limit = 200
	}
	args = append(args, limit)

	rows, err := s.db.Query(`SELECT m.id, m.item_id, i.sku, i.name, i.category, m.kind, m.qty, m.note, m.actor, m.at
		FROM movements m JOIN items i ON i.id = m.item_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY m.at DESC, m.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Movement{}
	for rows.Next() {
		var m Movement
		var at string
		if err := rows.Scan(&m.ID, &m.ItemID, &m.SKU, &m.ItemName, &m.Category,
			&m.Kind, &m.Qty, &m.Note, &m.Actor, &at); err != nil {
			return nil, err
		}
		m.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------- activity and deliveries ----------

// Log is best-effort on purpose: an activity row that failed to write must
// never fail the edit it was describing.
func (s *Store) Log(actor, action, subject, detail string) {
	if actor == "" {
		actor = "unattributed"
	}
	_, _ = s.db.Exec(`INSERT INTO activity (at, actor, action, subject, detail) VALUES (?,?,?,?,?)`,
		time.Now().UTC().Format(time.RFC3339), actor, action, subject, detail)
}

func (s *Store) ListActivity(limit int) ([]Activity, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, at, actor, action, subject, detail
		FROM activity ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var at string
		if err := rows.Scan(&a.ID, &at, &a.Actor, &a.Action, &a.Subject, &a.Detail); err != nil {
			return nil, err
		}
		a.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) RecordDelivery(d Delivery) {
	_, _ = s.db.Exec(`INSERT INTO deliveries (at, report, channel, trigger, target, status, detail, send_id, run_id)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		time.Now().UTC().Format(time.RFC3339), d.Report, d.Channel, d.Trigger,
		d.Target, d.Status, d.Detail, d.SendID, d.RunID)
}

func (s *Store) ListDeliveries(limit int) ([]Delivery, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.Query(`SELECT id, at, report, channel, trigger, target, status, detail, send_id, run_id
		FROM deliveries ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		var at string
		if err := rows.Scan(&d.ID, &at, &d.Report, &d.Channel, &d.Trigger, &d.Target,
			&d.Status, &d.Detail, &d.SendID, &d.RunID); err != nil {
			return nil, err
		}
		d.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---------- the numbers the dashboard and the reports share ----------

// CategoryStat is one segment of the aisle band.
type CategoryStat struct {
	Category   string
	Items      int
	Units      int
	ValueCents int
	Low        int
}

type Stats struct {
	Items       int
	Units       int
	ValueCents  int
	Low         int
	Out         int
	OnOrder     int
	MovedWeek   int // movements in the last 7 days
	Categories  []CategoryStat
	MaxCatValue int
}

// Stats is computed in one place because the dashboard, the PDF and the Slack
// summary all quote it. Three call sites deriving "stock value" separately is
// how a report and a screen come to disagree in front of a customer.
func (s *Store) Stats() (Stats, error) {
	var st Stats
	row := s.db.QueryRow(`SELECT
		COUNT(*),
		COALESCE(SUM(quantity),0),
		COALESCE(SUM(quantity * unit_cost_cents),0),
		COALESCE(SUM(CASE WHEN status != 'retired' AND quantity > 0 AND quantity <= reorder_point THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status != 'retired' AND quantity = 0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status = 'on_order' THEN 1 ELSE 0 END),0)
		FROM items WHERE status != 'retired'`)
	if err := row.Scan(&st.Items, &st.Units, &st.ValueCents, &st.Low, &st.Out, &st.OnOrder); err != nil {
		return st, err
	}

	week := time.Now().UTC().AddDate(0, 0, -7).Format(time.RFC3339)
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM movements WHERE at >= ?`, week).Scan(&st.MovedWeek); err != nil {
		return st, err
	}

	rows, err := s.db.Query(`SELECT category, COUNT(*), COALESCE(SUM(quantity),0),
		COALESCE(SUM(quantity * unit_cost_cents),0),
		COALESCE(SUM(CASE WHEN quantity <= reorder_point THEN 1 ELSE 0 END),0)
		FROM items WHERE status != 'retired' GROUP BY category`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	byName := map[string]CategoryStat{}
	for rows.Next() {
		var c CategoryStat
		if err := rows.Scan(&c.Category, &c.Items, &c.Units, &c.ValueCents, &c.Low); err != nil {
			return st, err
		}
		byName[c.Category] = c
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	// Emitted in Categories order, not in size order, and including a category
	// with nothing in it: the band is a picture of the room, and an empty aisle
	// is information.
	for _, name := range Categories {
		c := byName[name]
		c.Category = name
		st.Categories = append(st.Categories, c)
		if c.ValueCents > st.MaxCatValue {
			st.MaxCatValue = c.ValueCents
		}
	}
	return st, nil
}

func (s *Store) count(table string) int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
	return n
}
