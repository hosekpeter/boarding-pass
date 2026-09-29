package store

import (
	"context"

	"boarding-pass/internal/bcbp"
)

// Repository stores parsed boarding passes and reads their history.
type Repository interface {
	Save(context.Context, bcbp.BoardingPass) error
	List(context.Context, Filter) (Page, error)
}
