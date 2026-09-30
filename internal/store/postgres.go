package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"boarding-pass/internal/bcbp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ db *pgxpool.Pool }

var _ Repository = (*Store)(nil)

type Record struct {
	ID          string            `json:"id" format:"uuid"`
	ParsedAt    time.Time         `json:"parsed_at" format:"date-time"`
	DecodedBCBP bcbp.BoardingPass `json:"decoded_bcbp"`
}

type Filter struct {
	Limit         int
	Offset        int
	PassengerName string
	AirlineCode   string
}

type Page struct {
	Items  []Record `json:"items"`
	Total  int      `json:"total" minimum:"0" example:"1"`
	Limit  int      `json:"limit" minimum:"1" maximum:"100" example:"20"`
	Offset int      `json:"offset" minimum:"0" maximum:"2147483647" example:"0"`
}

// New uses the pool owned by ServiceScope; migrations run before construction.
func New(pool *pgxpool.Pool) *Store { return &Store{db: pool} }

func (s *Store) Save(ctx context.Context, pass bcbp.BoardingPass) error {
	payload, err := json.Marshal(pass)
	if err != nil {
		return err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO boarding_passes(id, parsed_at, passenger_name, decoded_bcbp) VALUES ($1, $2, $3, $4)`,
		id.String(), time.Now().UTC(), pass.PassengerName, string(payload))
	return err
}

func (s *Store) List(ctx context.Context, f Filter) (Page, error) {
	page := Page{Items: []Record{}, Limit: f.Limit, Offset: f.Offset}
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 {
		return page, fmt.Errorf("invalid pagination")
	}

	// Both queries must see the same snapshot even while other clients write.
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return page, err
	}
	defer tx.Rollback(ctx)

	// strpos implements literal substring matching, so % and _ are not wildcards.
	const where = ` WHERE ($1 = '' OR strpos(lower(passenger_name), lower($1)) > 0)
 AND ($2 = '' OR EXISTS (
  SELECT 1 FROM jsonb_array_elements(decoded_bcbp->'legs') AS leg
  WHERE leg.value->>'airline_code' = $2
 ))`
	args := []any{f.PassengerName, f.AirlineCode}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM boarding_passes`+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.Query(ctx, `SELECT id, parsed_at, decoded_bcbp FROM boarding_passes`+where+
		` ORDER BY parsed_at DESC, sequence DESC LIMIT $3 OFFSET $4`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Record
		var timestamp time.Time
		var payload string
		if err := rows.Scan(&r.ID, &timestamp, &payload); err != nil {
			return page, err
		}
		r.ParsedAt = timestamp.UTC()
		if err := json.Unmarshal([]byte(payload), &r.DecodedBCBP); err != nil {
			return page, err
		}
		page.Items = append(page.Items, r)
	}

	if err := rows.Err(); err != nil {
		return page, err
	}

	return page, tx.Commit(ctx)
}
