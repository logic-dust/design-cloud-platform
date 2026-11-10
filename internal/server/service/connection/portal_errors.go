package connection

import (
	"errors"
	"net/http"
)

// PublicHTTP maps connection-domain errors to stable portal HTTP status and public message.
// Public message is always the sentinel Error() string (no wrapped detail).
func PublicHTTP(err error) (status int, message string, ok bool) {
	if err == nil {
		return 0, "", false
	}
	switch {
	case errors.Is(err, ErrCandidateSetEmpty):
		return http.StatusUnprocessableEntity, ErrCandidateSetEmpty.Error(), true
	case errors.Is(err, ErrNoHealthyNode):
		return http.StatusServiceUnavailable, ErrNoHealthyNode.Error(), true
	case errors.Is(err, ErrAllNodesOccupied):
		return http.StatusConflict, ErrAllNodesOccupied.Error(), true
	case errors.Is(err, ErrNodeConnectionLimit):
		return http.StatusConflict, ErrNodeConnectionLimit.Error(), true
	case errors.Is(err, ErrTargetNodeUnavailable):
		return http.StatusServiceUnavailable, ErrTargetNodeUnavailable.Error(), true
	case errors.Is(err, ErrPlacerNoNodeAvailable):
		return http.StatusServiceUnavailable, ErrPlacerNoNodeAvailable.Error(), true
	case errors.Is(err, ErrResourceInUse):
		return http.StatusConflict, ErrResourceInUse.Error(), true
	case errors.Is(err, ErrResourceNameConflict):
		return http.StatusConflict, ErrResourceNameConflict.Error(), true
	case errors.Is(err, ErrDisplayOccupied):
		return http.StatusConflict, ErrDisplayOccupied.Error(), true
	case errors.Is(err, ErrDisplayExhausted):
		return http.StatusConflict, ErrDisplayExhausted.Error(), true
	case errors.Is(err, ErrDisplayConflict):
		return http.StatusConflict, ErrDisplayConflict.Error(), true
	case errors.Is(err, ErrConnectionAlreadyExists):
		return http.StatusConflict, ErrConnectionAlreadyExists.Error(), true
	case errors.Is(err, ErrConnectionStillStarting):
		return http.StatusConflict, ErrConnectionStillStarting.Error(), true
	case errors.Is(err, ErrConnectionStateUnknown):
		return http.StatusConflict, ErrConnectionStateUnknown.Error(), true
	default:
		return 0, "", false
	}
}
