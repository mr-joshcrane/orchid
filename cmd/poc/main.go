package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/joshc/orchid/minibus"
	"github.com/joshc/orchid/minicache"
	"github.com/joshc/orchid/minidb"
	"github.com/joshc/orchid/minifsm"
	"github.com/joshc/orchid/minilog"
	"github.com/joshc/orchid/minirouter"
)

func main() {
	var logBuf strings.Builder
	log := minilog.New(&logBuf)

	db := minidb.NewDB()
	db.CreateTable("users")
	users, _ := db.Table("users")

	cache := minicache.New()
	bus := minibus.New()
	fsm := minifsm.New("pending")
	fsm.On("pending", "approve", "active", func() error {
		log.Info("user approved")
		return nil
	})
	fsm.On("active", "ban", "banned", func() error {
		log.Info("user banned")
		return nil
	})
	fsm.On("banned", "unban", "active", func() error {
		log.Info("user unbanned")
		return nil
	})

	bus.Subscribe("user.created", func(payload string) {
		log.Info("event: user.created", "id", payload)
		cache.Set("latest_user", payload, 5*time.Minute)
	})

	router := minirouter.New()
	router.Handle("GET", "/users/:id", func(w http.ResponseWriter, r *http.Request, p minirouter.Params) {
		id := p["id"]

		if cached, ok := cache.Get("user:" + id); ok {
			fmt.Fprintf(w, "cached: %s", cached)
			return
		}

		row, ok := users.Get(id)
		if !ok {
			http.Error(w, "not found", 404)
			return
		}

		result := row["name"] + " (" + row["email"] + ")"
		cache.Set("user:"+id, result, 1*time.Minute)
		fmt.Fprintf(w, "db: %s", result)
	})

	router.Handle("POST", "/users/:id", func(w http.ResponseWriter, r *http.Request, p minirouter.Params) {
		id := p["id"]
		err := users.Insert(id, map[string]string{
			"name":  r.URL.Query().Get("name"),
			"email": r.URL.Query().Get("email"),
		})
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		bus.Publish("user.created", id)
		fmt.Fprintf(w, "created: %s", id)
	})

	// --- Run it ---

	fmt.Println("=== Create user ===")
	resp := request(router, "POST", "/users/1?name=alice&email=alice@example.com")
	fmt.Println(resp)

	fmt.Println("\n=== Get user (from db) ===")
	resp = request(router, "GET", "/users/1")
	fmt.Println(resp)

	fmt.Println("\n=== Get user (from cache) ===")
	resp = request(router, "GET", "/users/1")
	fmt.Println(resp)

	fmt.Println("\n=== FSM: approve user ===")
	fmt.Println("state:", fsm.State())
	fsm.Send("approve")
	fmt.Println("state:", fsm.State())

	fmt.Println("\n=== FSM: ban user ===")
	fsm.Send("ban")
	fmt.Println("state:", fsm.State())

	fmt.Println("\n=== FSM: invalid transition ===")
	err := fsm.Send("approve")
	fmt.Println("error:", err)

	fmt.Println("\n=== Cache stats ===")
	fmt.Println("entries:", cache.Len())
	fmt.Println("keys:", cache.Keys())

	fmt.Println("\n=== Bus topics ===")
	fmt.Println("topics:", bus.Topics())

	fmt.Println("\n=== Query users by name ===")
	results := users.Query("name", "alice")
	fmt.Println("found:", len(results), "rows")
	for _, row := range results {
		fmt.Println(" ", row)
	}

	fmt.Println("\n=== Logs ===")
	fmt.Print(logBuf.String())
}

func request(handler http.Handler, method string, path string) string {
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w.Body.String()
}
