package contracts

import "fmt"

type ErrorCode string

const (
	CodeAgentNotFound                 ErrorCode = "AGENT_NOT_FOUND"
	CodeAgentVersionNotFound          ErrorCode = "AGENT_VERSION_NOT_FOUND"
	CodePackageVersionConflict        ErrorCode = "PACKAGE_VERSION_CONFLICT"
	CodeModelError                    ErrorCode = "MODEL_ERROR"
	CodeModelTimeout                  ErrorCode = "MODEL_TIMEOUT"
	CodeDecisionSchemaError           ErrorCode = "DECISION_SCHEMA_ERROR"
	CodeToolNotFound                  ErrorCode = "TOOL_NOT_FOUND"
	CodeToolArgumentInvalid           ErrorCode = "TOOL_ARGUMENT_INVALID"
	CodeToolPolicyDenied              ErrorCode = "TOOL_POLICY_DENIED"
	CodeToolApprovalRequired          ErrorCode = "TOOL_APPROVAL_REQUIRED"
	CodeToolExecutionFailed           ErrorCode = "TOOL_EXECUTION_FAILED"
	CodeExecutionDomainUnavailable    ErrorCode = "EXECUTION_DOMAIN_UNAVAILABLE"
	CodeExternalBridgeError           ErrorCode = "EXTERNAL_BRIDGE_ERROR"
	CodeArtifactWriteFailed           ErrorCode = "ARTIFACT_WRITE_FAILED"
	CodeTaskConflict                  ErrorCode = "TASK_CONFLICT"
	CodeTaskCancelled                 ErrorCode = "TASK_CANCELLED"
	CodeHandoffDenied                 ErrorCode = "HANDOFF_DENIED"
	CodeHandoffContextTooLarge        ErrorCode = "HANDOFF_CONTEXT_TOO_LARGE"
	CodePolicyVersionConflict         ErrorCode = "POLICY_VERSION_CONFLICT"
	CodeAdmissionRejected             ErrorCode = "ADMISSION_REJECTED"
	CodeAgentRuntimeDriverUnavailable ErrorCode = "AGENT_RUNTIME_DRIVER_UNAVAILABLE"
)

type RuntimeError struct {
	Code       ErrorCode      `json:"code"`
	Message    string         `json:"message"`
	Retryable  bool           `json:"retryable"`
	Repairable bool           `json:"repairable"`
	Details    map[string]any `json:"details,omitempty"`
}

type APIErrorResponse struct {
	Error RuntimeError `json:"error"`
}

func NewRuntimeError(code ErrorCode, message string, details map[string]any) *RuntimeError {
	retryable, repairable := ErrorTraits(code)
	return &RuntimeError{
		Code:       code,
		Message:    message,
		Retryable:  retryable,
		Repairable: repairable,
		Details:    details,
	}
}

func (e *RuntimeError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *RuntimeError) IsRetryable() bool {
	return e != nil && e.Retryable
}

func (e *RuntimeError) IsRepairable() bool {
	return e != nil && e.Repairable
}

func (e *RuntimeError) ToAPIResponse() APIErrorResponse {
	if e == nil {
		return APIErrorResponse{}
	}
	return APIErrorResponse{Error: *e}
}

func (e *RuntimeError) ToTracePayload() map[string]any {
	if e == nil {
		return nil
	}
	payload := map[string]any{
		"code":       e.Code,
		"message":    e.Message,
		"retryable":  e.Retryable,
		"repairable": e.Repairable,
	}
	if len(e.Details) > 0 {
		payload["details"] = e.Details
	}
	return payload
}

func ErrorTraits(code ErrorCode) (retryable bool, repairable bool) {
	switch code {
	case CodeModelError, CodeModelTimeout, CodeDecisionSchemaError, CodeToolArgumentInvalid, CodeToolExecutionFailed, CodeExecutionDomainUnavailable, CodeExternalBridgeError, CodeArtifactWriteFailed, CodeTaskConflict, CodeHandoffContextTooLarge, CodeAdmissionRejected:
		retryable = true
	}
	switch code {
	case CodeDecisionSchemaError, CodeToolArgumentInvalid, CodeHandoffContextTooLarge:
		repairable = true
	}
	return retryable, repairable
}
