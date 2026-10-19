package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ucli.local/ucli/internal/core"
)

type TaskSummary struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Revision  int            `json:"revision"`
	Workspace core.Workspace `json:"workspace"`
	Status    string         `json:"status"`
	HasDraft  bool           `json:"has_draft"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type TaskSummaryPage struct {
	Tasks      []TaskSummary `json:"tasks"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type taskCursor struct {
	Version int    `json:"v"`
	Query   string `json:"query"`
	Updated string `json:"updated"`
	ID      string `json:"id"`
}

func queryFingerprint(query string) string {
	hash := sha256.Sum256([]byte(query))
	return hex.EncodeToString(hash[:])
}

func decodeTaskCursor(value, query string) (taskCursor, error) {
	var cursor taskCursor
	if value == "" {
		return cursor, nil
	}
	invalid := func() (taskCursor, error) {
		return taskCursor{}, problem("invalid_input", "Task cursor is invalid or belongs to a different search. Restart the task list.")
	}
	if len(value) > 2048 {
		return invalid()
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != value || json.Unmarshal(raw, &cursor) != nil {
		return invalid()
	}
	canonical, err := json.Marshal(cursor)
	// Only our canonical encoding is accepted: no duplicate/unknown fields or
	// case aliases. This is an opaque position, not an authorization token.
	if err != nil || string(canonical) != string(raw) || cursor.Version != 1 || cursor.Query != queryFingerprint(query) || !validID(cursor.ID) || len(cursor.Updated) > 64 {
		return invalid()
	}
	updated, err := time.Parse(time.RFC3339Nano, cursor.Updated)
	if err != nil || updated.IsZero() {
		return invalid()
	}
	return cursor, nil
}

func encodeTaskCursor(cursor taskCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func literalTitlePattern(query string) string {
	var pattern strings.Builder
	pattern.WriteByte('%')
	for _, r := range query {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r == '\\' || r == '%' || r == '_' {
			pattern.WriteByte('\\')
		}
		pattern.WriteRune(r)
	}
	pattern.WriteByte('%')
	return pattern.String()
}

// summaryRows projects metadata inside SQLite; draft text never crosses this
// boundary. Search deliberately scans O(history bytes), including JSON parsing,
// until enough matches are found. A future indexed summary/title migration is
// the upgrade path; no full-history Go allocation or new schema is used here.
// Invalid/oversized records are retained in the scan and fail visibly, including
// nonmatching records encountered before the next match.
func summaryRows(ctx context.Context, conn executor, cursor taskCursor, match string, matchArgs ...any) (*sql.Rows, error) {
	query := `WITH candidates AS (
		SELECT id,revision,updated_at,
		length(CAST(body AS BLOB))>? AS oversized,
		CASE WHEN length(CAST(body AS BLOB))<=? AND json_valid(body) THEN body END AS valid_body
		FROM tasks`
	args := []any{maxHistoryBytes, maxHistoryBytes}
	if cursor.ID != "" {
		query += " WHERE (updated_at,id)<(?,?)"
		args = append(args, cursor.Updated, cursor.ID)
	}
	query += `) SELECT CASE WHEN length(CAST(id AS BLOB))<=256 THEN id END,
		revision,CASE WHEN length(CAST(updated_at AS BLOB))<=64 THEN updated_at END,oversized,
		CASE WHEN valid_body IS NOT NULL THEN json_object(
		'id',json_extract(valid_body,'$.id'),
		'title',json_extract(valid_body,'$.title'),
		'revision',json_extract(valid_body,'$.revision'),
		'workspace',json_object('id',json_extract(valid_body,'$.workspace.id'),
			'root',json_extract(valid_body,'$.workspace.root'),
			'directory',json_extract(valid_body,'$.workspace.directory')),
		'status',json_extract(valid_body,'$.status'),
		'updated_at',json_extract(valid_body,'$.updated_at')) END,
		json_type(valid_body,'$.draft'),
		length(CAST(json_extract(valid_body,'$.draft') AS BLOB)),
		CASE WHEN valid_body IS NOT NULL THEN NOT EXISTS(SELECT 1 FROM json_tree(valid_body)
			WHERE path IN ('$','$.workspace') GROUP BY path,key HAVING count(*)>1) ELSE 0 END,
		coalesce((` + match + `),0)
		FROM candidates ORDER BY updated_at DESC,id DESC`
	args = append(args, matchArgs...)
	return conn.QueryContext(ctx, query, args...)
}

func readSummary(rows *sql.Rows) (TaskSummary, string, int, error) {
	var summary TaskSummary
	var id, indexedTime sql.NullString
	var revision int
	var oversized, uniqueMetadata, match bool
	var raw, draftType sql.NullString
	var draftBytes sql.NullInt64
	if err := rows.Scan(&id, &revision, &indexedTime, &oversized, &raw, &draftType, &draftBytes, &uniqueMetadata, &match); err != nil {
		return summary, "", 0, problem("state_corrupt", "A task summary contains an invalid stored column type.")
	}
	if oversized || len(raw.String) > maxHistoryBytes {
		return summary, "", 0, historyTooLarge()
	}
	corrupt := func() (TaskSummary, string, int, error) {
		return TaskSummary{}, "", 0, problem("state_corrupt", "A task summary is damaged or disagrees with its indexed identity, revision or update time.")
	}
	if !uniqueMetadata || !raw.Valid || !validText(raw.String, maxHistoryBytes) || json.Unmarshal([]byte(raw.String), &summary) != nil {
		return corrupt()
	}
	updated, err := time.Parse(time.RFC3339Nano, indexedTime.String)
	if !id.Valid || !indexedTime.Valid || err != nil || updated.IsZero() || !summary.UpdatedAt.Equal(updated) || summary.ID != id.String || !validID(id.String) || revision < 1 || summary.Revision != revision || summary.Title == "" || !validText(summary.Title, 1024) || validateWorkspace(summary.Workspace) != nil {
		return corrupt()
	}
	switch summary.Status {
	case "draft", "running", "complete", "failed", "interrupted", "unknown":
	default:
		return corrupt()
	}
	if draftType.Valid {
		if draftType.String != "text" || !draftBytes.Valid || draftBytes.Int64 < 0 || draftBytes.Int64 > maxInput {
			return corrupt()
		}
		summary.HasDraft = draftBytes.Int64 > 0
	}
	if !match {
		return summary, indexedTime.String, 0, nil
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return corrupt()
	}
	return summary, indexedTime.String, len(encoded), nil
}

// ListTaskSummaries returns newest-first metadata using the existing text index.
// query is a literal title or ID substring with ASCII-only case folding; it is bounded
// to 256 UTF-8 bytes without NUL. Empty query lists all tasks. Cursor is exclusive,
// bound to the exact query, and describes a live list, not a historical snapshot:
// edits/new tasks can move above an older cursor and require a fresh listing.
// Stored timestamp spelling is preserved. Legacy mixed-fraction/offset spellings
// can have lexical order different from chronological order; normalization would
// require an explicit migration rather than silently changing this index here.
func (s *Store) ListTaskSummaries(ctx context.Context, query, cursor string, limit int) (TaskSummaryPage, error) {
	if !validText(query, 256) || limit < 1 || limit > maxHistoryRows {
		return TaskSummaryPage{}, problem("invalid_input", "Task search requires valid UTF-8 without NUL, at most 256 bytes, and limit 1-1000.")
	}
	position, err := decodeTaskCursor(cursor, query)
	if err != nil {
		return TaskSummaryPage{}, err
	}
	page := TaskSummaryPage{Tasks: []TaskSummary{}}
	err = s.read(ctx, func(conn executor) error {
		var epoch int
		if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&epoch); err != nil {
			return err
		}
		if epoch == 2 {
			var err error
			page, err = materializedSummaries(ctx, conn, query, position, limit)
			return err
		}
		pattern := literalTitlePattern(query)
		rows, err := summaryRows(ctx, conn, position, `(lower(json_extract(valid_body,'$.title')) LIKE ? ESCAPE '\' OR lower(id) LIKE ? ESCAPE '\')`, pattern, pattern)
		if err != nil {
			return err
		}
		defer rows.Close()
		bytes := 0
		var last taskCursor
		for rows.Next() {
			summary, indexedTime, size, err := readSummary(rows)
			if err != nil {
				return err
			}
			if size == 0 {
				continue
			}
			if len(page.Tasks) == limit {
				page.NextCursor = encodeTaskCursor(last)
				return nil
			}
			if size > maxHistoryBytes-bytes {
				return historyTooLarge()
			}
			bytes += size
			page.Tasks = append(page.Tasks, summary)
			last = taskCursor{Version: 1, Query: queryFingerprint(query), Updated: indexedTime, ID: summary.ID}
		}
		return rows.Err()
	})
	if err != nil {
		return TaskSummaryPage{}, err
	}
	return page, nil
}

// RecentDraft finds the newest nonempty draft in the stored execution directory.
// Windows compares cleaned paths with Go Unicode EqualFold, preserving existing
// restore behavior; Linux/macOS use exact spelling. The caller resolves its path.
// It scans metadata once and loads only the selected full task in the same fenced
// transaction; nil means none. The live index/order and scan ceilings above apply.
func (s *Store) RecentDraft(ctx context.Context, directory string) (*core.Task, error) {
	if directory == "" || !validText(directory, 32768) {
		return nil, problem("invalid_input", "Draft lookup requires a nonempty UTF-8 directory without NUL, at most 32768 bytes.")
	}
	var found *core.Task
	err := s.read(ctx, func(conn executor) error {
		var epoch int
		if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&epoch); err != nil {
			return err
		}
		if epoch == 2 {
			var err error
			found, err = materializedRecentDraft(ctx, conn, directory)
			return err
		}
		rows, err := summaryRows(ctx, conn, taskCursor{}, `length(CAST(json_extract(valid_body,'$.draft') AS BLOB))>0`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			summary, _, size, err := readSummary(rows)
			if err != nil {
				return err
			}
			if size == 0 || !sameDraftDirectory(summary.Workspace.Dir, directory) {
				continue
			}
			if err = rows.Close(); err != nil {
				return err
			}
			task, err := loadTask(ctx, conn, summary.ID)
			if err != nil {
				return err
			}
			if err := validateSelectedDraft(task, summary); err != nil {
				return err
			}
			found = &task
			return nil
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

func validateSelectedDraft(task core.Task, summary TaskSummary) error {
	if task.ID != summary.ID || task.Revision != summary.Revision || !task.UpdatedAt.Equal(summary.UpdatedAt) || task.Workspace != summary.Workspace || task.Title != summary.Title || task.Status != summary.Status || task.Draft == "" || !validText(task.Draft, maxInput) || core.ValidateSelection(task.Selection) != nil {
		return problem("state_corrupt", "The selected draft is damaged or disagrees with its task summary.")
	}
	return nil
}

func sameDraftDirectory(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return left == right
}
