package app

import (
	"context"
	"reflect"
	"testing"

	"ucli.local/ucli/internal/core"
)

func TestAttemptEventsReadAcrossAppInstancesWithoutChangingWork(t *testing.T) {
	a, cwd := fixtureApp(t)
	ctx := context.Background()
	attempt, err := a.Run(ctx, Options{Input: "Monitor fixture", CWD: cwd, Demo: true, Selection: core.Defaults()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, history, err := a.Task(ctx, attempt.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := Open(a.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	first, err := reader.AttemptEvents(ctx, attempt.ID, 0, 1)
	if err != nil || len(first) != 1 || first[0].Type != "accepted" {
		t.Fatalf("first event=%+v err=%v", first, err)
	}
	rest, err := reader.AttemptEvents(ctx, attempt.ID, first[0].Sequence, 100)
	if err != nil || len(rest) == 0 || rest[len(rest)-1].Type != "result_persisted" {
		t.Fatalf("remaining events=%+v err=%v", rest, err)
	}
	for _, bad := range []struct {
		after int64
		limit int
	}{{-1, 1}, {0, 0}, {0, 1001}} {
		if _, err := reader.AttemptEvents(ctx, attempt.ID, bad.after, bad.limit); err == nil {
			t.Fatal("invalid event page accepted")
		}
	}
	after, latest, err := a.Task(ctx, attempt.TaskID)
	if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(history, latest) {
		t.Fatal("monitor read changed durable task or attempts", err)
	}
}
