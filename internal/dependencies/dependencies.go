// Package dependencies constructs and owns the application services.
package dependencies

import (
	"boarding-pass/internal/base"
	"boarding-pass/internal/service"
	"boarding-pass/internal/store"
)

// ServiceScope is the wired application, used by main as the composition root.
// Close it after the HTTP server stops.
type ServiceScope interface {
	Base() base.Base
	Parser() *service.Service
	Repository() store.Repository
	Close() error
}
