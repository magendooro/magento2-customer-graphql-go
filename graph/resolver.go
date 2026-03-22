package graph

import (
	"database/sql"

	"github.com/magendooro/magento2-customer-graphql-go/internal/jwt"
	"github.com/magendooro/magento2-customer-graphql-go/internal/middleware"
	"github.com/magendooro/magento2-customer-graphql-go/internal/repository"
	"github.com/magendooro/magento2-customer-graphql-go/internal/service"
)

// Resolver is the root resolver. It holds dependencies shared across all resolvers.
type Resolver struct {
	CustomerService *service.CustomerService
	TokenResolver   *middleware.TokenResolver
}

func NewResolver(db *sql.DB, jwtManager *jwt.Manager) (*Resolver, error) {
	customerRepo := repository.NewCustomerRepository(db)
	addressRepo := repository.NewAddressRepository(db)
	tokenRepo := repository.NewTokenRepository(db, jwtManager)
	newsletterRepo := repository.NewNewsletterRepository(db)
	storeRepo := repository.NewStoreRepository(db)
	groupRepo := repository.NewGroupRepository(db)

	customerService := service.NewCustomerService(
		customerRepo, addressRepo, tokenRepo, newsletterRepo, storeRepo, groupRepo, db,
	)

	return &Resolver{
		CustomerService: customerService,
	}, nil
}
