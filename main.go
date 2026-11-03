package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
)

var globalStore = NewStore()

func main() {
	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	mux := setupRouter(globalStore)

	fmt.Printf("Campaign Events service listening on %s\n", addr)
	fmt.Printf("Open in browser: http://localhost:8080\n")
	log.Fatal(http.ListenAndServe(addr, mux))
}

func setupRouter(store *Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleRoot(store))
	mux.HandleFunc("GET /seed", handleSeed(store))
	mux.HandleFunc("POST /events", handlePostEvents(store))
	mux.HandleFunc("GET /campaigns/{campaignID}/stats", handleGetStats(store))
	mux.HandleFunc("GET /campaigns/{campaignID}/events", handleGetEvents(store))
	return mux
}

// handleRoot returns an HTML dashboard homepage with quick links and API guide.
func handleRoot(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>Relay Campaign Events Service</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 800px; margin: 40px auto; padding: 0 20px; line-height: 1.6; color: #222; }
        h1 { color: #1a73e8; }
        .card { background: #f8f9fa; border: 1px solid #dadce0; border-radius: 8px; padding: 20px; margin-bottom: 20px; }
        .btn { display: inline-block; background: #1a73e8; color: white; padding: 10px 18px; border-radius: 6px; text-decoration: none; font-weight: bold; margin-top: 10px; }
        .btn:hover { background: #1557b0; }
        code { background: #e8eaed; padding: 2px 6px; border-radius: 4px; font-size: 0.9em; }
        ul { padding-left: 20px; }
        li { margin-bottom: 8px; }
    </style>
</head>
<body>
    <h1>🚀 Relay Campaign Events Service</h1>
    <p>Service is <strong>online and running</strong> on port 8080.</p>

    <div class="card">
        <h3>Step 1: Load Real-World Seed Data</h3>
        <p>Click below to ingest the 236 provider events from <code>seed/events.json</code> into memory:</p>
        <a class="btn" href="/seed">📥 Click Here to Load Seed Data</a>
    </div>

    <div class="card">
        <h3>Step 2: View Campaign Dashboard Metrics (JSON APIs)</h3>
        <ul>
            <li><a href="/campaigns/cmp_summer_sale/stats"><strong>cmp_summer_sale</strong> Stats</a> &mdash; <code>/campaigns/cmp_summer_sale/stats</code></li>
            <li><a href="/campaigns/cmp_welcome/stats"><strong>cmp_welcome</strong> Stats</a> &mdash; <code>/campaigns/cmp_welcome/stats</code></li>
            <li><a href="/campaigns/cmp_winback/stats"><strong>cmp_winback</strong> Stats</a> &mdash; <code>/campaigns/cmp_winback/stats</code></li>
            <li><a href="/campaigns/cmp_summer_sale/events?limit=5">Recent Activity Events</a> &mdash; <code>/campaigns/cmp_summer_sale/events?limit=5</code></li>
        </ul>
    </div>

    <div class="card">
        <h3>API Endpoints</h3>
        <ul>
            <li><code>POST /events</code> &mdash; Ingest JSON batch of events (idempotent, deduplicated).</li>
            <li><code>GET /campaigns/{campaign_id}/stats</code> &mdash; Real-time campaign stats and ratios.</li>
            <li><code>GET /campaigns/{campaign_id}/events?limit=50&offset=0&type=opened</code> &mdash; Paginated event activity.</li>
        </ul>
    </div>
</body>
</html>`)
	}
}

// handleSeed allows quick 1-click browser seeding of seed/events.json.
func handleSeed(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seedPaths := []string{"seed/events.json", "starter/seed/events.json", "../starter/seed/events.json"}
		var data []byte
		var err error
		for _, p := range seedPaths {
			data, err = os.ReadFile(p)
			if err == nil {
				break
			}
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "could not locate seed/events.json: " + err.Error(),
			})
			return
		}

		var rawBatch []json.RawMessage
		if err := json.Unmarshal(data, &rawBatch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid JSON in seed file: " + err.Error(),
			})
			return
		}

		resp := BatchIngestResponse{Received: len(rawBatch)}
		for i, rawMsg := range rawBatch {
			var raw rawEvent
			if err := json.Unmarshal(rawMsg, &raw); err != nil {
				resp.Rejected++
				resp.Errors = append(resp.Errors, EventError{Index: i, Reason: err.Error()})
				continue
			}
			ev, err := raw.toEvent()
			if err != nil {
				resp.Rejected++
				resp.Errors = append(resp.Errors, EventError{Index: i, EventID: raw.EventID, Reason: err.Error()})
				continue
			}
			if store.Ingest(ev) == ResultDuplicate {
				resp.Duplicates++
			} else {
				resp.Accepted++
			}
		}

		// Redirect to stats or return JSON
		if r.Header.Get("Accept") != "" && (r.Header.Get("Accept")[:9] == "text/html" || r.Header.Get("Accept") == "*/*") {
			http.Redirect(w, r, "/campaigns/cmp_summer_sale/stats", http.StatusSeeOther)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

type EventError struct {
	Index   int    `json:"index"`
	EventID string `json:"event_id,omitempty"`
	Reason  string `json:"reason"`
}

type BatchIngestResponse struct {
	Received   int          `json:"received"`
	Accepted   int          `json:"accepted"`
	Duplicates int          `json:"duplicates"`
	Rejected   int          `json:"rejected"`
	Errors     []EventError `json:"errors,omitempty"`
}

// handlePostEvents ingests a JSON array of events from providers.
// Decoding element-by-element preserves valid events even if some items are malformed.
func handlePostEvents(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body is empty"})
			return
		}
		defer r.Body.Close()

		var rawBatch []json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&rawBatch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid JSON body: expected JSON array of events",
			})
			return
		}

		resp := BatchIngestResponse{
			Received: len(rawBatch),
		}

		for i, rawMsg := range rawBatch {
			var raw rawEvent
			if err := json.Unmarshal(rawMsg, &raw); err != nil {
				resp.Rejected++
				resp.Errors = append(resp.Errors, EventError{
					Index:  i,
					Reason: "malformed JSON object: " + err.Error(),
				})
				continue
			}

			ev, err := raw.toEvent()
			if err != nil {
				resp.Rejected++
				resp.Errors = append(resp.Errors, EventError{
					Index:   i,
					EventID: raw.EventID,
					Reason:  err.Error(),
				})
				continue
			}

			result := store.Ingest(ev)
			if result == ResultDuplicate {
				resp.Duplicates++
			} else {
				resp.Accepted++
			}
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// handleGetStats returns aggregated stats for one campaign.
func handleGetStats(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		campaignID := r.PathValue("campaignID")
		if campaignID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing campaignID"})
			return
		}

		stats, found := store.GetStats(campaignID)
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("campaign %q has no recorded events yet. Ingest events first via POST /events or visit /seed", campaignID),
			})
			return
		}

		writeJSON(w, http.StatusOK, stats)
	}
}

// handleGetEvents returns a paginated list of events for a campaign.
func handleGetEvents(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		campaignID := r.PathValue("campaignID")
		if campaignID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing campaignID"})
			return
		}

		limit := 50
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
				limit = l
			}
		}

		offset := 0
		if oStr := r.URL.Query().Get("offset"); oStr != "" {
			if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
				offset = o
			}
		}

		eventType := r.URL.Query().Get("type")

		eventsResp, found := store.GetEvents(campaignID, limit, offset, eventType)
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("campaign %q has no recorded events yet", campaignID),
			})
			return
		}

		writeJSON(w, http.StatusOK, eventsResp)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}
