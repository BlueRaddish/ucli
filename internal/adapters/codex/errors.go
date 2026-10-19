package codex

import (
	"encoding/json"
	"errors"
	"strconv"

	"ucli.local/ucli/internal/core"
)

// Native messages, details and open-ended error labels are never diagnostic text.
// Retain only reviewed enum tags; future string/object variants become "other".
func turnErrorKind(raw json.RawMessage) (string, error) {
	f := objectFields(raw)
	if _, ok := textField(f, "message", maxFrame); !ok {
		return "", errors.New("Codex error payload lacks a valid message field")
	}
	if !absentNullable(f["additionalDetails"]) {
		if _, ok := textField(f, "additionalDetails", maxFrame); !ok {
			return "", errors.New("Codex error details have an invalid type")
		}
	}
	info := f["codexErrorInfo"]
	if absentNullable(info) {
		return "other", nil
	}
	var tag string
	if json.Unmarshal(info, &tag) == nil {
		switch tag {
		case "contextWindowExceeded", "sessionBudgetExceeded", "usageLimitExceeded", "rateLimitExceeded", "flexUnavailable", "serverOverloaded", "cyberPolicy", "misalignmentPolicyViolation", "tooManyDenials", "internalServerError", "unauthorized", "badRequest", "threadRollbackFailed", "sandboxError", "other":
			return tag, nil
		default:
			return "other", nil
		}
	}
	obj := objectFields(info)
	if obj == nil {
		return "", errors.New("Codex error classification has an invalid type")
	}
	for _, name := range []string{"httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts", "activeTurnNotSteerable"} {
		value, present := obj[name]
		if !present {
			continue
		}
		fields := objectFields(value)
		if len(obj) != 1 || fields == nil {
			return "", errors.New("Codex error classification has an invalid shape")
		}
		if name == "activeTurnNotSteerable" {
			if !equals(fields, "turnKind", "review") && !equals(fields, "turnKind", "compact") {
				return "", errors.New("Codex error classification has an invalid turn kind")
			}
		} else if !absentNullable(fields["httpStatusCode"]) {
			var status uint16
			if json.Unmarshal(fields["httpStatusCode"], &status) != nil {
				return "", errors.New("Codex error classification has an invalid HTTP status type")
			}
		}
		return name, nil
	}
	return "other", nil
}

func confirmedTurnFailure(kind string) *core.Error {
	class, exit := "provider", 8
	switch kind {
	case "unauthorized":
		class, exit = "auth", 3
	case "sessionBudgetExceeded", "usageLimitExceeded", "rateLimitExceeded":
		class, exit = "quota", 4
	case "contextWindowExceeded", "badRequest", "cyberPolicy", "misalignmentPolicyViolation", "tooManyDenials", "activeTurnNotSteerable":
		class, exit = "request", 5
	}
	problem := core.Problem("codex_"+class+"_failed", "Codex confirmed a failed turn ("+kind+"; "+class+").", exit)
	problem.Origin, problem.Acceptance, problem.SideEffects = "codex", "accepted", "unknown"
	return problem
}

func (s *turnStream) errorNotification(f map[string]json.RawMessage) error {
	if !equals(f, "threadId", s.thread) || !equals(f, "turnId", s.turn) {
		return errors.New("Codex error notification execution identity is missing or mismatched")
	}
	var retry bool
	if isNull(f["willRetry"]) || json.Unmarshal(f["willRetry"], &retry) != nil {
		return errors.New("Codex error notification lacks a boolean willRetry")
	}
	kind, err := turnErrorKind(f["error"])
	if err != nil {
		return err
	}
	s.errorKind, s.willRetry = kind, retry
	return nil
}

func (s *turnStream) unsettledError() error {
	if s.errorKind == "" {
		return nil
	}
	return errors.New("Codex error notification (" + s.errorKind + "; willRetry=" + strconv.FormatBool(s.willRetry) + ") was observed without a confirmed terminal outcome.")
}
