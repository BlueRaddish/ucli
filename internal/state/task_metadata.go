package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"ucli.local/ucli/internal/core"
)

const metadataColumns = "id,revision,updated_at,body_id,body_revision,body_updated_at,title,workspace_id,workspace_root,workspace_dir,status,draft_type,draft_bytes,oversized,unique_metadata,valid_types"

func metadataProjection() string {
	limits := map[string]int{"id": 256, "updated_at": 64, "body_id": 256, "body_updated_at": 64, "title": 1024, "workspace_id": 256, "workspace_root": 32768, "workspace_dir": 32768, "status": 32, "draft_type": 16}
	parts, guards := []string{}, []string{}
	for _, column := range strings.Split(metadataColumns, ",") {
		if column == "valid_types" {
			continue
		}
		var valid, expression string
		if limit, ok := limits[column]; ok {
			valid = "typeof(" + column + ")='text' AND length(CAST(" + column + " AS BLOB))<=" + strconv.Itoa(limit)
			expression = "CASE WHEN " + valid + " THEN " + column + " END"
		} else {
			valid = "typeof(" + column + ")='integer'"
			if column == "oversized" || column == "unique_metadata" {
				valid += " AND " + column + " IN (0,1)"
			}
			expression = "CASE WHEN " + valid + " THEN " + column + " END"
		}
		parts = append(parts, expression)
		// Mandatory fields already reject a failed CASE through readMetadata's
		// .Valid checks or bool Scan. Nullable draft fields need their raw guards:
		// otherwise two invalid values could both become an apparently absent draft.
		if column == "draft_type" || column == "draft_bytes" {
			guards = append(guards, "("+column+" IS NULL OR ("+valid+"))")
		}
	}
	parts = append(parts, "CASE WHEN typeof(valid_types)='integer' AND valid_types=1 AND "+strings.Join(guards, " AND ")+" THEN 1 ELSE 0 END")
	return strings.Join(parts, ",")
}

var metadataReadColumns = metadataProjection()

// The helper stores typed, bounded metadata, never a draft or rewritten task.
// Read paths still validate every encountered metadata row so damaged nonmatches
// remain visible. This is O(metadata rows), not an indexed substring search; an
// indexed search/certified validity design is a later explicit schema change.
var metadataObjects = []struct{ name, kind, sql string }{
	{"task_metadata", "table", `CREATE TABLE task_metadata (
id TEXT PRIMARY KEY REFERENCES tasks(id),revision INTEGER,updated_at TEXT,
body_id TEXT,body_revision INTEGER,body_updated_at TEXT,title TEXT,
workspace_id TEXT,workspace_root TEXT,workspace_dir TEXT,status TEXT,
draft_type TEXT,draft_bytes INTEGER,oversized INTEGER,unique_metadata INTEGER,valid_types INTEGER)`},
	{"task_metadata_recent", "index", `CREATE INDEX task_metadata_recent ON task_metadata(updated_at DESC,id DESC)`},
	{"task_metadata_source", "view", `CREATE VIEW task_metadata_source AS
SELECT id,revision,updated_at,
CASE WHEN length(CAST(json_extract(b,'$.id') AS BLOB))<=256 THEN json_extract(b,'$.id') END AS body_id,
json_extract(b,'$.revision') AS body_revision,
CASE WHEN length(CAST(json_extract(b,'$.updated_at') AS BLOB))<=64 THEN json_extract(b,'$.updated_at') END AS body_updated_at,
CASE WHEN length(CAST(json_extract(b,'$.title') AS BLOB))<=1024 THEN json_extract(b,'$.title') END AS title,
CASE WHEN length(CAST(json_extract(b,'$.workspace.id') AS BLOB))<=256 THEN json_extract(b,'$.workspace.id') END AS workspace_id,
CASE WHEN length(CAST(json_extract(b,'$.workspace.root') AS BLOB))<=32768 THEN json_extract(b,'$.workspace.root') END AS workspace_root,
CASE WHEN length(CAST(json_extract(b,'$.workspace.directory') AS BLOB))<=32768 THEN json_extract(b,'$.workspace.directory') END AS workspace_dir,
CASE WHEN length(CAST(json_extract(b,'$.status') AS BLOB))<=32 THEN json_extract(b,'$.status') END AS status,
json_type(b,'$.draft') AS draft_type,length(CAST(json_extract(b,'$.draft') AS BLOB)) AS draft_bytes,
oversized,
CASE WHEN b IS NOT NULL THEN NOT EXISTS(SELECT 1 FROM json_tree(b)
WHERE path IN ('$','$.workspace') GROUP BY path,key HAVING count(*)>1) ELSE 0 END AS unique_metadata,
coalesce(typeof(id)='text' AND typeof(revision)='integer' AND typeof(updated_at)='text'
AND json_type(b,'$.id')='text' AND json_type(b,'$.revision')='integer'
AND json_type(b,'$.updated_at')='text' AND json_type(b,'$.title')='text'
AND json_type(b,'$.workspace')='object' AND json_type(b,'$.workspace.id')='text'
AND json_type(b,'$.workspace.root')='text' AND json_type(b,'$.workspace.directory')='text'
AND json_type(b,'$.status')='text',0) AS valid_types
FROM (SELECT id,revision,updated_at,length(CAST(body AS BLOB))>16777216 AS oversized,
CASE WHEN length(CAST(body AS BLOB))<=16777216 AND json_valid(body) THEN body END AS b FROM tasks)`},
	{"task_metadata_insert", "trigger", `CREATE TRIGGER task_metadata_insert AFTER INSERT ON tasks BEGIN
INSERT INTO task_metadata SELECT * FROM task_metadata_source WHERE id=NEW.id;
END`},
	{"task_metadata_update", "trigger", `CREATE TRIGGER task_metadata_update AFTER UPDATE ON tasks BEGIN
DELETE FROM task_metadata WHERE id=OLD.id;
INSERT INTO task_metadata SELECT * FROM task_metadata_source WHERE id=NEW.id;
END`},
	{"task_metadata_delete", "trigger", `CREATE TRIGGER task_metadata_delete AFTER DELETE ON tasks BEGIN
DELETE FROM task_metadata WHERE id=OLD.id;
END`},
}

func metadataCorrupt() error {
	return problem("state_corrupt", "Task metadata is damaged or disagrees with its source records. Preserve the database for recovery.")
}

func checkMetadataKeys(ctx context.Context, conn executor) error {
	// Ordered set differences merge covering primary-key indexes instead of doing
	// one random index lookup per row in each direction. Both directions preserve
	// equal-count missing/orphan detection. SQLite permits NULL in these TEXT
	// primary keys, so reject it explicitly rather than treating two NULLs as equal.
	var mismatch bool
	err := conn.QueryRowContext(ctx, `SELECT
EXISTS(SELECT id FROM tasks WHERE id IS NULL)
OR EXISTS(SELECT id FROM task_metadata WHERE id IS NULL)
OR EXISTS(SELECT id FROM tasks EXCEPT SELECT id FROM task_metadata ORDER BY id)
OR EXISTS(SELECT id FROM task_metadata EXCEPT SELECT id FROM tasks ORDER BY id)`).Scan(&mismatch)
	if err != nil {
		return err
	}
	if mismatch {
		return metadataCorrupt()
	}
	return nil
}

func checkMetadataSchema(ctx context.Context, conn executor) error {
	// Six small schema lookups also fence an already-open client after dropped or
	// altered maintenance triggers. No task rows are scanned during normal Open.
	rows, err := conn.QueryContext(ctx, "SELECT name,type,sql FROM sqlite_master WHERE name IN ('task_metadata','task_metadata_recent','task_metadata_source','task_metadata_insert','task_metadata_update','task_metadata_delete')")
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, kind, definition string
		if err := rows.Scan(&name, &kind, &definition); err != nil {
			return metadataCorrupt()
		}
		found := false
		for _, object := range metadataObjects {
			if name == object.name && kind == object.kind && strings.TrimSpace(definition) == object.sql {
				found = true
				break
			}
		}
		if !found {
			return metadataCorrupt()
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if seen != len(metadataObjects) {
		return metadataCorrupt()
	}
	return nil
}

type metadataValues struct {
	id, indexedTime, bodyID, bodyTime, title, workspaceID, root, directory, status, draftType sql.NullString
	revision, bodyRevision, draftBytes                                                        sql.NullInt64
	oversized, unique, validTypes                                                             bool
}

type metadataRecord struct {
	values  metadataValues
	summary TaskSummary
	matched bool
}

func readMetadata(rows *sql.Rows) (metadataRecord, error) {
	var record metadataRecord
	v := &record.values
	if err := rows.Scan(&v.id, &v.revision, &v.indexedTime, &v.bodyID, &v.bodyRevision, &v.bodyTime,
		&v.title, &v.workspaceID, &v.root, &v.directory, &v.status, &v.draftType, &v.draftBytes,
		&v.oversized, &v.unique, &v.validTypes, &record.matched); err != nil {
		return record, metadataCorrupt()
	}
	if v.oversized {
		return record, historyTooLarge()
	}
	indexed, indexErr := time.Parse(time.RFC3339Nano, v.indexedTime.String)
	bodyTime, timeErr := time.Parse(time.RFC3339Nano, v.bodyTime.String)
	workspace := core.Workspace{ID: v.workspaceID.String, Root: v.root.String, Dir: v.directory.String}
	if !v.unique || !v.validTypes || !v.id.Valid || !validID(v.id.String) || !v.bodyID.Valid || v.bodyID.String != v.id.String ||
		!v.revision.Valid || v.revision.Int64 < 1 || !v.bodyRevision.Valid || v.bodyRevision.Int64 != v.revision.Int64 ||
		!v.indexedTime.Valid || len(v.indexedTime.String) > 64 || !v.bodyTime.Valid || indexErr != nil || timeErr != nil || indexed.IsZero() || !indexed.Equal(bodyTime) ||
		!v.title.Valid || v.title.String == "" || !validText(v.title.String, 1024) || !v.workspaceID.Valid || !v.root.Valid || !v.directory.Valid || validateWorkspace(workspace) != nil || !v.status.Valid {
		return record, metadataCorrupt()
	}
	switch v.status.String {
	case "draft", "running", "complete", "failed", "interrupted", "unknown":
	default:
		return record, metadataCorrupt()
	}
	hasDraft := false
	if v.draftType.Valid {
		if v.draftType.String != "text" || !v.draftBytes.Valid || v.draftBytes.Int64 < 0 || v.draftBytes.Int64 > maxInput {
			return record, metadataCorrupt()
		}
		hasDraft = v.draftBytes.Int64 > 0
	} else if v.draftBytes.Valid {
		return record, metadataCorrupt()
	}
	record.summary = TaskSummary{ID: v.id.String, Title: v.title.String, Revision: int(v.revision.Int64), Workspace: workspace, Status: v.status.String, HasDraft: hasDraft, UpdatedAt: bodyTime}
	return record, nil
}

func metadataRows(ctx context.Context, conn executor, table string, cursor taskCursor, match string, args ...any) (*sql.Rows, error) {
	// table is one of two internal constants, never caller-controlled SQL.
	query := "SELECT " + metadataReadColumns + ",coalesce((" + match + "),0) FROM " + table
	if cursor.ID != "" {
		query += " WHERE (updated_at,id)<(?,?)"
		args = append(args, cursor.Updated, cursor.ID)
	}
	return conn.QueryContext(ctx, query+" ORDER BY updated_at DESC,id DESC", args...)
}

func verifySelectedMetadata(ctx context.Context, conn executor, record metadataRecord) error {
	rows, err := conn.QueryContext(ctx, "SELECT "+metadataReadColumns+",1 FROM task_metadata_source WHERE id=?", record.summary.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return metadataCorrupt()
	}
	actual, err := readMetadata(rows)
	if err != nil {
		return err
	}
	if actual.values != record.values || rows.Next() {
		return metadataCorrupt()
	}
	return rows.Err()
}

func materializedSummaries(ctx context.Context, conn executor, query string, cursor taskCursor, limit int) (TaskSummaryPage, error) {
	if err := checkMetadataKeys(ctx, conn); err != nil {
		return TaskSummaryPage{}, err
	}
	pattern := literalTitlePattern(query)
	rows, err := metadataRows(ctx, conn, "task_metadata", cursor, `(lower(title) LIKE ? ESCAPE '\' OR lower(id) LIKE ? ESCAPE '\')`, pattern, pattern)
	if err != nil {
		return TaskSummaryPage{}, err
	}
	defer rows.Close()
	page := TaskSummaryPage{Tasks: []TaskSummary{}}
	selected := []metadataRecord{}
	bytes := 0
	var last taskCursor
	for rows.Next() {
		record, err := readMetadata(rows)
		if err != nil {
			return TaskSummaryPage{}, err
		}
		if !record.matched {
			continue
		}
		selected = append(selected, record)
		if len(page.Tasks) == limit {
			page.NextCursor = encodeTaskCursor(last)
			break
		}
		encoded, err := json.Marshal(record.summary)
		if err != nil {
			return TaskSummaryPage{}, metadataCorrupt()
		}
		if len(encoded) > maxHistoryBytes-bytes {
			return TaskSummaryPage{}, historyTooLarge()
		}
		bytes += len(encoded)
		page.Tasks = append(page.Tasks, record.summary)
		last = taskCursor{Version: 1, Query: queryFingerprint(query), Updated: record.values.indexedTime.String, ID: record.summary.ID}
	}
	if err := rows.Err(); err != nil {
		return TaskSummaryPage{}, err
	}
	if err := rows.Close(); err != nil {
		return TaskSummaryPage{}, err
	}
	for _, record := range selected {
		if err := verifySelectedMetadata(ctx, conn, record); err != nil {
			return TaskSummaryPage{}, err
		}
	}
	return page, nil
}

func materializedRecentDraft(ctx context.Context, conn executor, directory string) (*core.Task, error) {
	if err := checkMetadataKeys(ctx, conn); err != nil {
		return nil, err
	}
	rows, err := metadataRows(ctx, conn, "task_metadata", taskCursor{}, "draft_type='text' AND draft_bytes>0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		record, err := readMetadata(rows)
		if err != nil {
			return nil, err
		}
		if !record.matched || !sameDraftDirectory(record.summary.Workspace.Dir, directory) {
			continue
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := verifySelectedMetadata(ctx, conn, record); err != nil {
			return nil, err
		}
		task, err := loadTask(ctx, conn, record.summary.ID)
		if err != nil {
			return nil, err
		}
		if err := validateSelectedDraft(task, record.summary); err != nil {
			return nil, err
		}
		return &task, nil
	}
	return nil, rows.Err()
}

func auditMetadata(ctx context.Context, conn executor) error {
	if err := checkMetadataSchema(ctx, conn); err != nil {
		return err
	}
	rows, err := metadataRows(ctx, conn, "task_metadata_source", taskCursor{}, "1")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if _, err := readMetadata(rows); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	conditions := []string{}
	for _, column := range strings.Split(metadataColumns, ",") {
		conditions = append(conditions, "m."+column+" IS s."+column)
	}
	var mismatch bool
	err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_metadata_source s LEFT JOIN task_metadata m ON m.id=s.id
WHERE m.id IS NULL OR NOT (`+strings.Join(conditions, " AND ")+`))
OR EXISTS(SELECT 1 FROM task_metadata m LEFT JOIN tasks t ON t.id=m.id WHERE t.id IS NULL)`).Scan(&mismatch)
	if err != nil {
		return err
	}
	if mismatch {
		return metadataCorrupt()
	}
	return nil
}
