package chaos

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	baseURL = "http://localhost:8080"
)

type TestContainers struct {
	redisContainer testcontainers.Container
	pgContainer    testcontainers.Container
	redisURL       string
	pgURL          string
}

func setupContainers(t *testing.T) *TestContainers {
	ctx := context.Background()

	redisReq := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		Cmd:          []string{"redis-server", "--appendonly", "yes", "--appendfsync", "everysec"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	redisContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: redisReq,
		Started:          true,
	})
	require.NoError(t, err)

	redisHost, err := redisContainer.Host(ctx)
	require.NoError(t, err)
	redisPort, err := redisContainer.MappedPort(ctx, "6379")
	require.NoError(t, err)

	pgReq := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "testuser",
			"POSTGRES_PASSWORD": "testpass",
			"POSTGRES_DB":       "testdb",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections"),
	}

	pgContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: pgReq,
		Started:          true,
	})
	require.NoError(t, err)

	pgHost, err := pgContainer.Host(ctx)
	require.NoError(t, err)
	pgPort, err := pgContainer.MappedPort(ctx, "5432")
	require.NoError(t, err)

	return &TestContainers{
		redisContainer: redisContainer,
		pgContainer:    pgContainer,
		redisURL:       fmt.Sprintf("redis://%s:%s", redisHost, redisPort.Port()),
		pgURL:          fmt.Sprintf("postgres://testuser:testpass@%s:%s/testdb?sslmode=disable", pgHost, pgPort.Port()),
	}
}

func (tc *TestContainers) cleanup(t *testing.T) {
	ctx := context.Background()
	if tc.redisContainer != nil {
		tc.redisContainer.Terminate(ctx)
	}
	if tc.pgContainer != nil {
		tc.pgContainer.Terminate(ctx)
	}
}

func TestRedisCrashMidSale(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	tc := setupContainers(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	pg, err := pgxpool.New(ctx, tc.pgURL)
	require.NoError(t, err)
	defer pg.Close()

	rdb := redis.NewClient(&redis.Options{Addr: tc.redisURL[8:]})
	defer rdb.Close()

	saleID := uuid.New().String()
	totalStock := 50

	_, err = pg.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS sales (
			id TEXT PRIMARY KEY,
			total_stock INT NOT NULL,
			status TEXT NOT NULL
		)
	`)
	require.NoError(t, err)

	_, err = pg.Exec(ctx, `
		INSERT INTO sales (id, total_stock, status) VALUES ($1, $2, 'active')
	`, saleID, totalStock)
	require.NoError(t, err)

	err = rdb.Set(ctx, fmt.Sprintf("sale:%s:available", saleID), totalStock, 0).Err()
	require.NoError(t, err)
	err = rdb.Set(ctx, fmt.Sprintf("sale:%s:reserved", saleID), 0, 0).Err()
	require.NoError(t, err)

	var successCount atomic.Int32
	var errorCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := http.Post(
				fmt.Sprintf("%s/api/sales/%s/reserve", baseURL, saleID),
				"application/json",
				nil,
			)
			if err != nil {
				errorCount.Add(1)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				successCount.Add(1)
			} else {
				errorCount.Add(1)
			}
		}(i)

		if i == 20 {
			time.Sleep(200 * time.Millisecond)
			t.Log("Killing Redis container...")
			err := tc.redisContainer.Stop(ctx, nil)
			require.NoError(t, err)
		}
	}

	wg.Wait()

	t.Logf("Success: %d, Errors: %d", successCount.Load(), errorCount.Load())

	time.Sleep(2 * time.Second)
	t.Log("Restarting Redis...")
	err = tc.redisContainer.Start(ctx)
	require.NoError(t, err)

	startRecovery := time.Now()
	for i := 0; i < 30; i++ {
		resp, err := http.Get(fmt.Sprintf("%s/healthz", baseURL))
		if err == nil && resp.StatusCode == http.StatusOK {
			recoveryTime := time.Since(startRecovery)
			t.Logf("Recovery time: %v", recoveryTime)
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(1 * time.Second)
	}

	var confirmedCount int
	err = pg.QueryRow(ctx, `
		SELECT COUNT(*) FROM orders WHERE sale_id = $1 AND status IN ('pending', 'paid')
	`, saleID).Scan(&confirmedCount)
	require.NoError(t, err)

	assert.LessOrEqual(t, confirmedCount, totalStock, "Overselling detected!")
	t.Logf("Confirmed orders: %d (max: %d)", confirmedCount, totalStock)
}

func TestReconciliationCorrectedDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	tc := setupContainers(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	pg, err := pgxpool.New(ctx, tc.pgURL)
	require.NoError(t, err)
	defer pg.Close()

	rdb := redis.NewClient(&redis.Options{Addr: tc.redisURL[8:]})
	defer rdb.Close()

	saleID := uuid.New().String()
	totalStock := 100

	_, err = pg.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS sales (
			id TEXT PRIMARY KEY,
			total_stock INT NOT NULL,
			status TEXT NOT NULL
		)
	`)
	require.NoError(t, err)

	_, err = pg.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS orders (
			id TEXT PRIMARY KEY,
			sale_id TEXT NOT NULL,
			status TEXT NOT NULL
		)
	`)
	require.NoError(t, err)

	_, err = pg.Exec(ctx, `
		INSERT INTO sales (id, total_stock, status) VALUES ($1, $2, 'active')
	`, saleID, totalStock)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		_, err = pg.Exec(ctx, `
			INSERT INTO orders (id, sale_id, status) VALUES ($1, $2, 'paid')
		`, uuid.New().String(), saleID)
		require.NoError(t, err)
	}

	err = rdb.Set(ctx, fmt.Sprintf("sale:%s:available", saleID), 999, 0).Err()
	require.NoError(t, err)

	t.Log("Waiting for reconciliation cycle (65 seconds)...")
	time.Sleep(65 * time.Second)

	available, err := rdb.Get(ctx, fmt.Sprintf("sale:%s:available", saleID)).Int()
	require.NoError(t, err)

	assert.Equal(t, 90, available, "Reconciliation should correct drift to 90 (100 - 10 confirmed)")
	t.Logf("Available after reconciliation: %d", available)

	var auditCount int
	err = pg.QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_log WHERE event_type = 'reconciliation_correction' AND entity_id = $1
	`, saleID).Scan(&auditCount)
	if err == nil {
		assert.Greater(t, auditCount, 0, "Audit log should have reconciliation_correction entry")
	}
}

func TestRateLimiterBurst(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	userID := uuid.New().String()
	client := &http.Client{Timeout: 5 * time.Second}

	var successCount atomic.Int32
	var rateLimitedCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/sales/test-sale/reserve", baseURL), nil)
			req.Header.Set("X-User-ID", userID)

			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				successCount.Add(1)
			} else if resp.StatusCode == http.StatusTooManyRequests {
				rateLimitedCount.Add(1)
				retryAfter := resp.Header.Get("Retry-After")
				assert.Equal(t, "1", retryAfter, "Retry-After header should be 1")
			}
		}()
	}

	wg.Wait()

	t.Logf("Success: %d, Rate limited: %d", successCount.Load(), rateLimitedCount.Load())
	assert.LessOrEqual(t, int(successCount.Load()), 10, "Should allow at most 10 requests (bucket capacity)")
	assert.GreaterOrEqual(t, int(rateLimitedCount.Load()), 90, "Should rate limit at least 90 requests")

	t.Log("Waiting 5 seconds for bucket refill...")
	time.Sleep(5 * time.Second)

	successCount.Store(0)
	rateLimitedCount.Store(0)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/sales/test-sale/reserve", baseURL), nil)
			req.Header.Set("X-User-ID", userID)

			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				successCount.Add(1)
			} else if resp.StatusCode == http.StatusTooManyRequests {
				rateLimitedCount.Add(1)
			}
		}()
	}

	wg.Wait()

	t.Logf("After refill - Success: %d, Rate limited: %d", successCount.Load(), rateLimitedCount.Load())
	assert.Equal(t, int32(10), successCount.Load(), "All 10 requests should succeed after refill")
}

type SaleResponse struct {
	ID            string `json:"id"`
	TotalStock    int    `json:"total_stock"`
	Available     int    `json:"available"`
	Reserved      int    `json:"reserved"`
	Confirmed     int    `json:"confirmed"`
	Status        string `json:"status"`
}

func getSale(saleID string) (*SaleResponse, error) {
	resp, err := http.Get(fmt.Sprintf("%s/api/sales/%s", baseURL, saleID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var sale SaleResponse
	if err := json.NewDecoder(resp.Body).Decode(&sale); err != nil {
		return nil, err
	}

	return &sale, nil
}
