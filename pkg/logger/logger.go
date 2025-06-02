package logger

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	G = GetLogger
	L = logrus.NewEntry(newLogger())
)

type (
	loggerKey struct{}
)

func WithLogger(ctx context.Context, logger *logrus.Entry) context.Context {
	e := logger.WithContext(ctx)
	return context.WithValue(ctx, loggerKey{}, e)
}

func GetLogger(ctx context.Context) *logrus.Entry {
	logger := ctx.Value(loggerKey{})

	if logger == nil {
		return L.WithContext(ctx)
	}

	return logger.(*logrus.Entry)
}

func newLogger() *logrus.Logger {
	l := logrus.New()

	// Default to JSON format
	setLoggerFormat(l, "json")

	return l
}

// setLoggerFormat sets the formatter for the given logger
func setLoggerFormat(logger *logrus.Logger, format string) {
	switch format {
	case "text", "fmt":
		logger.Formatter = &logrus.TextFormatter{
			TimestampFormat: time.RFC3339Nano,
			FullTimestamp:   true,
		}
	case "json":
		fallthrough
	default:
		logger.Formatter = &logrus.JSONFormatter{
			FieldMap: logrus.FieldMap{
				logrus.FieldKeyTime:  "timestamp",
				logrus.FieldKeyLevel: "logLevel",
				logrus.FieldKeyMsg:   "message",
			},
			TimestampFormat: time.RFC3339Nano,
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
