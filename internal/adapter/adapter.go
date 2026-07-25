// Package adapter defines the station-agnostic scraping contract and a factory.
package adapter

import (
	"context"
	"fmt"
	"net/http"

	"transitmonitor/internal/adapter/newapi"
	"transitmonitor/internal/adapter/sub2api"
	"transitmonitor/internal/domain"
)

// Adapter scrapes one station's ratios and normalizes them.
type Adapter interface {
	// ProbeCapabilities discovers which endpoints/auth succeed and fills a
	// CapabilityReport so the monitor can degrade gracefully.
	ProbeCapabilities(ctx context.Context) (domain.CapabilityReport, error)
	// FetchRatios uses the capability report to pick the richest available
	// ratio source, fetches it, normalizes to []RatioObservation, and returns
	// the RawSnapshot too (raw payloads + endpoint statuses).
	FetchRatios(ctx context.Context, caps domain.CapabilityReport) (domain.RawSnapshot, []domain.RatioObservation, error)
}

// JWTRefresher is optionally implemented by adapters that accept a dynamic JWT
// update (e.g. sub2api after auto-login).
type JWTRefresher interface {
	SetJWT(jwt string)
}

// NewAdapter builds the right adapter for a station's kind.
func NewAdapter(s domain.Station, client *http.Client) (Adapter, error) {
	switch s.Kind {
	case domain.KindNewAPI:
		return newapi.New(s.ID, s.BaseURL, s.Auth.PAT, s.Auth.UserID, s.Auth.APIKey, s.Auth.Group, client), nil
	case domain.KindSub2API:
		return sub2api.New(s.ID, s.BaseURL, s.Auth.APIKey, s.Auth.JWT, s.Auth.AdminAPIKey, s.Auth.Group, client), nil
	default:
		return nil, fmt.Errorf("unknown station kind %q for station %s", s.Kind, s.ID)
	}
}
