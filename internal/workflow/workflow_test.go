package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ucli.local/ucli/internal/core"
)

const example = `{"version":1,"id":"feature-plan","steps":[
	{"id":"implement","prompt":"Implement the requested small change.","selection":{"model":"auto","account":"work","effort":"high","permission":"workspace-write"}},
	{"id":"check","prompt":"Run the required checks.","depends_on":["implement"]},
	{"id":"review","prompt":"Review the change and the recorded check evidence.","depends_on":["implement","check"]}
]}`

func exampleSpec(t *testing.T) Spec {
	t.Helper()
	spec, err := Parse(strings.NewReader(example))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
func pending(spec Spec) map[string]State {
	m := map[string]State{}
	for _, step := range spec.Steps {
		m[step.ID] = Pending
	}
	return m
}
func checkCode(t *testing.T, err error, want string) {
	t.Helper()
	var problem *core.Error
	if !errors.As(err, &problem) || problem.Code != want || problem.Exit != 2 || problem.Acceptance != "not-sent" || problem.SideEffects != "none" {
		t.Fatalf("error=%+v want %s (exit2/no dispatch)", err, want)
	}
}
func planOK(t *testing.T, spec Spec, snapshot map[string]State) Plan {
	t.Helper()
	plan, err := PlanReady(spec, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestParseUniversalSelectionAndNoExecutableSurface(t *testing.T) {
	spec := exampleSpec(t)
	if spec.ID != "feature-plan" || len(spec.Steps) != 3 {
		t.Fatalf("spec %+v", spec)
	}
	if got := spec.Steps[0].Selection; got.Model != "auto" || got.Account != "work" || got.Effort != "high" || got.Permission != "workspace-write" {
		t.Fatalf("selection lost: %+v", got)
	}
	if spec.Steps[1].Selection != core.Defaults() || spec.Steps[0].DependsOn == nil {
		t.Fatal("missing fields did not receive universal defaults")
	}
	// Commands and environment-looking text remain literal prompt data.
	spec.Steps[0].Prompt = "$(do-not-run) ${TOKEN} {{include:private}}; 日本語"
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(bytes.NewReader(raw))
	if err != nil || !reflect.DeepEqual(got, spec) {
		t.Fatalf("literal roundtrip: %+v %v", got, err)
	}
}

func TestStrictJSONRejectsUnknownDuplicateNullAndWrongTypes(t *testing.T) {
	for _, tc := range []struct{ name, raw, code string }{
		{"unknown root", `{"version":1,"id":"x","steps":[],"execute":true}`, "workflow_unknown_field"},
		{"wrong case", `{"Version":1,"id":"x","steps":[]}`, "workflow_unknown_field"},
		{"duplicate root", `{"version":1,"version":1,"id":"x","steps":[]}`, "workflow_duplicate_field"},
		{"escaped duplicate", `{"version":1,"\u0076ersion":1,"id":"x","steps":[]}`, "workflow_duplicate_field"},
		{"duplicate step", `{"version":1,"id":"x","steps":[{"id":"a","id":"b","prompt":"x"}]}`, "workflow_duplicate_field"},
		{"unknown step", `{"version":1,"id":"x","steps":[{"id":"a","prompt":"x","retry":2}]}`, "workflow_unknown_field"},
		{"unknown selection", `{"version":1,"id":"x","steps":[{"id":"a","prompt":"x","selection":{"tools":"all"}}]}`, "workflow_unknown_field"},
		{"wrong selection case", `{"version":1,"id":"x","steps":[{"id":"a","prompt":"x","selection":{"Model":"x"}}]}`, "workflow_unknown_field"},
		{"duplicate selection", `{"version":1,"id":"x","steps":[{"id":"a","prompt":"x","selection":{"effort":"low","effort":"high"}}]}`, "workflow_duplicate_field"},
		{"null steps", `{"version":1,"id":"x","steps":null}`, "workflow_invalid_json"},
		{"null optional", `{"version":1,"id":"x","steps":[{"id":"a","prompt":"x","selection":null}]}`, "workflow_invalid_json"},
		{"null dependency", `{"version":1,"id":"x","steps":[{"id":"a","prompt":"x","depends_on":[null]}]}`, "workflow_invalid_json"},
		{"boolean prompt", `{"version":1,"id":"x","steps":[{"id":"a","prompt":true}]}`, "workflow_invalid_json"},
		{"fractional version", `{"version":1.0,"id":"x","steps":[]}`, "workflow_invalid_json"},
		{"string version", `{"version":"1","id":"x","steps":[]}`, "workflow_invalid_json"},
		{"wrong root", `[]`, "workflow_invalid_json"},
		{"trailing document", example + `{}`, "workflow_invalid_json"},
		{"trailing junk", example + ` garbage`, "workflow_invalid_json"},
		{"incomplete", `{"version":`, "workflow_invalid_json"},
		{"comments", `/* comment */` + example, "workflow_invalid_json"},
		{"invalid UTF8", string([]byte{0xff}), "workflow_invalid_json"},
		{"unpaired high surrogate", `{"version":1,"id":"x","steps":[{"id":"a","prompt":"\uD800"}]}`, "workflow_invalid_json"},
		{"unpaired low surrogate", `{"version":1,"id":"x","steps":[{"id":"a","prompt":"\uDC00"}]}`, "workflow_invalid_json"},
		{"wrong surrogate pair", `{"version":1,"id":"x","steps":[{"id":"a","prompt":"\uD800\u0041"}]}`, "workflow_invalid_json"},
		{"deep nesting", strings.Repeat("[", 10) + strings.Repeat("]", 10), "workflow_invalid_json"},
	} {
		t.Run(tc.name, func(t *testing.T) { _, err := Parse(strings.NewReader(tc.raw)); checkCode(t, err, tc.code) })
	}
}

func TestUnicodeEscapesPreservePromptText(t *testing.T) {
	for _, tc := range []struct{ encoded, want string }{
		{`\ud83d\ude00`, "\U0001f600"},
		{`\\uD800`, `\uD800`},
		{`\ufffd`, "\ufffd"},
	} {
		raw := `{"version":1,"id":"x","steps":[{"id":"a","prompt":"` + tc.encoded + `"}]}`
		spec, err := Parse(strings.NewReader(raw))
		if err != nil || spec.Steps[0].Prompt != tc.want {
			t.Fatalf("prompt escape %q: %+v %v", tc.encoded, spec, err)
		}
	}
}

func TestValidationRejectsMalformedGraphsBeforePlanning(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*Spec)
	}{
		{"version", "workflow_version", func(s *Spec) { s.Version = 2 }},
		{"missing version", "workflow_version", func(s *Spec) { s.Version = 0 }},
		{"no steps", "workflow_step_limit", func(s *Spec) { s.Steps = nil }},
		{"duplicate IDs", "workflow_duplicate_step", func(s *Spec) { s.Steps[1].ID = s.Steps[0].ID }},
		{"self dependency", "workflow_self_dependency", func(s *Spec) { s.Steps[0].DependsOn = []string{"implement"} }},
		{"duplicate edge", "workflow_duplicate_dependency", func(s *Spec) { s.Steps[1].DependsOn = []string{"implement", "implement"} }},
		{"missing dependency", "workflow_missing_dependency", func(s *Spec) { s.Steps[0].DependsOn = []string{"absent"} }},
		{"cycle", "workflow_cycle", func(s *Spec) { s.Steps[0].DependsOn = []string{"review"} }},
		{"empty prompt", "workflow_invalid_prompt", func(s *Spec) { s.Steps[0].Prompt = " \n " }},
		{"NUL prompt", "workflow_invalid_prompt", func(s *Spec) { s.Steps[0].Prompt = "bad\x00text" }},
		{"invalid UTF8 prompt", "workflow_invalid_prompt", func(s *Spec) { s.Steps[0].Prompt = string([]byte{255}) }},
		{"unknown effort", "workflow_invalid_selection", func(s *Spec) { s.Steps[0].Selection.Effort = "thinking" }},
		{"unknown permission", "workflow_invalid_selection", func(s *Spec) { s.Steps[0].Selection.Permission = "sudo" }},
		{"model controls", "workflow_invalid_selection", func(s *Spec) { s.Steps[0].Selection.Model = "model\nsecret" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := exampleSpec(t)
			tc.change(&s)
			checkCode(t, Validate(s), tc.code)
			_, err := PlanReady(s, pending(s))
			checkCode(t, err, tc.code)
		})
	}
}

func TestASCIIIdentifiersHaveNoNormalizationOrPathMeaning(t *testing.T) {
	for _, id := range []string{"", ".", "../outside", "x/y", "x\\y", "a b", "-a", "é", "e\u0301", "a\n", strings.Repeat("x", 65)} {
		s := exampleSpec(t)
		s.ID = id
		checkCode(t, Validate(s), "workflow_invalid_id")
		s = exampleSpec(t)
		s.Steps[0].ID = id
		checkCode(t, Validate(s), "workflow_invalid_id")
	}
	s := Spec{Version: 1, ID: "Plan.v1_2-3", Steps: []Step{{ID: "A", Prompt: "one"}, {ID: "a", Prompt: "two"}, {ID: strings.Repeat("x", 64), Prompt: "three"}}}
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
	plan := planOK(t, s, pending(s))
	if !reflect.DeepEqual(plan.Ready, []string{"A", "a", strings.Repeat("x", 64)}) {
		t.Fatalf("ASCII case/order changed: %+v", plan)
	}
}

func TestStepEdgeAndAggregatePromptBounds(t *testing.T) {
	s := Spec{Version: 1, ID: "limits"}
	for i := 0; i < MaxSteps; i++ {
		s.Steps = append(s.Steps, Step{ID: fmt.Sprint("s", i), Prompt: "x"})
	}
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
	s.Steps = append(s.Steps, Step{ID: "overflow", Prompt: "x"})
	checkCode(t, Validate(s), "workflow_step_limit")
	raw, _ := json.Marshal(s)
	_, err := Parse(bytes.NewReader(raw))
	checkCode(t, err, "workflow_step_limit")
	s = Spec{Version: 1, ID: "edges"}
	for i := 0; i < 65; i++ {
		step := Step{ID: fmt.Sprint("s", i), Prompt: "x"}
		for j := 0; j < i; j++ {
			step.DependsOn = append(step.DependsOn, fmt.Sprint("s", j))
		}
		s.Steps = append(s.Steps, step)
	}
	// First 64 nodes use 2016 edges; the last 32 reach the exact 2048 limit.
	s.Steps[64].DependsOn = s.Steps[64].DependsOn[:32]
	if err = Validate(s); err != nil {
		t.Fatal(err)
	}
	s.Steps[64].DependsOn = append(s.Steps[64].DependsOn, "s32")
	checkCode(t, Validate(s), "workflow_edge_limit")
	raw, _ = json.Marshal(s)
	_, err = Parse(bytes.NewReader(raw))
	checkCode(t, err, "workflow_edge_limit")
	s = Spec{Version: 1, ID: "prompts", Steps: []Step{{ID: "a", Prompt: strings.Repeat("x", MaxPromptBytes-1)}, {ID: "b", Prompt: "x"}}}
	if err = Validate(s); err != nil {
		t.Fatal(err)
	}
	s.Steps[1].Prompt = "xx"
	checkCode(t, Validate(s), "workflow_prompt_limit")
	raw, _ = json.Marshal(s)
	_, err = Parse(bytes.NewReader(raw))
	checkCode(t, err, "workflow_prompt_limit")
}

type countedReader struct {
	reader io.Reader
	read   int
}

func (r *countedReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("private reader diagnostic") }

func TestDocumentReadIsBoundedAndFailuresAreSanitized(t *testing.T) {
	raw := example + strings.Repeat(" ", MaxDocumentBytes-len(example))
	if _, err := Parse(strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	reader := &countedReader{reader: strings.NewReader(raw + strings.Repeat("x", 1000))}
	_, err := Parse(reader)
	checkCode(t, err, "workflow_too_large")
	if reader.read != MaxDocumentBytes+1 {
		t.Fatalf("read %d bytes", reader.read)
	}
	_, err = Parse(brokenReader{})
	checkCode(t, err, "workflow_read_failed")
	if strings.Contains(err.Error(), "private") {
		t.Fatal("reader diagnostics leaked")
	}
	_, err = Parse(nil)
	checkCode(t, err, "workflow_invalid_json")
}

func TestImplementCheckReviewReadinessAndCompletedEvidence(t *testing.T) {
	spec := exampleSpec(t)
	states := pending(spec)
	p := planOK(t, spec, states)
	if !reflect.DeepEqual(p.Ready, []string{"implement"}) || len(p.Blocked) != 2 {
		t.Fatalf("initial %+v", p)
	}
	states["implement"] = Running
	p = planOK(t, spec, states)
	if len(p.Ready) != 0 || !reflect.DeepEqual(p.Running, []string{"implement"}) {
		t.Fatalf("running %+v", p)
	}
	states["implement"] = Complete
	p = planOK(t, spec, states)
	if !reflect.DeepEqual(p.Ready, []string{"check"}) || !reflect.DeepEqual(p.Complete, []string{"implement"}) {
		t.Fatalf("checking %+v", p)
	}
	states["check"] = Failed
	p = planOK(t, spec, states)
	if len(p.Ready) != 0 || !reflect.DeepEqual(p.Complete, []string{"implement"}) || p.Blocked[0].Reasons[0].Code != "step_failed" || p.Blocked[1].Reasons[0].Code != "dependency_failed" {
		t.Fatalf("failure %+v", p)
	}
	states["check"] = Complete
	p = planOK(t, spec, states)
	if !reflect.DeepEqual(p.Ready, []string{"review"}) {
		t.Fatalf("review %+v", p)
	}
	states["review"] = Complete
	p = planOK(t, spec, states)
	if len(p.Ready) != 0 || len(p.Blocked) != 0 || len(p.Complete) != 3 {
		t.Fatalf("terminal %+v", p)
	}
}

func TestNoImplicitRetryOrUnknownOutcomeReplay(t *testing.T) {
	spec := exampleSpec(t)
	spec.Steps = append(spec.Steps, Step{ID: "independent", Prompt: "Independent task"})
	for _, state := range []State{Failed, Interrupted, Unknown} {
		states := pending(spec)
		states["implement"] = state
		p := planOK(t, spec, states)
		if !reflect.DeepEqual(p.Ready, []string{"independent"}) || len(p.Blocked) != 3 {
			t.Fatalf("state %s: %+v", state, p)
		}
		for _, blocked := range p.Blocked {
			if blocked.ID == "implement" && blocked.Reasons[0].Code != "step_"+string(state) {
				t.Fatalf("missing recovery reason: %+v", blocked)
			}
		}
	}
	// Caller evidence remains authoritative even after another step fails. The
	// planner never turns already completed work back into pending work.
	states := pending(spec)
	states["implement"] = Failed
	states["check"] = Complete
	p := planOK(t, spec, states)
	if !reflect.DeepEqual(p.Complete, []string{"check"}) {
		t.Fatalf("completion changed: %+v", p)
	}
}

func TestSnapshotsRequireEveryExplicitKnownState(t *testing.T) {
	spec := exampleSpec(t)
	for _, states := range []map[string]State{
		nil, {}, {"implement": Pending, "check": Pending},
		{"implement": Pending, "check": Pending, "review": Pending, "other": Complete},
		{"implement": Pending, "check": Pending, "other": Complete},
		{"implement": "", "check": Pending, "review": Pending},
		{"implement": "queued", "check": Pending, "review": Pending},
	} {
		_, err := PlanReady(spec, states)
		checkCode(t, err, "workflow_invalid_snapshot")
	}
}

func TestPlanningIsDeterministicImmutableAndSerializable(t *testing.T) {
	spec := exampleSpec(t)
	states := pending(spec)
	states["implement"] = Unknown
	want := planOK(t, spec, states)
	before, _ := json.Marshal(spec)
	stateBefore, _ := json.Marshal(states)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := PlanReady(spec, states)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("concurrent planner mismatch: %v", err)
			}
		}()
	}
	wg.Wait()
	after, _ := json.Marshal(spec)
	stateAfter, _ := json.Marshal(states)
	if !bytes.Equal(before, after) || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("planner mutated its inputs")
	}
	spec.Steps[0], spec.Steps[2] = spec.Steps[2], spec.Steps[0]
	spec.Steps[0].DependsOn[0], spec.Steps[0].DependsOn[1] = spec.Steps[0].DependsOn[1], spec.Steps[0].DependsOn[0]
	if got := planOK(t, spec, states); !reflect.DeepEqual(got, want) {
		t.Fatalf("order changed result: %+v", got)
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var resumed Plan
	if err = json.Unmarshal(raw, &resumed); err != nil || !reflect.DeepEqual(resumed, want) {
		t.Fatalf("JSON plan roundtrip: %v", err)
	}
	// A snapshot roundtrip reproduces readiness, not accepted/executed state.
	var restored map[string]State
	if err = json.Unmarshal(stateBefore, &restored); err != nil {
		t.Fatal(err)
	}
	if got := planOK(t, spec, restored); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot roundtrip changed plan: %+v", got)
	}
}

func TestWriterReadinessDoesNotClaimLeaseOrPermission(t *testing.T) {
	spec := Spec{Version: 1, ID: "writers", Steps: []Step{
		{ID: "a", Prompt: "write A", Selection: core.Selection{Permission: "workspace-write"}},
		{ID: "b", Prompt: "write B", Selection: core.Selection{Permission: "unrestricted"}},
	}}
	p := planOK(t, spec, pending(spec))
	if !reflect.DeepEqual(p.Ready, []string{"a", "b"}) {
		t.Fatalf("dependency readiness should remain separate from execution lease policy: %+v", p)
	}
}
