package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"boarding-pass/internal/bcbp"
	"boarding-pass/internal/client/skypicker"
	"boarding-pass/internal/store"
)

var ErrInvalidBCBP = errors.New("invalid BCBP payload")

type Extractor interface {
	Extract(context.Context, []byte) (string, error)
}

type AirportLookup interface {
	Lookup(context.Context, string) (skypicker.Airport, error)
}

type Service struct {
	logger     *slog.Logger
	extractor  Extractor
	airports   AirportLookup
	repository store.Repository
}

func New(logger *slog.Logger, extractor Extractor, airports AirportLookup, repository store.Repository) *Service {
	return &Service{
		logger:     logger,
		extractor:  extractor,
		airports:   airports,
		repository: repository,
	}
}

func (s *Service) Parse(ctx context.Context, pdf []byte) (bcbp.BoardingPass, error) {
	var empty bcbp.BoardingPass
	payload, err := s.extractor.Extract(ctx, pdf)
	if err != nil {
		s.logger.Error("failed to extract boarding pass data", "error", err)
		return empty, err
	}

	pass, err := bcbp.Parse(payload)
	if err != nil {
		s.logger.Error("failed to parse boarding pass", "error", err)
		return empty, fmt.Errorf("%w: %v", ErrInvalidBCBP, err)
	}

	// Resolve each airport at most once for the entire itinerary.
	resolved := make(map[string]bcbp.Airport)
	for i := range pass.Legs {
		for _, airport := range []*bcbp.Airport{&pass.Legs[i].Origin, &pass.Legs[i].Destination} {
			value, ok := resolved[airport.Code]
			if !ok {
				details, err := s.airports.Lookup(ctx, airport.Code)
				if err != nil {
					s.logger.Error("failed to lookup airport", "error", err)
					return empty, err
				}
				value = bcbp.Airport{
					Code: details.Code, AirportName: details.AirportName,
					CityName: details.CityName, Country: details.Country,
				}
				resolved[airport.Code] = value
			}
			*airport = value
		}
	}

	// A successful response means the fully enriched result has been committed.
	// Failed parses or airport lookups never create partial history entries.
	if err := s.repository.Save(ctx, pass); err != nil {
		return empty, fmt.Errorf("save boarding pass: %w", err)
	}

	s.logger.InfoContext(ctx, "boarding pass parsed successfully")

	return pass, nil
}
