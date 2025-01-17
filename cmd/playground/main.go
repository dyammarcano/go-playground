package main

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/x1unix/foundation/app"
	"github.com/x1unix/go-playground/pkg/analyzer"
	"github.com/x1unix/go-playground/pkg/compiler"
	"github.com/x1unix/go-playground/pkg/compiler/storage"
	"github.com/x1unix/go-playground/pkg/config"
	"github.com/x1unix/go-playground/pkg/goplay"
	"github.com/x1unix/go-playground/pkg/langserver"
	"github.com/x1unix/go-playground/pkg/langserver/webutil"
	"github.com/x1unix/go-playground/pkg/util/cmdutil"
	"github.com/x1unix/go-playground/pkg/util/osutil"
	"go.uber.org/zap"
)

// Version is server version symbol. Should be replaced by linker during build
var Version = "testing"

func main() {
	cfg, err := config.FromEnv(config.FromFlags())
	if err != nil {
		cmdutil.FatalOnError(err)
	}

	logger, err := cfg.Log.ZapLogger()
	if err != nil {
		cmdutil.FatalOnError(err)
	}
	zap.ReplaceGlobals(logger)
	analyzer.SetLogger(logger)
	defer logger.Sync() //nolint:errcheck

	goRoot, err := compiler.GOROOT()
	if err != nil {
		logger.Fatal("Failed to find GOROOT environment variable value", zap.Error(err))
	}

	if err := start(goRoot, logger, cfg); err != nil {
		logger.Fatal("Failed to start application", zap.Error(err))
	}
}

func start(goRoot string, logger *zap.Logger, cfg *config.Config) error {
	logger.Info("Starting service",
		zap.String("version", Version), zap.Any("config", cfg))
	analyzer.SetRoot(goRoot)
	packages, err := analyzer.ReadPackagesFile(cfg.Build.PackagesFile)
	if err != nil {
		return fmt.Errorf("failed to read packages file %q: %s", cfg.Build.PackagesFile, err)
	}

	store, err := storage.NewLocalStorage(logger.Sugar(), cfg.Build.BuildDir)
	if err != nil {
		return err
	}

	ctx, _ := app.GetApplicationContext()
	wg := &sync.WaitGroup{}
	go store.StartCleaner(ctx, cfg.Build.CleanupInterval, nil)

	// Initialize services
	pgClient := goplay.NewClient(cfg.Playground.PlaygroundURL, goplay.DefaultUserAgent,
		cfg.Playground.ConnectTimeout)
	goTipClient := goplay.NewClient(cfg.Playground.GoTipPlaygroundURL, goplay.DefaultUserAgent,
		cfg.Playground.ConnectTimeout)
	clients := &langserver.PlaygroundServices{
		Default: pgClient,
		GoTip:   goTipClient,
	}
	buildCfg := compiler.BuildEnvironmentConfig{
		IncludedEnvironmentVariables: osutil.SelectEnvironmentVariables(cfg.Build.BypassEnvVarsList...),
	}
	logger.Debug("Loaded list of environment variables used by compiler",
		zap.Any("vars", buildCfg.IncludedEnvironmentVariables))
	buildSvc := compiler.NewBuildService(zap.S(), buildCfg, store)

	// Initialize API endpoints
	r := mux.NewRouter()
	svcCfg := langserver.ServiceConfig{Version: Version}
	langserver.New(svcCfg, clients, packages, buildSvc).
		Mount(r.PathPrefix("/api").Subrouter())

	// Web UI routes
	tplVars := langserver.TemplateArguments{
		GoogleTagID: cfg.Services.GoogleAnalyticsID,
	}
	if tplVars.GoogleTagID != "" {
		if err := webutil.ValidateGTag(tplVars.GoogleTagID); err != nil {
			logger.Error("invalid GTag ID value, parameter will be ignored",
				zap.String("gtag", tplVars.GoogleTagID), zap.Error(err))
			tplVars.GoogleTagID = ""
		}
	}

	assetsDir := cfg.HTTP.AssetsDir
	indexHandler := langserver.NewTemplateFileServer(zap.L(), filepath.Join(assetsDir, langserver.IndexFileName), tplVars)
	spaHandler := langserver.NewSpaFileServer(assetsDir, tplVars)
	r.Path("/").
		Handler(indexHandler)
	r.Path("/snippet/{snippetID:[A-Za-z0-9_-]+}").
		Handler(indexHandler)
	r.PathPrefix("/").
		Handler(spaHandler)

	server := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      r,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	if err := startHttpServer(ctx, wg, server); err != nil {
		return err
	}

	wg.Wait()
	return nil
}

func startHttpServer(ctx context.Context, wg *sync.WaitGroup, server *http.Server) error {
	logger := zap.S()
	go func() {
		<-ctx.Done()
		logger.Info("Shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		defer wg.Done()
		server.SetKeepAlivesEnabled(false)
		if err := server.Shutdown(shutdownCtx); err != nil {
			if err == context.Canceled {
				return
			}
			logger.Errorf("Could not gracefully shutdown the server: %v\n", err)
		}
	}()

	wg.Add(1)
	logger.Infof("Listening on %q", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("cannot start server on %q: %s", server.Addr, err)
	}

	return nil
}
