// Ported from packages/chord/src/services/errors.ts (pi v1.0.0).

package chord

// RemoteServiceErrorCode is a stable remote-service failure code.
type RemoteServiceErrorCode string

const (
	RemoteServiceErrorCodeServiceNotAllowed       RemoteServiceErrorCode = "service_not_allowed"
	RemoteServiceErrorCodeServiceNotFound         RemoteServiceErrorCode = "service_not_found"
	RemoteServiceErrorCodeServiceModeMismatch     RemoteServiceErrorCode = "service_mode_mismatch"
	RemoteServiceErrorCodeServiceMemberNotFound   RemoteServiceErrorCode = "service_member_not_found"
	RemoteServiceErrorCodeServiceMemberMismatch   RemoteServiceErrorCode = "service_member_mismatch"
	RemoteServiceErrorCodeServiceInstanceNotFound RemoteServiceErrorCode = "service_instance_not_found"
	RemoteServiceErrorCodeServiceStaleInstance    RemoteServiceErrorCode = "service_stale_instance"
	RemoteServiceErrorCodeServiceInvalidValue     RemoteServiceErrorCode = "service_invalid_value"
)

// RemoteServiceErrorCodes is every RemoteServiceErrorCode, in declaration order.
var RemoteServiceErrorCodes = []RemoteServiceErrorCode{
	RemoteServiceErrorCodeServiceNotAllowed,
	RemoteServiceErrorCodeServiceNotFound,
	RemoteServiceErrorCodeServiceModeMismatch,
	RemoteServiceErrorCodeServiceMemberNotFound,
	RemoteServiceErrorCodeServiceMemberMismatch,
	RemoteServiceErrorCodeServiceInstanceNotFound,
	RemoteServiceErrorCodeServiceStaleInstance,
	RemoteServiceErrorCodeServiceInvalidValue,
}

// IsRemoteServiceErrorCode reports whether value is a RemoteServiceErrorCode.
func IsRemoteServiceErrorCode(value any) bool {
	panic("unported: IsRemoteServiceErrorCode")
}

// RemoteServiceError is a remote service failure with a stable code.
// Name is "RemoteServiceError".
type RemoteServiceError struct {
	Code    RemoteServiceErrorCode
	Message string
}

// NewRemoteServiceError returns an error with the given code and message.
func NewRemoteServiceError(code RemoteServiceErrorCode, message string) *RemoteServiceError {
	panic("unported: NewRemoteServiceError")
}

func (e *RemoteServiceError) Error() string { return e.Message }

func (e *RemoteServiceError) Name() string { return "RemoteServiceError" }

var _ error = (*RemoteServiceError)(nil)
