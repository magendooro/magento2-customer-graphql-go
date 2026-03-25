package graph

import (
	"database/sql"
	"fmt"

	"github.com/magendooro/magento2-go-common/config"
	"github.com/magendooro/magento2-go-common/jwt"
	"github.com/magendooro/magento2-go-common/middleware"
	"github.com/magendooro/magento2-customer-graphql-go/internal/repository"
	"github.com/magendooro/magento2-customer-graphql-go/internal/service"
)

// Resolver is the root resolver. It holds dependencies shared across all resolvers.
type Resolver struct {
	CustomerService *service.CustomerService
	OrderService    *service.OrderService
	TokenResolver   *middleware.TokenResolver
}

func NewResolver(db *sql.DB, jwtManager *jwt.Manager) (*Resolver, error) {
	// Initialize ConfigProvider (preloads all core_config_data)
	cp, err := config.NewConfigProvider(db)
	if err != nil {
		return nil, fmt.Errorf("failed to load config provider: %w", err)
	}

	customerRepo := repository.NewCustomerRepository(db)
	addressRepo := repository.NewAddressRepository(db)
	tokenRepo := repository.NewTokenRepository(db, jwtManager)
	newsletterRepo := repository.NewNewsletterRepository(db)
	storeRepo := repository.NewStoreRepository(db)
	groupRepo := repository.NewGroupRepository(db)
	eavRepo := repository.NewEAVAttributeRepository(db)
	orderRepo := repository.NewOrderRepository(db)

	customerService := service.NewCustomerService(
		customerRepo, addressRepo, tokenRepo, newsletterRepo, storeRepo, groupRepo, eavRepo, cp,
	)
	orderService := service.NewOrderService(orderRepo, cp)

	return &Resolver{
		CustomerService: customerService,
		OrderService:    orderService,
	}, nil
}
