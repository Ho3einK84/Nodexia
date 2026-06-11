package servers

import (
	"context"
	"time"
)

type Server struct {
	ID                 int64
	Name               string
	Host               string
	Port               int
	AuthMode           string
	Username           string
	Note               string
	Tags               []string
	CredentialStrategy string
	CredentialRef      string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Repository interface {
	Create(ctx context.Context, server Server) (Server, error)
	GetByID(ctx context.Context, id int64) (Server, error)
	List(ctx context.Context) ([]Server, error)
	Update(ctx context.Context, server Server) (Server, error)
	Delete(ctx context.Context, id int64) error
}
