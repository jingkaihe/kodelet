// Package logger provides context-aware structured logging functionality
// using logrus. It offers global logger access and context-based logger
// management for consistent logging across the application.
package logger

import (
	"context"
	"io"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	// G is a convenience alias for GetLogger, providing quick access to context-aware logger retrieval.
	G = GetLogger
	// L is the global logger entry used as a fallback when no logger is found in context.
	L = logrus.NewEntry(newLogger())
)

type (
	loggerKey struct{}
)

// WithLogger attaches a logger entry to the given context, making it retrievable via GetLogger.
func WithLogger(ctx context.Context, logger *logrus.Entry) context.Context {
	e := logger.WithContext(ctx)
	return context.WithValue(ctx, loggerKey{}, e)
}

// GetLogger retrieves the logger entry from the context. If no logger is found,
// it returns the global logger L with the context attached.
func GetLogger(ctx context.Context) *logrus.Entry {
	logger := ctx.Value(loggerKey{})

	if logger == nil {
		return L.WithContext(ctx)
	}

	return logger.(*logrus.Entry)
}

func newLogger() *logrus.Logger {
	l := logrus.New()

	// Default to formatted text format
	setLoggerFormat(l, "fmt")

	return l
}

// setLoggerFormat sets the formatter for the given logger
func setLoggerFormat(logger *logrus.Logger, format string) {
	switch format {
	case "json":
		logger.Formatter = &logrus.JSONFormatter{
			FieldMap: logrus.FieldMap{
				logrus.FieldKeyTime:  "timestamp",
				logrus.FieldKeyLevel: "logLevel",
				logrus.FieldKeyMsg:   "message",
			},
			TimestampFormat: time.RFC3339Nano,
		}
	case "text", "fmt":
		fallthrough
	default:
		logger.Formatter = &logrus.TextFormatter{
			TimestampFormat: time.RFC3339Nano,
			FullTimestamp:   true,
		}
	}
}

// SetLogLevel sets the log level for the global logger
func SetLogLevel(level string) error {
	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		return err
	}
	L.Logger.SetLevel(logLevel)
	return nil
}

// SetLogLevelForLogger sets the log level for a specific logger
func SetLogLevelForLogger(logger *logrus.Logger, level string) error {
	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		return err
	}
	logger.SetLevel(logLevel)
	return nil
}

// SetLogFormat sets the log format for the global logger
func SetLogFormat(format string) {
	setLoggerFormat(L.Logger, format)
}

// SetLogFormatForLogger sets the log format for a specific logger
func SetLogFormatForLogger(logger *logrus.Logger, format string) {
	setLoggerFormat(logger, format)
}

// SetLogOutput sets the output destination for the global logger
func SetLogOutput(w io.Writer) {
	L.Logger.SetOutput(w)
}
