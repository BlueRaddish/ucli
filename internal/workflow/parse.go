package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"unicode/utf8"

	"ucli.local/ucli/internal/core"
)

// Parse reads at most MaxDocumentBytes+1 bytes. It never opens a file or follows
// imports, environment references or commands. File/stdin ownership is the caller's.
func Parse(reader io.Reader) (Spec, error) {
	if reader == nil {
		return Spec{}, problem("workflow_invalid_json", "A workflow JSON reader is required.")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxDocumentBytes+1))
	if err != nil {
		return Spec{}, problem("workflow_read_failed", "The workflow document could not be read completely.")
	}
	if len(raw) > MaxDocumentBytes {
		return Spec{}, problem("workflow_too_large", "Workflow JSON exceeds 2 MiB.")
	}
	if !utf8.Valid(raw) || !validUnicodeEscapes(raw) {
		return Spec{}, problem("workflow_invalid_json", "Workflow JSON must use valid UTF-8 and paired Unicode surrogate escapes.")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = checkValue(decoder, "$", 0, &jsonBudget{}); err != nil {
		return Spec{}, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return Spec{}, problem("workflow_invalid_json", "Supply exactly one workflow JSON object with no trailing document.")
	}
	var spec Spec
	if err = json.Unmarshal(raw, &spec); err != nil {
		return Spec{}, problem("workflow_invalid_json", "Workflow fields must match the versioned JSON object, step array and string field types.")
	}
	if err = Validate(spec); err != nil {
		return Spec{}, err
	}
	for i := range spec.Steps {
		spec.Steps[i].Selection = spec.Steps[i].Selection.WithDefaults()
		if spec.Steps[i].DependsOn == nil {
			spec.Steps[i].DependsOn = []string{}
		}
	}
	return spec, nil
}

// encoding/json replaces unpaired surrogate escapes with U+FFFD. Reject them
// rather than silently changing prompt text; ordinary JSON syntax is checked below.
func validUnicodeEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' || i+1 >= len(raw) {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		r, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil || r >= 0xdc00 && r <= 0xdfff {
			return false
		}
		i += 4
		if r >= 0xd800 && r <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

// A small schema-scoped token pass rejects duplicate and case-mismatched keys.
// encoding/json's struct decoder alone accepts both. The document byte limit and
// depth cap also bound this pass before graph validation allocates any index.
type jsonBudget struct{ steps, edges, promptBytes int }

func checkValue(decoder *json.Decoder, path string, depth int, budget *jsonBudget) error {
	if depth > 8 {
		return problem("workflow_invalid_json", "Workflow JSON nesting exceeds the supported schema.")
	}
	token, err := decoder.Token()
	if err != nil {
		return problem("workflow_invalid_json", "Workflow JSON is malformed or incomplete.")
	}
	if token == nil {
		return problem("workflow_invalid_json", "Null workflow values are unsupported; omit optional fields instead.")
	}
	delim, composite := token.(json.Delim)
	if !composite {
		if path == "$.steps[].prompt" {
			if prompt, ok := token.(string); ok {
				if len(prompt) > MaxPromptBytes-budget.promptBytes {
					return problem("workflow_prompt_limit", "Combined workflow prompts exceed 1 MiB.")
				}
				budget.promptBytes += len(prompt)
			}
		}
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			token, err = decoder.Token()
			if err != nil {
				return problem("workflow_invalid_json", "Workflow JSON has an invalid object member.")
			}
			key, ok := token.(string)
			if !ok {
				return problem("workflow_invalid_json", "Workflow JSON object keys must be strings.")
			}
			if !allowedKey(path, key) {
				return problem("workflow_unknown_field", "An unknown or incorrectly cased field appears at "+path+"; only the v1 workflow fields are supported.")
			}
			if seen[key] {
				return problem("workflow_duplicate_field", "A workflow JSON object contains the same field more than once at "+path+".")
			}
			seen[key] = true
			if err = checkValue(decoder, path+"."+key, depth+1, budget); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if path == "$.steps" {
				budget.steps++
				if budget.steps > MaxSteps {
					return problem("workflow_step_limit", "A workflow requires 1-256 steps.")
				}
			}
			if path == "$.steps[].depends_on" {
				budget.edges++
				if budget.edges > MaxEdges {
					return problem("workflow_edge_limit", "Workflow dependencies exceed 2048 edges.")
				}
			}
			if err = checkValue(decoder, path+"[]", depth+1, budget); err != nil {
				return err
			}
		}
	default:
		return problem("workflow_invalid_json", "Workflow JSON contains an unexpected delimiter.")
	}
	if _, err = decoder.Token(); err != nil {
		return problem("workflow_invalid_json", "Workflow JSON is incomplete.")
	}
	return nil
}

func allowedKey(path, key string) bool {
	switch path {
	case "$":
		return key == "version" || key == "id" || key == "steps"
	case "$.steps[]":
		return key == "id" || key == "prompt" || key == "selection" || key == "depends_on"
	case "$.steps[].selection":
		return key == "model" || key == "account" || key == "effort" || key == "permission"
	}
	return false
}

// Validate accepts direct typed callers as well as parsed input and never mutates
// the supplied graph. Universal selections express intent, not granted authority.
func Validate(spec Spec) error {
	if spec.Version != SchemaVersion {
		return problem("workflow_version", "Only workflow schema version 1 is supported.")
	}
	if !validID(spec.ID) {
		return problem("workflow_invalid_id", "Workflow ID must be 1-64 ASCII letters/digits with optional dot, underscore or hyphen after its first character.")
	}
	if len(spec.Steps) < 1 || len(spec.Steps) > MaxSteps {
		return problem("workflow_step_limit", "A workflow requires 1-256 steps.")
	}
	steps := make(map[string]Step, len(spec.Steps))
	promptBytes, edges := 0, 0
	for _, step := range spec.Steps {
		if !validID(step.ID) {
			return problem("workflow_invalid_id", "Step IDs must be 1-64 ASCII letters/digits with optional dot, underscore or hyphen after the first character.")
		}
		if _, exists := steps[step.ID]; exists {
			return problem("workflow_duplicate_step", fmt.Sprintf("Step %q appears more than once.", step.ID))
		}
		if err := core.ValidateInput(step.Prompt); err != nil {
			return problem("workflow_invalid_prompt", fmt.Sprintf("Step %q needs nonempty valid UTF-8 prompt text without NUL, within 1 MiB.", step.ID))
		}
		if len(step.Prompt) > MaxPromptBytes-promptBytes {
			return problem("workflow_prompt_limit", "Combined workflow prompts exceed 1 MiB.")
		}
		promptBytes += len(step.Prompt)
		if err := core.ValidateSelection(step.Selection); err != nil {
			return problem("workflow_invalid_selection", fmt.Sprintf("Step %q: %s", step.ID, err.Error()))
		}
		if len(step.DependsOn) > MaxEdges-edges {
			return problem("workflow_edge_limit", "Workflow dependencies exceed 2048 edges.")
		}
		edges += len(step.DependsOn)
		dependencies := map[string]bool{}
		for _, id := range step.DependsOn {
			if !validID(id) {
				return problem("workflow_invalid_id", fmt.Sprintf("Step %q has an invalid dependency ID.", step.ID))
			}
			if id == step.ID {
				return problem("workflow_self_dependency", fmt.Sprintf("Step %q cannot depend on itself.", step.ID))
			}
			if dependencies[id] {
				return problem("workflow_duplicate_dependency", fmt.Sprintf("Step %q names dependency %q more than once.", step.ID, id))
			}
			dependencies[id] = true
		}
		steps[step.ID] = step
	}
	ids := sortedIDs(steps)
	for _, id := range ids {
		for _, dependency := range sortedDependencies(steps[id]) {
			if _, exists := steps[dependency]; !exists {
				return problem("workflow_missing_dependency", fmt.Sprintf("Step %q depends on missing step %q.", id, dependency))
			}
		}
	}
	// DFS depth is bounded by MaxSteps (256); there is no recursive execution.
	color := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if color[id] == 1 {
			return problem("workflow_cycle", fmt.Sprintf("Workflow dependencies contain a cycle through step %q.", id))
		}
		if color[id] == 2 {
			return nil
		}
		color[id] = 1
		for _, dependency := range sortedDependencies(steps[id]) {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		color[id] = 2
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func validID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}

func sortedIDs(steps map[string]Step) []string {
	ids := make([]string, 0, len(steps))
	for id := range steps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedDependencies(step Step) []string {
	ids := append([]string(nil), step.DependsOn...)
	sort.Strings(ids)
	return ids
}
