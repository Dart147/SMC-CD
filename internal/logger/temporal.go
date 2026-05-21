package logger

import (
	"go.temporal.io/sdk/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ZapLoggerAdapter adapts zap.Logger to Temporal's log.Logger interface
type ZapLoggerAdapter struct {
	logger *zap.Logger
}

// NewZapLoggerAdapter creates a new adapter from zap.Logger
func NewZapLoggerAdapter(zapLogger *zap.Logger) *ZapLoggerAdapter {
	return &ZapLoggerAdapter{
		logger: zapLogger,
	}
}

// Debug logs a debug message
func (z *ZapLoggerAdapter) Debug(msg string, keyvals ...interface{}) {
	z.log(zap.DebugLevel, msg, keyvals...)
}

// Info logs an info message
func (z *ZapLoggerAdapter) Info(msg string, keyvals ...interface{}) {
	z.log(zap.InfoLevel, msg, keyvals...)
}

// Warn logs a warning message
func (z *ZapLoggerAdapter) Warn(msg string, keyvals ...interface{}) {
	z.log(zap.WarnLevel, msg, keyvals...)
}

// Error logs an error message
func (z *ZapLoggerAdapter) Error(msg string, keyvals ...interface{}) {
	z.log(zap.ErrorLevel, msg, keyvals...)
}

// With creates a new logger with additional key-value pairs
func (z *ZapLoggerAdapter) With(keyvals ...interface{}) log.Logger {
	fields := make([]zap.Field, 0, len(keyvals)/2)
	for i := 0; i < len(keyvals)-1; i += 2 {
		key, ok := keyvals[i].(string)
		if !ok {
			continue
		}
		value := keyvals[i+1]
		fields = append(fields, zap.Any(key, value))
	}
	return &ZapLoggerAdapter{
		logger: z.logger.With(fields...),
	}
}

func (z *ZapLoggerAdapter) log(level zapcore.Level, msg string, keyvals ...interface{}) {
	fields := make([]zap.Field, 0, len(keyvals)/2+1)
	fields = append(fields, zap.String("message", msg))

	for i := 0; i < len(keyvals)-1; i += 2 {
		key, ok := keyvals[i].(string)
		if !ok {
			continue
		}
		value := keyvals[i+1]
		fields = append(fields, zap.Any(key, value))
	}

	z.logger.Log(level, msg, fields...)
}
