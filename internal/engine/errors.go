package engine

import (
	"errors"
	"fmt"
)

var (
	// ErrNoContent means that Pagemark did not find useful output content.
	ErrNoContent = errors.New("pagemark: no useful content")
	// ErrInvalidURL means that the supplied page URL is not an absolute HTTP or HTTPS URL.
	ErrInvalidURL = errors.New("pagemark: invalid page URL")
	// ErrLimit means that the input or HTML tree exceeded a resource limit.
	// Use errors.As to get the related *LimitError.
	ErrLimit = errors.New("pagemark: resource limit exceeded")
	// ErrInvalidOption means that one or more extraction options are invalid.
	ErrInvalidOption = errors.New("pagemark: invalid option")
)

// LimitResource identifies a resource that can trigger ErrLimit.
type LimitResource string

const (
	LimitInputBytes     LimitResource = "input-bytes"
	LimitElements       LimitResource = "elements"
	LimitDepth          LimitResource = "depth"
	LimitAttributes     LimitResource = "attributes"
	LimitAttributeBytes LimitResource = "attribute-bytes"
	LimitTextBytes      LimitResource = "text-bytes"
)

// LimitError reports a resource limit. Use errors.Is(err, ErrLimit) to test it.
type LimitError struct {
	// Resource identifies the limited resource.
	Resource LimitResource
	// Count is the observed resource count.
	Count int64
	// Max is the configured maximum.
	Max int64
}

// Error returns the resource-limit message.
func (e *LimitError) Error() string {
	return fmt.Sprintf("pagemark: %s count %d exceeds maximum %d", e.Resource, e.Count, e.Max)
}

// Unwrap returns ErrLimit.
func (e *LimitError) Unwrap() error { return ErrLimit }
