package main

import (
	"github.com/Dart147/SMC/deploy/internal/activity"
	"github.com/Dart147/SMC/deploy/internal/adapter/discord"
	"github.com/Dart147/SMC/deploy/internal/adapter/ssh"
	"github.com/Dart147/SMC/deploy/internal/config"
	"github.com/Dart147/SMC/deploy/internal/logger"
	"github.com/Dart147/SMC/deploy/internal/workflow"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.6.1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	AppName    = "deployment-service-worker"
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

	// Initialize logger
	zapLogger, err := initLogger(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer zapLogger.Sync()

	zapLogger.Info("Starting deployment service worker",
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

	// Create adapters (Infisical + Cloudflare stripped — see CLAUDE.md §POC scope)
	sshClient := ssh.NewClient(cfg.SSH, zapLogger)
	discordClient := discord.NewClient(cfg.Discord.BotToken, cfg.Discord.DefaultChannelID, zapLogger)

	// Create activities
	sshActivity := activity.NewSSHActivity(sshClient, cfg.SSH, zapLogger)
	notifyActivity := activity.NewNotifyActivity(discordClient, zapLogger)

	// Create worker
	w := worker.New(temporalClient, "cd-task-queue", worker.Options{})

	// Register workflows
	w.RegisterWorkflow(workflow.CDWorkflow)

	// Register activities
	w.RegisterActivity(sshActivity.RunSSHDeploy)
	w.RegisterActivity(notifyActivity.SendDiscordNotification)

	zapLogger.Info("Worker registered, starting...")

	// Start worker
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = w.Run(worker.InterruptCh())
	if err != nil {
		zapLogger.Fatal("Worker failed", zap.Error(err))
	}

	<-ctx.Done()
	zapLogger.Info("Worker stopped")
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
