package kernel

import "github.com/google/uuid"

// NewID returns a new UUIDv7 (SDD §11.1).
func NewID() uuid.UUID { return uuid.Must(uuid.NewV7()) }
