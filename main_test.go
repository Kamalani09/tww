package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

func newTestServer() (*Store, *http.ServeMux) {
	store := NewStore()
	mux := setupRouter(store)
	return store, mux
}

func TestBatchIngestValid(t *testing.T) {
	_, mux := newTestServer()

	payload := `[
		{"event_id": "evt_1", "campaign_id": "c1", "contact_id": "u1", "type": "sent", "timestamp": "2026-08-10T10:00:00Z"},
		{"event_id": "evt_2", "campaign_id": "c1", "contact_id": "u1", "type": "delivered", "timestamp": "2026-08-10T10:01:00Z"},
		{"event_id": "evt_3", "campaign_id": "c1", "contact_id": "u1", "type": "opened", "timestamp": "2026-08-10T10:05:00Z"},
		{"event_id": "evt_4", "campaign_id": "c1", "contact_id": "u1", "type": "clicked", "timestamp": "2026-08-10T10:06:00Z"}
	]`

	req := httptest.NewRequest("POST", "/events", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp BatchIngestResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Received != 4 || resp.Accepted != 4 || resp.Duplicates != 0 || resp.Rejected != 0 {
		t.Fatalf("unexpected ingest response counts: %+v", resp)
	}
}

func TestDeduplication(t *testing.T) {
	_, mux := newTestServer()

	// Batch 1: Ingest evt_1 and evt_2
	batch1 := `[
		{"event_id": "evt_1", "campaign_id": "c1", "contact_id": "u1", "type": "sent", "timestamp": "2026-08-10T10:00:00Z"},
		{"event_id": "evt_2", "campaign_id": "c1", "contact_id": "u1", "type": "delivered", "timestamp": "2026-08-10T10:01:00Z"}
	]`
	req1 := httptest.NewRequest("POST", "/events", bytes.NewBufferString(batch1))
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)

	// Batch 2: Retry evt_1, retry evt_2, plus new evt_3
	batch2 := `[
		{"event_id": "evt_1", "campaign_id": "c1", "contact_id": "u1", "type": "sent", "timestamp": "2026-08-10T10:00:00Z"},
		{"event_id": "evt_2", "campaign_id": "c1", "contact_id": "u1", "type": "delivered", "timestamp": "2026-08-10T10:01:00Z"},
		{"event_id": "evt_3", "campaign_id": "c1", "contact_id": "u1", "type": "opened", "timestamp": "2026-08-10T10:05:00Z"}
	]`
	req2 := httptest.NewRequest("POST", "/events", bytes.NewBufferString(batch2))
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	var resp2 BatchIngestResponse
	if err := json.NewDecoder(rec2.Body).Decode(&resp2); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp2.Received != 3 || resp2.Accepted != 1 || resp2.Duplicates != 2 {
		t.Fatalf("expected 1 accepted, 2 duplicates; got %+v", resp2)
	}

	// Verify stats show sent=1, delivered=1, opened=1 (no double counting)
	reqStats := httptest.NewRequest("GET", "/campaigns/c1/stats", nil)
	recStats := httptest.NewRecorder()
	mux.ServeHTTP(recStats, reqStats)

	var stats CampaignStatsResponse
	if err := json.NewDecoder(recStats.Body).Decode(&stats); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}

	if stats.Sent != 1 || stats.Delivered != 1 || stats.Opened != 1 {
		t.Fatalf("counts were double-counted: %+v", stats)
	}
}

func TestMalformedBatchResilience(t *testing.T) {
	_, mux := newTestServer()

	// A batch with:
	// 1. Valid event (evt_101)
	// 2. Malformed timestamp ("not-a-time")
	// 3. Empty event_id ("")
	// 4. Missing contact_id
	// 5. Unknown event type ("spam_report")
	// 6. Valid event (evt_102)
	mixedBatch := `[
		{"event_id": "evt_101", "campaign_id": "c2", "contact_id": "u1", "type": "sent", "timestamp": "2026-08-10T10:00:00Z"},
		{"event_id": "evt_102", "campaign_id": "c2", "contact_id": "u1", "type": "opened", "timestamp": "not-a-time"},
		{"event_id": "", "campaign_id": "c2", "contact_id": "u1", "type": "delivered", "timestamp": "2026-08-10T10:01:00Z"},
		{"event_id": "evt_103", "campaign_id": "c2", "contact_id": "", "type": "delivered", "timestamp": "2026-08-10T10:01:00Z"},
		{"event_id": "evt_104", "campaign_id": "c2", "contact_id": "u1", "type": "spam_report", "timestamp": "2026-08-10T10:02:00Z"},
		{"event_id": "evt_105", "campaign_id": "c2", "contact_id": "u1", "type": "delivered", "timestamp": "2026-08-10T10:03:00Z"}
	]`

	req := httptest.NewRequest("POST", "/events", bytes.NewBufferString(mixedBatch))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for partial success, got %d", rec.Code)
	}

	var resp BatchIngestResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Received != 6 {
		t.Fatalf("expected 6 received, got %d", resp.Received)
	}
	if resp.Accepted != 2 {
		t.Fatalf("expected 2 accepted (evt_101, evt_105), got %d", resp.Accepted)
	}
	if resp.Rejected != 4 {
		t.Fatalf("expected 4 rejected, got %d", resp.Rejected)
	}
	if len(resp.Errors) != 4 {
		t.Fatalf("expected 4 error details, got %d", len(resp.Errors))
	}
}

func TestConcurrentIngestion(t *testing.T) {
	_, mux := newTestServer()

	var wg sync.WaitGroup
	numWorkers := 10
	batchesPerWorker := 10

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for b := 0; b < batchesPerWorker; b++ {
				// Each worker sends unique events for campaign "conc_camp"
				payload := `[
					{"event_id": "evt_w` + string(rune('a'+workerID)) + `_` + string(rune('0'+b)) + `", "campaign_id": "conc_camp", "contact_id": "user_` + string(rune('a'+workerID)) + `", "type": "sent", "timestamp": "2026-08-10T10:00:00Z"}
				]`
				req := httptest.NewRequest("POST", "/events", bytes.NewBufferString(payload))
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("worker %d batch %d failed: %d", workerID, b, rec.Code)
				}
			}
		}(w)
	}
	wg.Wait()

	reqStats := httptest.NewRequest("GET", "/campaigns/conc_camp/stats", nil)
	recStats := httptest.NewRecorder()
	mux.ServeHTTP(recStats, reqStats)

	var stats CampaignStatsResponse
	if err := json.NewDecoder(recStats.Body).Decode(&stats); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}

	expectedSent := numWorkers * batchesPerWorker
	if stats.Sent != expectedSent {
		t.Fatalf("expected sent=%d, got %d", expectedSent, stats.Sent)
	}
}

func TestStatsAndRatesCalculation(t *testing.T) {
	_, mux := newTestServer()

	// 10 sent, 8 delivered, 4 opens by 2 distinct users, 2 clicks by 1 user
	payload := `[
		{"event_id": "s1", "campaign_id": "rate_cmp", "contact_id": "u1", "type": "sent", "timestamp": "2026-08-10T09:00:00Z"},
		{"event_id": "s2", "campaign_id": "rate_cmp", "contact_id": "u2", "type": "sent", "timestamp": "2026-08-10T09:01:00Z"},
		{"event_id": "d1", "campaign_id": "rate_cmp", "contact_id": "u1", "type": "delivered", "timestamp": "2026-08-10T09:02:00Z"},
		{"event_id": "d2", "campaign_id": "rate_cmp", "contact_id": "u2", "type": "delivered", "timestamp": "2026-08-10T09:03:00Z"},
		{"event_id": "o1", "campaign_id": "rate_cmp", "contact_id": "u1", "type": "opened", "timestamp": "2026-08-10T09:04:00Z"},
		{"event_id": "o2", "campaign_id": "rate_cmp", "contact_id": "u1", "type": "opened", "timestamp": "2026-08-10T09:05:00Z"},
		{"event_id": "o3", "campaign_id": "rate_cmp", "contact_id": "u2", "type": "opened", "timestamp": "2026-08-10T09:06:00Z"},
		{"event_id": "c1", "campaign_id": "rate_cmp", "contact_id": "u1", "type": "clicked", "timestamp": "2026-08-10T09:07:00Z"},
		{"event_id": "c2", "campaign_id": "rate_cmp", "contact_id": "u1", "type": "clicked", "timestamp": "2026-08-10T09:08:00Z"}
	]`

	req := httptest.NewRequest("POST", "/events", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	reqStats := httptest.NewRequest("GET", "/campaigns/rate_cmp/stats", nil)
	recStats := httptest.NewRecorder()
	mux.ServeHTTP(recStats, reqStats)

	var stats CampaignStatsResponse
	if err := json.NewDecoder(recStats.Body).Decode(&stats); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}

	if stats.Sent != 2 || stats.Delivered != 2 || stats.Opened != 3 || stats.Clicked != 2 {
		t.Fatalf("unexpected raw counts: %+v", stats)
	}
	if stats.UniqueOpens != 2 {
		t.Fatalf("expected 2 unique opens, got %d", stats.UniqueOpens)
	}
	if stats.UniqueClicks != 1 {
		t.Fatalf("expected 1 unique click, got %d", stats.UniqueClicks)
	}
	if stats.DeliveryRate != 1.0 {
		t.Fatalf("expected delivery_rate 1.0, got %f", stats.DeliveryRate)
	}
	if stats.OpenRate != 1.0 { // unique_opens / delivered = 2 / 2
		t.Fatalf("expected open_rate 1.0, got %f", stats.OpenRate)
	}
	if stats.ClickThroughRate != 0.5 { // unique_clicks / delivered = 1 / 2
		t.Fatalf("expected click_through_rate 0.5, got %f", stats.ClickThroughRate)
	}
	if stats.ClickToOpenRate != 0.5 { // unique_clicks / unique_opens = 1 / 2
		t.Fatalf("expected click_to_open_rate 0.5, got %f", stats.ClickToOpenRate)
	}
}

func TestPaginatedEvents(t *testing.T) {
	_, mux := newTestServer()

	payload := `[
		{"event_id": "e1", "campaign_id": "page_cmp", "contact_id": "u1", "type": "sent", "timestamp": "2026-08-10T10:00:00Z"},
		{"event_id": "e2", "campaign_id": "page_cmp", "contact_id": "u1", "type": "delivered", "timestamp": "2026-08-10T10:01:00Z"},
		{"event_id": "e3", "campaign_id": "page_cmp", "contact_id": "u1", "type": "opened", "timestamp": "2026-08-10T10:02:00Z"}
	]`
	req := httptest.NewRequest("POST", "/events", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Fetch with limit=2, offset=0
	reqPage := httptest.NewRequest("GET", "/campaigns/page_cmp/events?limit=2&offset=0", nil)
	recPage := httptest.NewRecorder()
	mux.ServeHTTP(recPage, reqPage)

	var pResp EventsListResponse
	if err := json.NewDecoder(recPage.Body).Decode(&pResp); err != nil {
		t.Fatalf("failed to decode page response: %v", err)
	}

	if pResp.Total != 3 || len(pResp.Events) != 2 {
		t.Fatalf("expected total=3, len=2; got total=%d len=%d", pResp.Total, len(pResp.Events))
	}

	// Test filtering by type=opened
	reqFilter := httptest.NewRequest("GET", "/campaigns/page_cmp/events?type=opened", nil)
	recFilter := httptest.NewRecorder()
	mux.ServeHTTP(recFilter, reqFilter)

	var fResp EventsListResponse
	if err := json.NewDecoder(recFilter.Body).Decode(&fResp); err != nil {
		t.Fatalf("failed to decode filtered response: %v", err)
	}
	if fResp.Total != 1 || fResp.Events[0].EventID != "e3" {
		t.Fatalf("expected 1 opened event (e3), got %+v", fResp)
	}
}

func TestSeedDataSurvives(t *testing.T) {
	_, mux := newTestServer()

	seedPath := "seed/events.json"
	data, err := os.ReadFile(seedPath)
	if err != nil {
		t.Skipf("skipping seed test: could not read %s: %v", seedPath, err)
		return
	}

	req := httptest.NewRequest("POST", "/events", bytes.NewBuffer(data))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from seed ingestion, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp BatchIngestResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode seed ingest response: %v", err)
	}

	if resp.Received != 236 {
		t.Logf("Seed received: %d events", resp.Received)
	}
	t.Logf("Seed ingestion summary: received=%d accepted=%d duplicates=%d rejected=%d errors=%d",
		resp.Received, resp.Accepted, resp.Duplicates, resp.Rejected, len(resp.Errors))

	if resp.Accepted == 0 {
		t.Fatalf("expected valid events to be accepted")
	}

	// Verify stats for cmp_summer_sale
	reqStats := httptest.NewRequest("GET", "/campaigns/cmp_summer_sale/stats", nil)
	recStats := httptest.NewRecorder()
	mux.ServeHTTP(recStats, reqStats)

	if recStats.Code != http.StatusOK {
		t.Fatalf("failed to get stats for cmp_summer_sale: %d", recStats.Code)
	}
}

