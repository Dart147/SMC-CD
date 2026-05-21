package main

import (
	"github.com/Dart147/SMC/deploy/internal/config"
	"github.com/Dart147/SMC/deploy/internal/handler"
	"github.com/Dart147/SMC/deploy/internal/logger"
	"github.com/Dart147/SMC/deploy/internal/middleware"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.6.1"
	"go.temporal.io/sdk/client"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	AppName    = "deployment-service"
	Version    = "dev"
	BuildTime  = "unknown"
	CommitHash = "unknown"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Config validation failed: %v", err)
	}

	// Initialize logger
	zapLogger, err := initLogger(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer zapLogger.Sync()

	zapLogger.Info("Starting deployment service API",
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.String("commit_hash", CommitHash),
	)

	// Initialize OpenTelemetry
	shutdown, err := initOpenTelemetry(cfg, zapLogger)
	if err != nil {
		zapLogger.Fatal("Failed to initialize OpenTelemetry", zap.Error(err))
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			zapLogger.Error("Failed to shutdown OpenTelemetry", zap.Error(err))
		}
	}()

	// Create Temporal client
	temporalLogger := logger.NewZapLoggerAdapter(zapLogger)
	temporalClient, err := client.Dial(client.Options{
		HostPort:  cfg.Temporal.Address,
		Namespace: cfg.Temporal.Namespace,
		Logger:    temporalLogger,
	})
	if err != nil {
		zapLogger.Fatal("Failed to create Temporal client", zap.Error(err))
	}
	defer temporalClient.Close()

	// Create validator
	validator := validator.New()

	// Create handlers
	webhookHandler := handler.NewWebhookHandler(temporalClient, validator, zapLogger)

	// Create middlewares
	authMiddleware := middleware.NewAuthMiddleware(cfg.Auth.DeployToken, zapLogger)
	traceMiddleware := middleware.NewTraceMiddleware(zapLogger)

	// Setup routes
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Webhook endpoint
	mux.HandleFunc("POST /api/webhook/deploy",
		traceMiddleware.Middleware(
			authMiddleware.Middleware(
				webhookHandler.HandleDeploy,
			),
		),
	)

	// Create HTTP server
	srv := &http.Server{
		Addr:    cfg.Server.Host + ":" + cfg.Server.Port,
		Handler: mux,
	}

	// Start server in goroutine
	go func() {
		zapLogger.Info("Starting HTTP server",
			zap.String("host", cfg.Server.Host),
			zap.String("port", cfg.Server.Port),
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			zapLogger.Fatal("Failed to start server", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	<-ctx.Done()

	zapLogger.Info("Shutting down gracefully...")

	// Shutdown server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		zapLogger.Error("Server forced to shutdown", zap.Error(err))
	}

	stop()
	zapLogger.Info("Server stopped")
}

func initLogger(cfg *config.Config) (*zap.Logger, error) {
	var logger *zap.Logger
	var err error

	if cfg.Logger.Format == "console" {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}

	if err != nil {
		return nil, err
	}

	// Log level is set via logger config above
	return logger, nil
}

func initOpenTelemetry(cfg *config.Config, logger *zap.Logger) (func(context.Context) error, error) {
	if cfg.OTEL.CollectorURL == "" {
		logger.Info("OpenTelemetry collector URL not configured, tracing disabled")
		return func(context.Context) error { return nil }, nil
	}

	ctx := context.Background()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(AppName),
			semconv.ServiceVersionKey.String(Version),
			attribute.String("service.commit_hash", CommitHash),
			attribute.String("service.build_time", BuildTime),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	options := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	}

	conn, err := grpc.NewClient(cfg.OTEL.CollectorURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	options = append(options, sdktrace.WithSpanProcessor(bsp))

	tracerProvider := sdktrace.NewTracerProvider(options...)

	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tracerProvider.Shutdown, nil
}
