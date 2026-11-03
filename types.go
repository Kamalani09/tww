package main

import (
	"errors"
	"strings"
	"time"
)

// Allowed event types according to the business specification.
const (
	TypeSent      = "sent"
	TypeDelivered = "delivered"
	TypeOpened    = "opened"
	TypeClicked   = "clicked"
)

// Event represents an event payload sent by delivery providers.
type Event struct {
	EventID    string         `json:"event_id"`
	CampaignID string         `json:"campaign_id"`
	ContactID  string         `json:"contact_id"`
	Type       string         `json:"type"`
	Timestamp  time.Time      `json:"timestamp"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// rawEvent is used for raw decoding to handle flexible time parsing and field validation.
type rawEvent struct {
	EventID    string         `json:"event_id"`
	CampaignID string         `json:"campaign_id"`
	ContactID  string         `json:"contact_id"`
	Type       string         `json:"type"`
	Timestamp  any            `json:"timestamp"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Validate validates and normalizes an event.
func (r *rawEvent) toEvent() (Event, error) {
	if strings.TrimSpace(r.EventID) == "" {
		return Event{}, errors.New("event_id is required and cannot be empty")
	}
	if strings.TrimSpace(r.CampaignID) == "" {
		return Event{}, errors.New("campaign_id is required and cannot be empty")
	}
	if strings.TrimSpace(r.ContactID) == "" {
		return Event{}, errors.New("contact_id is required and cannot be empty")
	}

	normType := strings.ToLower(strings.TrimSpace(r.Type))
	switch normType {
	case TypeSent, TypeDelivered, TypeOpened, TypeClicked:
		// valid
	default:
		return Event{}, errors.New("invalid event type: must be sent, delivered, opened, or clicked")
	}

	if r.Timestamp == nil {
		return Event{}, errors.New("timestamp is required")
	}

	var parsedTime time.Time
	switch ts := r.Timestamp.(type) {
	case string:
		var err error
		parsedTime, err = time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			parsedTime, err = time.Parse(time.RFC3339, ts)
			if err != nil {
				return Event{}, errors.New("invalid timestamp format: must be RFC3339 / ISO8601")
			}
		}
	default:
		return Event{}, errors.New("invalid timestamp type: must be string")
	}

	return Event{
		EventID:    r.EventID,
		CampaignID: r.CampaignID,
		ContactID:  r.ContactID,
		Type:       normType,
		Timestamp:  parsedTime.UTC(),
		Metadata:   r.Metadata,
	}, nil
}
