package pagemark

import (
	"errors"
	"fmt"
)

var (
	ErrNoContent  = errors.New("pagemark: no useful content")
	ErrInvalidURL = errors.New("pagemark: invalid page URL")
	ErrLimit      = errors.New("pagemark: resource limit exceeded")
)

// LimitError reports a resource limit.
type LimitError struct {
	Resource string
	Count    int64
	Max      int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("pagemark: %s count %d exceeds maximum %d", e.Resource, e.Count, e.Max)
}

func (e *LimitError) Unwrap() error { return ErrLimit }
