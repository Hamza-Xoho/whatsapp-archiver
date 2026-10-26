package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

// The open-ended vocabularies of V1 are validated here, in Go, against one list each (R6): a new value then never needs
// a table rebuild of an insert-only table. The closed vocabularies (source, media state, alert kind, ...) are CHECKs.

// MessageKinds is the list of messages.kind values (schema.md section 3).
var MessageKinds = []string{"text", "image", "video", "audio", "voice", "document", "sticker", "gif", "location",
	"live_location", "contact", "contacts", "poll", "event", "system", "unknown"}

// EventKinds is the list of message_events.kind values.
var EventKinds = []string{"edit", "revoke", "protocol", "reaction", "pin", "poll_vote", "undecryptable", "secret_encrypted", "stub"}

// EventCauses is the list of message_events.cause values; "" is no cause (every kind but undecryptable).
// untrusted_identity is never produced (see W2(c) in docs/v1/STATE.md); it stays allowed so no table rebuild is needed.
var EventCauses = []string{"", "no_sender_key", "untrusted_identity", "view_once_unavailable", "other"}

// ConnectionStates is the list of connection_periods.state values; only non-ok states are recorded.
var ConnectionStates = []string{"degraded", "reconnecting", "disconnected", "logged_out", "client_outdated", "temp_banned",
	"stream_replaced", "not_running"}

// ConnectionCauseClasses is the list of connection_periods.cause_class values; "" is unknown.
var ConnectionCauseClasses = []string{"", "dns", "tcp", "tls", "ws", "keepalive", "server", "logout", "other", "not_running"}

func ValidMessageKind(k string) bool     { return slices.Contains(MessageKinds, k) }
func ValidEventKind(k string) bool       { return slices.Contains(EventKinds, k) }
func ValidEventCause(c string) bool      { return slices.Contains(EventCauses, c) }
func ValidConnectionState(s string) bool { return slices.Contains(ConnectionStates, s) }
func ValidConnectionCause(c string) bool { return slices.Contains(ConnectionCauseClasses, c) }

// The details structs follow schema.md section 4.2, field for field and in that order, so the marshalled form is
// deterministic. Numbers whose zero is a real value are pointers; everything absent is omitted. Task (c) builds
// them in the normaliser and marshals them with MarshalDetails.

// LinkDetails is the link preview of a text message.
type LinkDetails struct {
	URL         string `json:"url,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// TextDetails: kind text.
type TextDetails struct {
	Link *LinkDetails `json:"link,omitempty"`
}

// StickerDetails: kind sticker.
type StickerDetails struct {
	Animated *bool `json:"animated,omitempty"`
}

// VideoDetails: kind video (a video note has ptv).
type VideoDetails struct {
	PTV *bool `json:"ptv,omitempty"`
}

// LocationDetails: kind location.
type LocationDetails struct {
	Lat     *float64 `json:"lat,omitempty"`
	Lon     *float64 `json:"lon,omitempty"`
	Name    string   `json:"name,omitempty"`
	Address string   `json:"address,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// LiveLocationDetails: kind live_location.
type LiveLocationDetails struct {
	Lat       *float64 `json:"lat,omitempty"`
	Lon       *float64 `json:"lon,omitempty"`
	AccuracyM *float64 `json:"accuracy_m,omitempty"`
	Speed     *float64 `json:"speed,omitempty"`
	Sequence  *int64   `json:"sequence,omitempty"`
}

// ContactDetails: kind contact.
type ContactDetails struct {
	DisplayName string `json:"display_name,omitempty"`
	VCard       string `json:"vcard,omitempty"`
}

// ContactsDetails: kind contacts.
type ContactsDetails struct {
	DisplayName string           `json:"display_name,omitempty"`
	Contacts    []ContactDetails `json:"contacts,omitempty"`
}

// PollDetails: kind poll.
type PollDetails struct {
	Question string   `json:"question,omitempty"`
	Options  []string `json:"options,omitempty"`
	Max      *int     `json:"max,omitempty"`
}

// EventPlace is the location of an event message.
type EventPlace struct {
	Name string   `json:"name,omitempty"`
	Lat  *float64 `json:"lat,omitempty"`
	Lon  *float64 `json:"lon,omitempty"`
}

// EventDetails: kind event.
type EventDetails struct {
	Name        string      `json:"name,omitempty"`
	Description string      `json:"description,omitempty"`
	StartTS     *int64      `json:"start_ts,omitempty"`
	EndTS       *int64      `json:"end_ts,omitempty"`
	Location    *EventPlace `json:"location,omitempty"`
	Canceled    *bool       `json:"canceled,omitempty"`
}

// SystemDetails: kind system; Type is the proto field name (call log, scheduled call, ...).
type SystemDetails struct {
	Type string `json:"type,omitempty"`
}

// UnknownDetails: kind unknown; Fields are the top-level proto field names, listed on the Logging page.
type UnknownDetails struct {
	Fields []string `json:"fields,omitempty"`
}

// emptyDetails is the details of a kind with nothing extra: image, gif, audio, voice, document.
type emptyDetails struct{}

// detailsFor returns a pointer to a fresh details struct of the kind.
func detailsFor(kind string) any {
	switch kind {
	case "text":
		return &TextDetails{}
	case "sticker":
		return &StickerDetails{}
	case "video":
		return &VideoDetails{}
	case "location":
		return &LocationDetails{}
	case "live_location":
		return &LiveLocationDetails{}
	case "contact":
		return &ContactDetails{}
	case "contacts":
		return &ContactsDetails{}
	case "poll":
		return &PollDetails{}
	case "event":
		return &EventDetails{}
	case "system":
		return &SystemDetails{}
	case "unknown":
		return &UnknownDetails{}
	}
	return &emptyDetails{}
}

// ValidateDetails checks raw against the details of the kind, rejecting an unknown field or a field of the wrong
// type, and returns its canonical form: the struct marshalled in field order, no whitespace. "" means {}.
func ValidateDetails(kind, raw string) (string, error) {
	if !ValidMessageKind(kind) {
		return "", invalid("details of unknown message kind %q", kind)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}", nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var generic map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &generic); err != nil || generic == nil {
		return "", invalid("details of a %s message are not a JSON object", kind)
	}
	v := detailsFor(kind)
	if err := dec.Decode(v); err != nil {
		return "", invalid("details of a %s message: %v", kind, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", invalid("details of a %s message: trailing data", kind)
	}
	return MarshalDetails(v)
}

// MarshalDetails marshals a details struct deterministically (no HTML escaping, no trailing newline).
func MarshalDetails(v any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("marshal details: %w", err)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}
