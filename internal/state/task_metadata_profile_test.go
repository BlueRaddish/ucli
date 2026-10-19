package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMetadataQueryProfile is opt-in and reads only the explicit, hash-verified
// synthetic timing fixture. It records component costs, not packaged CLI SLOs.
// Run with UCLI_STATE_PROFILE_FIXTURE pointing to the completed seeded home.
func TestMetadataQueryProfile(t *testing.T) {
	home := os.Getenv("UCLI_STATE_PROFILE_FIXTURE")
	if home == "" {
		t.Skip("set UCLI_STATE_PROFILE_FIXTURE to a completed synthetic timing home")
	}
	marker, err := os.ReadFile(filepath.Join(home, ".ucli-timing-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Kind, Status string
		Tasks        int
		Hash         string `json:"database_sha256"`
	}
	if json.Unmarshal(marker, &fixture) != nil || fixture.Kind != "ucli-synthetic-timing" || fixture.Status != "complete" || fixture.Tasks != 10000 || len(fixture.Hash) != 64 {
		t.Fatal("not a completed 10k synthetic timing fixture")
	}
	path := filepath.Join(home, "state", "state.db")
	verify := func() {
		t.Helper()
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil || closeErr != nil || hex.EncodeToString(h.Sum(nil)) != fixture.Hash {
			t.Fatal("fixture hash mismatch", err, closeErr)
		}
	}
	verify()
	defer verify()
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatal("fixture must be an idle standalone database", suffix, err)
		}
	}
	u := url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/"), RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if os.Getenv("UCLI_STATE_PROFILE_MODE") == "projection" {
		profileMetadataProjection(t, ctx, db)
		return
	}
	queries := []struct{ name, sql string }{
		{"recent_left_join", `SELECT EXISTS(SELECT 1 FROM tasks t INDEXED BY tasks_recent LEFT JOIN task_metadata m ON m.id=t.id WHERE m.id IS NULL) OR EXISTS(SELECT 1 FROM task_metadata m INDEXED BY task_metadata_recent LEFT JOIN tasks t ON t.id=m.id WHERE t.id IS NULL)`},
		{"automatic_left_join", `SELECT EXISTS(SELECT 1 FROM tasks t LEFT JOIN task_metadata m ON m.id=t.id WHERE m.id IS NULL) OR EXISTS(SELECT 1 FROM task_metadata m LEFT JOIN tasks t ON t.id=m.id WHERE t.id IS NULL)`},
		{"symmetric_except", `SELECT EXISTS(SELECT id FROM tasks WHERE id IS NULL) OR EXISTS(SELECT id FROM task_metadata WHERE id IS NULL) OR EXISTS(SELECT id FROM tasks EXCEPT SELECT id FROM task_metadata) OR EXISTS(SELECT id FROM task_metadata EXCEPT SELECT id FROM tasks)`},
		{"ordered_except", `SELECT EXISTS(SELECT id FROM tasks WHERE id IS NULL) OR EXISTS(SELECT id FROM task_metadata WHERE id IS NULL) OR EXISTS(SELECT id FROM tasks EXCEPT SELECT id FROM task_metadata ORDER BY id) OR EXISTS(SELECT id FROM task_metadata EXCEPT SELECT id FROM tasks ORDER BY id)`},
		{"union_group", `SELECT EXISTS(SELECT id FROM (SELECT id FROM tasks UNION ALL SELECT id FROM task_metadata) GROUP BY id HAVING id IS NULL OR count(*)<>2)`},
	}
	for _, q := range queries {
		rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q.sql)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("PLAN %s %s", q.name, strings.Join(plan, " | "))
	}
	type sample struct {
		Case         string  `json:"case"`
		Iteration    int     `json:"iteration"`
		Milliseconds float64 `json:"milliseconds"`
		Rows         int     `json:"rows,omitempty"`
	}
	var samples []sample
	for i := 0; i < 30; i++ {
		for n := range queries {
			q := queries[(n+i)%len(queries)]
			start := time.Now()
			var mismatch bool
			if err := db.QueryRowContext(ctx, q.sql).Scan(&mismatch); err != nil || mismatch {
				t.Fatal(q.name, mismatch, err)
			}
			samples = append(samples, sample{q.name, i, float64(time.Since(start)) / float64(time.Millisecond), 0})
		}
		for _, query := range []string{"", "ucli-profile-no-match-20261018"} {
			start := time.Now()
			page, err := materializedSummaries(ctx, db, query, taskCursor{}, 20)
			if err != nil {
				t.Fatal(err)
			}
			name := "list_matching"
			want := 20
			if query != "" {
				name = "list_missing"
				want = 0
			}
			if len(page.Tasks) != want {
				t.Fatal(name, "unexpected rows", len(page.Tasks))
			}
			samples = append(samples, sample{name, i, float64(time.Since(start)) / float64(time.Millisecond), len(page.Tasks)})
		}
	}
	data, err := json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("METADATA_PROFILE " + string(data))
}

func profileMetadataProjection(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	type sample struct {
		Case         string  `json:"case"`
		Iteration    int     `json:"iteration"`
		Milliseconds float64 `json:"milliseconds"`
		Rows         int     `json:"rows"`
	}
	cases := []struct{ name, projection, query string }{
		{"legacy_matching", legacyMetadataProjection(), ""},
		{"current_matching", metadataReadColumns, ""},
		{"legacy_missing", legacyMetadataProjection(), "ucli-profile-no-match-20261018"},
		{"current_missing", metadataReadColumns, "ucli-profile-no-match-20261018"},
	}
	var samples []sample
	for i := 0; i < 30; i++ {
		for n := range cases {
			c := cases[(n+i)%len(cases)]
			start := time.Now()
			page, err := materializedSummaries(ctx, metadataProjectionExecutor{db, c.projection}, c.query, taskCursor{}, 20)
			elapsed := time.Since(start)
			want := 0
			if c.query == "" {
				want = 20
			}
			if err != nil || len(page.Tasks) != want || (page.NextCursor != "") != (want == 20) {
				t.Fatal(c.name, "unexpected page", len(page.Tasks), page.NextCursor, err)
			}
			samples = append(samples, sample{c.name, i, float64(elapsed) / float64(time.Millisecond), len(page.Tasks)})
		}
	}
	data, err := json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("METADATA_PROFILE " + string(data))
}
