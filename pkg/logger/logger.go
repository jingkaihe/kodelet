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

	l.Formatter = &logrus.JSONFormatter{
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "timestamp",
			logrus.FieldKeyLevel: "logLevel",
			logrus.FieldKeyMsg:   "message",
		},
		TimestampFormat: time.RFC3339Nano,
	}

	return l
}
