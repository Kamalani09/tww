package main

import (
	"math"
	"sort"
	"sync"
	"time"
)

// CampaignStats internal state per campaign.
type campaignData struct {
	campaignID   string
	sent         int
	delivered    int
	opened       int
	clicked      int
	uniqueOpens  map[string]bool
	uniqueClicks map[string]bool
	lastEventAt  time.Time
}

// CampaignStatsResponse is the public response shape for GET /campaigns/{id}/stats.
type CampaignStatsResponse struct {
	CampaignID       string  `json:"campaign_id"`
	Sent             int     `json:"sent"`
	Delivered        int     `json:"delivered"`
	Opened           int     `json:"opened"`
	Clicked          int     `json:"clicked"`
	UniqueOpens      int     `json:"unique_opens"`
	UniqueClicks     int     `json:"unique_clicks"`
	DeliveryRate     float64 `json:"delivery_rate"`      // delivered / sent
	OpenRate         float64 `json:"open_rate"`          // unique_opens / delivered
	ClickThroughRate float64 `json:"click_through_rate"` // unique_clicks / delivered
	ClickToOpenRate  float64 `json:"click_to_open_rate"` // unique_clicks / unique_opens
	LastEventAt      string  `json:"last_event_at,omitempty"`
}

// EventsListResponse is the paginated response for GET /campaigns/{id}/events.
type EventsListResponse struct {
	CampaignID string  `json:"campaign_id"`
	Total      int     `json:"total"`
	Limit      int     `json:"limit"`
	Offset     int     `json:"offset"`
	Events     []Event `json:"events"`
}

// Store provides thread-safe in-memory storage for events and campaign statistics.
type Store struct {
	mu             sync.RWMutex
	seenEventIDs   map[string]struct{}
	campaigns      map[string]*campaignData
	campaignEvents map[string][]Event
}

// NewStore initializes a new Store.
func NewStore() *Store {
	return &Store{
		seenEventIDs:   make(map[string]struct{}),
		campaigns:      make(map[string]*campaignData),
		campaignEvents: make(map[string][]Event),
	}
}

// IngestResult captures the ingestion outcome for a single event.
type IngestResult int

const (
	ResultAccepted IngestResult = iota
	ResultDuplicate
)

// Ingest accepts a validated event, applying deduplication and updating stats.
func (s *Store) Ingest(ev Event) IngestResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Deduplication by event_id across the whole service lifetime.
	if _, seen := s.seenEventIDs[ev.EventID]; seen {
		return ResultDuplicate
	}
	s.seenEventIDs[ev.EventID] = struct{}{}

	// 2. Ensure campaign records exist.
	cd, ok := s.campaigns[ev.CampaignID]
	if !ok {
		cd = &campaignData{
			campaignID:   ev.CampaignID,
			uniqueOpens:  make(map[string]bool),
			uniqueClicks: make(map[string]bool),
		}
		s.campaigns[ev.CampaignID] = cd
	}

	// 3. Update last seen timestamp.
	if ev.Timestamp.After(cd.lastEventAt) {
		cd.lastEventAt = ev.Timestamp
	}

	// 4. Update counters.
	switch ev.Type {
	case TypeSent:
		cd.sent++
	case TypeDelivered:
		cd.delivered++
	case TypeOpened:
		cd.opened++
		cd.uniqueOpens[ev.ContactID] = true
	case TypeClicked:
		cd.clicked++
		cd.uniqueClicks[ev.ContactID] = true
	}

	// 5. Store for event log / paginated lookup.
	s.campaignEvents[ev.CampaignID] = append(s.campaignEvents[ev.CampaignID], ev)

	return ResultAccepted
}

// GetStats returns aggregated statistics for a specific campaign.
func (s *Store) GetStats(campaignID string) (*CampaignStatsResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cd, ok := s.campaigns[campaignID]
	if !ok {
		return nil, false
	}

	uOpens := len(cd.uniqueOpens)
	uClicks := len(cd.uniqueClicks)

	var delivRate, openRate, ctr, ctor float64
	if cd.sent > 0 {
		delivRate = round4(float64(cd.delivered) / float64(cd.sent))
	}
	if cd.delivered > 0 {
		openRate = round4(float64(uOpens) / float64(cd.delivered))
		ctr = round4(float64(uClicks) / float64(cd.delivered))
	}
	if uOpens > 0 {
		ctor = round4(float64(uClicks) / float64(uOpens))
	}

	var lastEventStr string
	if !cd.lastEventAt.IsZero() {
		lastEventStr = cd.lastEventAt.UTC().Format(time.RFC3339)
	}

	return &CampaignStatsResponse{
		CampaignID:       cd.campaignID,
		Sent:             cd.sent,
		Delivered:        cd.delivered,
		Opened:           cd.opened,
		Clicked:          cd.clicked,
		UniqueOpens:      uOpens,
		UniqueClicks:     uClicks,
		DeliveryRate:     delivRate,
		OpenRate:         openRate,
		ClickThroughRate: ctr,
		ClickToOpenRate:  ctor,
		LastEventAt:      lastEventStr,
	}, true
}

// GetEvents returns a paginated list of events for a campaign.
func (s *Store) GetEvents(campaignID string, limit, offset int, eventType string) (*EventsListResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events, ok := s.campaignEvents[campaignID]
	if !ok {
		return nil, false
	}

	var filtered []Event
	for _, ev := range events {
		if eventType == "" || ev.Type == eventType {
			filtered = append(filtered, ev)
		}
	}

	// Sort newest first by timestamp
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})

	total := len(filtered)
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 50
	} else if limit > 100 {
		limit = 100
	}

	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	page := make([]Event, end-start)
	copy(page, filtered[start:end])

	return &EventsListResponse{
		CampaignID: campaignID,
		Total:      total,
		Limit:      limit,
		Offset:     offset,
		Events:     page,
	}, true
}

func round4(val float64) float64 {
	return math.Round(val*10000) / 10000
}

