package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- Dashboard ----------

type dashboardData struct {
	Stats     Stats
	Low       []Item
	Movements []Movement
	Activity  []Activity
	Total     int
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items, err := s.store.ListItems(ItemQuery{Sort: "qty"})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	low := []Item{}
	for _, it := range items {
		if st := it.State(); st == "low" || st == "out" {
			low = append(low, it)
		}
	}
	movements, err := s.store.ListMovements(time.Time{}, 0, "", 8)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	activity, err := s.store.ListActivity(6)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	s.render(w, r, "dashboard", "dashboard", "The room",
		dashboardData{Stats: stats, Low: low, Movements: movements, Activity: activity, Total: len(items)})
}

// ---------- Inventory ----------

type inventoryData struct {
	Items    []Item
	Query    ItemQuery
	Matched  int
	Units    int
	Value    int
	AllItems int
}

func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	q := ItemQuery{
		Search:   strings.TrimSpace(r.URL.Query().Get("q")),
		Category: r.URL.Query().Get("category"),
		State:    r.URL.Query().Get("state"),
		Sort:     r.URL.Query().Get("sort"),
	}
	items, err := s.store.ListItems(q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data := inventoryData{Items: items, Query: q, Matched: len(items), AllItems: s.store.count("items")}
	for _, it := range items {
		data.Units += it.Quantity
		data.Value += it.ValueCents()
	}
	s.render(w, r, "inventory", "inventory", "Inventory", data)
}

type itemFormData struct {
	Item    Item
	IsNew   bool
	History []Movement
}

func (s *Server) handleItemForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "item", "inventory", "New item", itemFormData{
		Item:  Item{Category: Categories[0], Status: "active", ReorderPoint: 5},
		IsNew: true,
	})
}

func (s *Server) handleItemDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	it, err := s.store.GetItem(id)
	if err == ErrNotFound {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	history, err := s.store.ListMovements(time.Time{}, id, "", 40)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "item", "inventory", it.Name, itemFormData{Item: it, History: history})
}

// itemFromForm reads and validates the form. Every refusal names the field and
// what would be accepted; "invalid input" tells somebody they were wrong and
// nothing else.
func itemFromForm(r *http.Request) (Item, error) {
	it := Item{
		SKU:          strings.ToUpper(strings.TrimSpace(r.FormValue("sku"))),
		Name:         strings.TrimSpace(r.FormValue("name")),
		Category:     r.FormValue("category"),
		Bin:          strings.ToUpper(strings.TrimSpace(r.FormValue("bin"))),
		Supplier:     strings.TrimSpace(r.FormValue("supplier")),
		Status:       r.FormValue("status"),
		Notes:        strings.TrimSpace(r.FormValue("notes")),
		Quantity:     formInt(r, "quantity"),
		ReorderPoint: formInt(r, "reorder_point"),
	}
	if it.SKU == "" {
		return it, fmt.Errorf("give the item a SKU — it is what the reports and the ledger key on")
	}
	if it.Name == "" {
		return it, fmt.Errorf("give the item a name")
	}
	if !contains(Categories, it.Category) {
		return it, fmt.Errorf("%q is not one of the five aisles", it.Category)
	}
	if !contains([]string{"active", "on_order", "retired"}, it.Status) {
		it.Status = "active"
	}
	cost := strings.TrimSpace(r.FormValue("unit_cost"))
	cost = strings.TrimPrefix(cost, "$")
	if cost != "" {
		v, err := strconv.ParseFloat(cost, 64)
		if err != nil || v < 0 {
			return it, fmt.Errorf("unit cost should be a number like 12.50, not %q", r.FormValue("unit_cost"))
		}
		it.UnitCostCents = int(v*100 + 0.5)
	}
	if it.Quantity < 0 || it.ReorderPoint < 0 {
		return it, fmt.Errorf("counts cannot be negative — record an issue instead")
	}
	return it, nil
}

func (s *Server) handleItemCreate(w http.ResponseWriter, r *http.Request) {
	it, err := itemFromForm(r)
	if err != nil {
		setFlash(w, "bad", err.Error())
		s.redirect(w, r, s.cfg.url("/inventory/new"))
		return
	}
	id, err := s.store.CreateItem(it, actorFrom(r))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			setFlash(w, "bad", "SKU "+it.SKU+" is already on the shelf. Open that line instead of adding a second one.")
			s.redirect(w, r, s.cfg.url("/inventory?q="+it.SKU))
			return
		}
		s.fail(w, r, err)
		return
	}
	setFlash(w, "ok", "Added "+it.SKU+" — "+it.Name+".")
	s.redirect(w, r, s.cfg.url(fmt.Sprintf("/inventory/%d", id)))
}

func (s *Server) handleItemUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	it, err := itemFromForm(r)
	if err != nil {
		setFlash(w, "bad", err.Error())
		s.redirect(w, r, s.cfg.url(fmt.Sprintf("/inventory/%d", id)))
		return
	}
	it.ID = id
	if err := s.store.UpdateItem(it, actorFrom(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	setFlash(w, "ok", "Saved "+it.SKU+".")
	s.redirect(w, r, s.cfg.url(fmt.Sprintf("/inventory/%d", id)))
}

func (s *Server) handleItemDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	it, err := s.store.GetItem(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Typed confirmation, because this one cannot be undone: deleting a line
	// takes its whole movement history with it, and the register no longer adds
	// up to what the shelf did. Retiring an item is the reversible alternative,
	// and the dialog offers it.
	if strings.TrimSpace(r.FormValue("confirm")) != it.SKU {
		setFlash(w, "bad", "Type the SKU exactly ("+it.SKU+") to delete the line and its history.")
		s.redirect(w, r, s.cfg.url(fmt.Sprintf("/inventory/%d", id)))
		return
	}
	if err := s.store.DeleteItem(id, actorFrom(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	setFlash(w, "ok", "Deleted "+it.SKU+" and its movements.")
	s.redirect(w, r, s.cfg.url("/inventory"))
}

// handleQuickMove is the +1 / −1 on a row: the movement people make twenty
// times a day, without a form in the way.
func (s *Server) handleQuickMove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	qty := formInt(r, "qty")
	if qty == 0 {
		s.redirect(w, r, s.cfg.url(fmt.Sprintf("/inventory/%d", id)))
		return
	}
	kind := "issue"
	if qty > 0 {
		kind = "receipt"
	}
	it, err := s.store.RecordMovement(Movement{
		ItemID: id, Kind: kind, Qty: qty, Note: "quick change", Actor: actorFrom(r),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	setFlash(w, "ok", fmt.Sprintf("%s %s — %d on the shelf.", signWord(qty), it.SKU, it.Quantity))
	s.redirect(w, r, backTo(r, s.cfg.url("/inventory")))
}

// ---------- Movements ----------

type movementsData struct {
	Movements []Movement
	Items     []Item
	Kind      string
	Days      int
	In, Out   int
}

func (s *Server) handleMovements(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 365 {
		days = v
	}
	kind := r.URL.Query().Get("kind")
	movements, err := s.store.ListMovements(time.Now().UTC().AddDate(0, 0, -days), 0, kind, 400)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items, err := s.store.ListItems(ItemQuery{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data := movementsData{Movements: movements, Items: items, Kind: kind, Days: days}
	for _, m := range movements {
		if m.Qty > 0 {
			data.In += m.Qty
		} else {
			data.Out -= m.Qty
		}
	}
	s.render(w, r, "movements", "movements", "Movements", data)
}

func (s *Server) handleMovementCreate(w http.ResponseWriter, r *http.Request) {
	itemID, _ := strconv.ParseInt(r.FormValue("item_id"), 10, 64)
	kind := r.FormValue("kind")
	qty := formInt(r, "qty")

	switch {
	case itemID == 0:
		setFlash(w, "bad", "Pick the item the movement applies to.")
	case !contains(MovementKinds, kind):
		setFlash(w, "bad", "Pick one of the four movement kinds.")
	case qty == 0:
		setFlash(w, "bad", "A movement of zero changes nothing — enter how many moved.")
	default:
		// Issues are entered as a positive number and stored negative: nobody
		// types a minus sign to give something out, and a ledger that accepted
		// both conventions would fill up with sign errors.
		if kind == "issue" && qty > 0 {
			qty = -qty
		}
		it, err := s.store.RecordMovement(Movement{
			ItemID: itemID, Kind: kind, Qty: qty,
			Note: strings.TrimSpace(r.FormValue("note")), Actor: actorFrom(r),
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		setFlash(w, "ok", fmt.Sprintf("Recorded %s of %d on %s — %d on the shelf.",
			kind, absInt(qty), it.SKU, it.Quantity))
	}
	s.redirect(w, r, backTo(r, s.cfg.url("/movements")))
}

// ---------- Activity ----------

type activityData struct {
	Activity []Activity
	Runs     []JobRun
	RunsErr  string
	OnPlat   bool
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	activity, err := s.store.ListActivity(120)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data := activityData{Activity: activity, OnPlat: s.plat.Available()}
	if s.plat.Available() {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		runs, err := s.plat.JobRuns(ctx, "report", 25)
		if err != nil {
			data.RunsErr = err.Error()
		}
		data.Runs = runs
	}
	s.render(w, r, "activity", "activity", "Activity", data)
}

// ---------- Platform ----------

type platformData struct {
	Self    *Self
	SelfErr string
	OnPlat  bool
	Mounts  []Mount
}

func (s *Server) handlePlatform(w http.ResponseWriter, r *http.Request) {
	data := platformData{OnPlat: s.plat.Available(), Mounts: s.cfg.Mounts}
	if s.plat.Available() {
		self, err := s.self(r.Context())
		if err != nil {
			data.SelfErr = err.Error()
		}
		data.Self = self
	}
	s.render(w, r, "platform", "platform", "Platform", data)
}

// self caches /app/self for a minute.
//
// The answer changes when an administrator attaches an integration, which is a
// rare, deliberate act — and the Reports tab asks on every render. A minute is
// short enough that "an admin just turned Slack on" is not a puzzling wait, and
// long enough that browsing the app is not a stream of API calls.
type selfCache struct {
	mu   sync.Mutex
	self *Self
	at   time.Time
	err  error
}

var cachedSelf selfCache

func (s *Server) self(ctx context.Context) (*Self, error) {
	cachedSelf.mu.Lock()
	defer cachedSelf.mu.Unlock()
	if time.Since(cachedSelf.at) < time.Minute && (cachedSelf.self != nil || cachedSelf.err != nil) {
		return cachedSelf.self, cachedSelf.err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	self, err := s.plat.Self(ctx)
	cachedSelf.self, cachedSelf.err, cachedSelf.at = self, err, time.Now()
	return self, err
}

// invalidateSelf is called after a send is refused for a capability reason: the
// most likely next thing to happen is an administrator turning it on, and a
// stale minute of "you cannot do this" right after they did is confusing.
func invalidateSelf() {
	cachedSelf.mu.Lock()
	cachedSelf.at = time.Time{}
	cachedSelf.mu.Unlock()
}

// ---------- small shared helpers ----------

// redirect sends the browser to an app-relative path, with the mount prefix
// removed. Every redirect in this app goes through it — see Config.bare.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, s.cfg.bare(path), http.StatusSeeOther)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	setFlash(w, "bad", "Something went wrong reading the register. The detail is in the app's log.")
	http.Error(w, "the register could not answer that request", http.StatusInternalServerError)
}

func formInt(r *http.Request, name string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(name)))
	return v
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func signWord(n int) string {
	if n < 0 {
		return "Issued"
	}
	return "Received"
}

// backTo returns to the filtered list somebody was looking at.
//
// Only a relative path is honoured. An open redirect is a small thing in an app
// behind a sign-in, and still not worth having.
func backTo(r *http.Request, fallback string) string {
	v := r.FormValue("back")
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return v
	}
	return fallback
}
