package host

import (
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/infrastructure/updates"
)

// Update checks do not depend on a writable profile or stored credentials.
func NewUpdates() *application.Updates {
	return application.NewUpdates(Version, updates.Client{})
}
