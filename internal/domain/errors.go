package domain

import "errors"

// ErrNotFound is returned when a resource does not exist or is not
// visible to the caller (ownership miss is indistinguishable from a
// missing row on purpose).
var ErrNotFound = errors.New("not found")

// ErrAgentInUse is returned when an agent cannot be deleted because
// conversations are still bound to it (spec: Delete protection). The
// HTTP layer maps it to 409 Conflict; deleting or re-binding those
// conversations first unblocks the delete.
var ErrAgentInUse = errors.New("agent in use")

// ErrAgentExists is returned when an agent id is already taken (spec:
// the slug id is the stable identity of an agent). The HTTP layer maps
// it to 409 Conflict.
var ErrAgentExists = errors.New("agent id already exists")
