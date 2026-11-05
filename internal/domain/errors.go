package domain

import "errors"

// ErrNotFound is returned when a resource does not exist or is not
// visible to the caller (ownership miss is indistinguishable from a
// missing row on purpose).
var ErrNotFound = errors.New("not found")
