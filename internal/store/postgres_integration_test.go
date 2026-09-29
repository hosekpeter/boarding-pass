//go:build integration

package store

import (
	"context"
	"sync"
	"testing"

	"boarding-pass/internal/bcbp"
	"boarding-pass/internal/config"
	"boarding-pass/internal/db"
	"boarding-pass/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPersistenceFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	cfg := testutil.DatabaseConfig(t)
	s, pool := openTestStore(t, cfg)
	for _, pass := range []bcbp.BoardingPass{
		{PassengerName: "CYPRIAN/MICHAL", Legs: []bcbp.Leg{{AirlineCode: "FR"}}},
		{PassengerName: "SMITH/JANE", Legs: []bcbp.Leg{{AirlineCode: "BA"}, {AirlineCode: "FR"}}},
		{PassengerName: "CYPRIAN/JAN", Legs: []bcbp.Leg{{AirlineCode: "BA"}}},
	} {
		if err := s.Save(ctx, pass); err != nil {
			t.Fatal(err)
		}
	}
	pool.Close()
	s, pool = openTestStore(t, cfg)
	defer pool.Close()
	cases := []struct {
		name   string
		filter Filter
		total  int
		names  []string
	}{
		{"newest first", Filter{Limit: 20}, 3, []string{"CYPRIAN/JAN", "SMITH/JANE", "CYPRIAN/MICHAL"}},
		{"page", Filter{Limit: 1, Offset: 1}, 3, []string{"SMITH/JANE"}},
		{"case insensitive", Filter{Limit: 20, PassengerName: "cYpRiAn"}, 2, []string{"CYPRIAN/JAN", "CYPRIAN/MICHAL"}},
		{"any leg", Filter{Limit: 20, AirlineCode: "FR"}, 2, []string{"SMITH/JANE", "CYPRIAN/MICHAL"}},
		{"AND", Filter{Limit: 20, AirlineCode: "FR", PassengerName: "cyprian"}, 1, []string{"CYPRIAN/MICHAL"}},
		{"exact airline", Filter{Limit: 20, AirlineCode: "fr"}, 0, nil},
		{"past end", Filter{Limit: 20, Offset: 99}, 3, nil},
		{"literal wildcard", Filter{Limit: 20, PassengerName: "%"}, 0, nil},
		{"SQL injection", Filter{Limit: 20, PassengerName: "' OR 1=1 --"}, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.List(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != tc.total || len(page.Items) != len(tc.names) || page.Items == nil {
				t.Fatalf("unexpected page: %+v", page)
			}
			for i, r := range page.Items {
				if r.DecodedBCBP.PassengerName != tc.names[i] || r.ID == "" || r.ParsedAt.IsZero() {
					t.Fatalf("unexpected record: %+v", r)
				}
			}
		})
	}
}

func TestConcurrentSaves(t *testing.T) {
	s, pool := openTestStore(t, testutil.DatabaseConfig(t))
	defer pool.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Save(context.Background(), bcbp.BoardingPass{PassengerName: "TEST/USER", Legs: []bcbp.Leg{}}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	page, err := s.List(context.Background(), Filter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 20 || len(page.Items) != 20 {
		t.Fatalf("lost writes: %+v", page)
	}
	ids := map[string]bool{}
	for _, r := range page.Items {
		if ids[r.ID] {
			t.Fatal("duplicate ID")
		}
		ids[r.ID] = true
	}
}

func TestCanceledAndInvalidQueries(t *testing.T) {
	s, pool := openTestStore(t, testutil.DatabaseConfig(t))
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Save(ctx, bcbp.BoardingPass{}); err == nil {
		t.Fatal("expected canceled save")
	}
	if _, err := s.List(ctx, Filter{Limit: 20}); err == nil {
		t.Fatal("expected canceled list")
	}
	if _, err := s.List(context.Background(), Filter{Limit: 0}); err == nil {
		t.Fatal("expected invalid limit")
	}
}

func TestConcurrentInitialization(t *testing.T) {
	cfg := testutil.DatabaseConfig(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			pool, err := db.Connect(t.Context(), cfg)
			if err != nil {
				t.Errorf("concurrent initialization: %v", err)
				return
			}
			defer pool.Close()
			if err := db.RunMigrations(t.Context(), pool); err != nil {
				t.Errorf("concurrent migrations: %v", err)
				return
			}
			s := New(pool)
			if _, err := s.List(t.Context(), Filter{Limit: 20}); err != nil {
				t.Errorf("read initialized schema: %v", err)
			}
		})
	}
	wg.Wait()
}

func openTestStore(t *testing.T, cfg config.DBConfig) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool, err := db.Connect(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.RunMigrations(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return New(pool), pool
}
