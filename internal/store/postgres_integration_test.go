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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistenceFiltersAndPagination(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := testutil.DatabaseConfig(t)
	s, pool := openTestStore(t, cfg)
	for _, pass := range []bcbp.BoardingPass{
		{PassengerName: "CYPRIAN/MICHAL", Legs: []bcbp.Leg{{AirlineCode: "FR"}}},
		{PassengerName: "SMITH/JANE", Legs: []bcbp.Leg{{AirlineCode: "BA"}, {AirlineCode: "FR"}}},
		{PassengerName: "CYPRIAN/JAN", Legs: []bcbp.Leg{{AirlineCode: "BA"}}},
	} {
		require.NoError(t, s.Save(ctx, pass))
	}
	pool.Close()
	s, _ = openTestStore(t, cfg) // Cleanup keeps the pool open until all subtests finish.
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
			t.Parallel()

			page, err := s.List(ctx, tc.filter)
			require.NoError(t, err)
			assert.Equal(t, tc.total, page.Total)
			assert.NotNil(t, page.Items)
			require.Len(t, page.Items, len(tc.names))
			for i, r := range page.Items {
				assert.Equal(t, tc.names[i], r.DecodedBCBP.PassengerName)
				assert.NotEmpty(t, r.ID)
				assert.False(t, r.ParsedAt.IsZero())
			}
		})
	}
}

func TestConcurrentSaves(t *testing.T) {
	t.Parallel()

	s, pool := openTestStore(t, testutil.DatabaseConfig(t))
	defer pool.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, s.Save(context.Background(), bcbp.BoardingPass{PassengerName: "TEST/USER", Legs: []bcbp.Leg{}}))
		}()
	}
	wg.Wait()
	page, err := s.List(context.Background(), Filter{Limit: 100})
	require.NoError(t, err)
	assert.Equal(t, 20, page.Total)
	assert.Len(t, page.Items, 20)
	ids := map[string]bool{}
	for _, r := range page.Items {
		assert.NotContains(t, ids, r.ID, "duplicate ID")
		ids[r.ID] = true
	}
}

func TestCanceledAndInvalidQueries(t *testing.T) {
	t.Parallel()

	s, pool := openTestStore(t, testutil.DatabaseConfig(t))
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, s.Save(ctx, bcbp.BoardingPass{}), "expected canceled save")
	_, err := s.List(ctx, Filter{Limit: 20})
	assert.Error(t, err, "expected canceled list")
	_, err = s.List(context.Background(), Filter{Limit: 0})
	assert.Error(t, err, "expected invalid limit")
}

func TestConcurrentInitialization(t *testing.T) {
	t.Parallel()

	cfg := testutil.DatabaseConfig(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			pool, err := db.Connect(t.Context(), cfg)
			if !assert.NoError(t, err, "concurrent initialization") {
				return
			}
			defer pool.Close()
			if !assert.NoError(t, db.RunMigrations(t.Context(), pool), "concurrent migrations") {
				return
			}
			s := New(pool)
			_, err = s.List(t.Context(), Filter{Limit: 20})
			assert.NoError(t, err, "read initialized schema")
		})
	}
	wg.Wait()
}

func openTestStore(t *testing.T, cfg config.DBConfig) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool, err := db.Connect(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, db.RunMigrations(t.Context(), pool))
	return New(pool), pool
}
