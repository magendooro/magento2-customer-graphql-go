package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"os/signal"
	"syscall"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/magendooro/magento2-customer-graphql-go/graph"
	"github.com/magendooro/magento2-customer-graphql-go/internal/cache"
	"github.com/magendooro/magento2-customer-graphql-go/internal/config"
	"github.com/magendooro/magento2-customer-graphql-go/internal/database"
	"github.com/magendooro/magento2-customer-graphql-go/internal/jwt"
	"github.com/magendooro/magento2-customer-graphql-go/internal/middleware"
)

type App struct {
	cfg   *config.Config
	db    *sql.DB
	cache *cache.Client
}

func New(cfg *config.Config) (*App, error) {
	if cfg.Logging.Pretty {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
	level, err := zerolog.ParseLevel(cfg.Logging.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	db, err := database.NewConnection(cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("database connection failed: %w", err)
	}
	log.Info().Str("database", cfg.Database.Name).Msg("connected to database")

	redisCache := cache.New(cache.Config{
		Host:     cfg.Redis.Host,
		Port:     cfg.Redis.Port,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	return &App{cfg: cfg, db: db, cache: redisCache}, nil
}

func (a *App) Run() error {
	storeResolver := middleware.NewStoreResolver(a.db)

	// Initialize JWT manager (requires MAGENTO_CRYPT_KEY)
	var jwtManager *jwt.Manager
	if a.cfg.Magento.CryptKey != "" {
		jwtManager = jwt.NewManager(a.cfg.Magento.CryptKey, a.cfg.Magento.JWTTTLMinutes)
		log.Info().Int("ttl_minutes", a.cfg.Magento.JWTTTLMinutes).Msg("JWT authentication enabled")
	} else {
		log.Warn().Msg("MAGENTO_CRYPT_KEY not set — JWT token generation disabled, only legacy oauth_token supported")
	}

	tokenResolver := middleware.NewTokenResolver(a.db, jwtManager)

	resolver, err := graph.NewResolver(a.db, jwtManager)
	if err != nil {
		return fmt.Errorf("failed to create resolver: %w", err)
	}
	resolver.TokenResolver = tokenResolver

	srv := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{
		Resolvers: resolver,
	}))

	// Custom error presenter to add Magento-compatible extensions.category
	srv.SetErrorPresenter(magentoErrorPresenter)

	if a.cfg.GraphQL.ComplexityLimit > 0 {
		srv.Use(extension.FixedComplexityLimit(a.cfg.GraphQL.ComplexityLimit))
	}

	mux := http.NewServeMux()
	mux.Handle("/graphql", srv)
	mux.Handle("/{$}", playground.Handler("Magento Customer GraphQL", "/graphql"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if err := a.db.Ping(); err != nil {
			http.Error(w, "database unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Middleware chain (outermost first)
	var h http.Handler = mux
	h = middleware.CacheMiddleware(a.cache)(h)
	h = middleware.AuthMiddleware(tokenResolver)(h)
	h = middleware.StoreMiddleware(storeResolver)(h)
	h = middleware.LoggingMiddleware(h)
	h = middleware.CORSMiddleware(h)
	h = middleware.RecoveryMiddleware(h)

	server := &http.Server{
		Addr:         ":" + a.cfg.Server.Port,
		Handler:      h,
		ReadTimeout:  a.cfg.Server.ReadTimeout,
		WriteTimeout: a.cfg.Server.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Info().Str("port", a.cfg.Server.Port).Msg("server starting")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	<-done
	log.Info().Msg("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown failed: %w", err)
	}

	a.db.Close()
	if a.cache != nil {
		a.cache.Close()
	}
	log.Info().Msg("server stopped")
	return nil
}

// magentoErrorPresenter adds Magento-compatible extensions.category to GraphQL errors.
func magentoErrorPresenter(ctx context.Context, err error) *gqlerror.Error {
	gqlErr := graphql.DefaultErrorPresenter(ctx, err)

	msg := gqlErr.Message
	switch {
	case strings.Contains(msg, "isn't authorized"):
		gqlErr.Extensions = map[string]interface{}{"category": "graphql-authorization"}
	case strings.Contains(msg, "account sign-in was incorrect"),
		strings.Contains(msg, "token has been revoked"):
		gqlErr.Extensions = map[string]interface{}{"category": "graphql-authentication"}
	}

	return gqlErr
}
