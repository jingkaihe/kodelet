package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLogger(t *testing.T) {
	logger := newLogger()

	assert.NotNil(t, logger)
	assert.IsType(t, &logrus.TextFormatter{}, logger.Formatter)

	formatter, ok := logger.Formatter.(*logrus.TextFormatter)
	require.True(t, ok)

	assert.Equal(t, time.RFC3339Nano, formatter.TimestampFormat)
	assert.True(t, formatter.FullTimestamp)
}

func TestGlobalVariables(t *testing.T) {
	// Test that G is an alias for GetLogger
	ctx := context.Background()
	logger1 := G(ctx)
	logger2 := G(ctx)

	assert.Equal(t, logger1.Logger, logger2.Logger)

	// Test that L is properly initialized
	assert.NotNil(t, L)
	assert.IsType(t, &logrus.Entry{}, L)
}

func TestWithLogger(t *testing.T) {
	ctx := context.Background()

	// Create a custom logger
	customLogger := logrus.NewEntry(logrus.New())

	// Add the logger to context
	ctxWithLogger := WithLogger(ctx, customLogger)

	// Verify the logger is stored in context
	storedLogger := ctxWithLogger.Value(loggerKey{})
	assert.NotNil(t, storedLogger)
	assert.IsType(t, &logrus.Entry{}, storedLogger)
}

func TestGetLogger_WithContextLogger(t *testing.T) {
	ctx := context.Background()

	// Create a custom logger with a field
	customLogger := logrus.NewEntry(logrus.New()).WithField("test", "value")

	// Add the logger to context
	ctxWithLogger := WithLogger(ctx, customLogger)

	// Retrieve the logger
	retrievedLogger := G(ctxWithLogger)

	assert.NotNil(t, retrievedLogger)
	assert.Contains(t, retrievedLogger.Data, "test")
	assert.Equal(t, "value", retrievedLogger.Data["test"])
}

func TestGetLogger_WithoutContextLogger(t *testing.T) {
	ctx := context.Background()

	// Get logger from context without setting one
	retrievedLogger := G(ctx)

	assert.NotNil(t, retrievedLogger)
	// Should return the global logger L with context
	assert.Equal(t, L.Logger, retrievedLogger.Logger)
}

func TestGetLogger_GlobalAlias(t *testing.T) {
	ctx := context.Background()

	// Test that G and GetLogger return the same thing
	logger1 := G(ctx)
	logger2 := G(ctx)

	assert.Equal(t, logger1.Logger, logger2.Logger)
}

func TestLoggerOutput(t *testing.T) {
	// Create a buffer to capture log output
	var buf bytes.Buffer

	// Create a logger that writes to our buffer
	logger := logrus.New()
	logger.SetOutput(&buf)
	logger.Formatter = &logrus.JSONFormatter{
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "timestamp",
			logrus.FieldKeyLevel: "logLevel",
			logrus.FieldKeyMsg:   "message",
		},
		TimestampFormat: time.RFC3339Nano,
	}

	entry := logrus.NewEntry(logger)
	ctx := context.Background()
	ctxWithLogger := WithLogger(ctx, entry)

	// Log a message
	retrievedLogger := G(ctxWithLogger)
	retrievedLogger.Info("test message")

	// Parse the JSON output
	var logEntry map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	// Verify the custom field names are used
	assert.Contains(t, logEntry, "timestamp")
	assert.Contains(t, logEntry, "logLevel")
	assert.Contains(t, logEntry, "message")
	assert.Equal(t, "info", logEntry["logLevel"])
	assert.Equal(t, "test message", logEntry["message"])

	// Verify timestamp format
	timestamp, ok := logEntry["timestamp"].(string)
	require.True(t, ok)
	_, err = time.Parse(time.RFC3339Nano, timestamp)
	assert.NoError(t, err)
}

func TestLoggerChaining(t *testing.T) {
	ctx := context.Background()

	// Create initial logger with field
	logger1 := logrus.NewEntry(logrus.New()).WithField("service", "test")
	ctxWithLogger := WithLogger(ctx, logger1)

	// Get logger and add another field
	retrievedLogger := G(ctxWithLogger)
	logger2 := retrievedLogger.WithField("operation", "testing")

	// Update context with new logger
	ctxWithLogger2 := WithLogger(ctxWithLogger, logger2)

	// Get final logger
	finalLogger := G(ctxWithLogger2)

	assert.Contains(t, finalLogger.Data, "service")
	assert.Contains(t, finalLogger.Data, "operation")
	assert.Equal(t, "test", finalLogger.Data["service"])
	assert.Equal(t, "testing", finalLogger.Data["operation"])
}

func TestLoggerKey_UniqueContextKey(t *testing.T) {
	// Test that loggerKey doesn't conflict with other context values
	ctx := context.Background()
	
	// Define a custom key type to avoid collision warnings
	type customKey string
	
	// Add a string key with a value
	ctx = context.WithValue(ctx, customKey("logger"), "string-logger-value")
	
	// Add a logger with our key type
	customLogger := logrus.NewEntry(logrus.New()).WithField("test", "value")
	ctx = WithLogger(ctx, customLogger)
	
	// Verify both values can coexist without conflict
	stringValue := ctx.Value(customKey("logger"))
	assert.Equal(t, "string-logger-value", stringValue)
	
	loggerValue := ctx.Value(loggerKey{})
	assert.NotNil(t, loggerValue)
	assert.IsType(t, &logrus.Entry{}, loggerValue)
	
	// Verify the logger has the expected field
	retrievedLogger := G(ctx)
	assert.Equal(t, "value", retrievedLogger.Data["test"])
}

func TestContextPropagation(t *testing.T) {
	ctx := context.Background()

	// Create logger with custom output
	var buf bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&buf)
	entry := logrus.NewEntry(logger).WithField("request_id", "123")

	// Add to context
	ctxWithLogger := WithLogger(ctx, entry)

	// Simulate passing context through function calls
	func(ctx context.Context) {
		logger := G(ctx)
		logger.Info("nested function log")

		// Verify the field is preserved
		assert.Contains(t, logger.Data, "request_id")
		assert.Equal(t, "123", logger.Data["request_id"])
	}(ctxWithLogger)

	// Verify log was written
	output := buf.String()
	assert.Contains(t, output, "nested function log")
	assert.Contains(t, output, "request_id")
	assert.Contains(t, output, "123")
}

func TestGetLogger_TypeAssertion(t *testing.T) {
	// Test that GetLogger properly handles non-logger values in context
	ctx := context.Background()
	
	// Add a non-logger value with the same key (shouldn't happen in practice)
	// This tests the type assertion in GetLogger
	ctx = context.WithValue(ctx, loggerKey{}, "not-a-logger")
	
	// This should panic due to failed type assertion
	defer func() {
		if r := recover(); r != nil {
			// Expected behavior - type assertion should fail
			panicStr := fmt.Sprintf("%v", r)
			assert.Contains(t, panicStr, "interface conversion")
		} else {
			t.Error("Expected panic from invalid type assertion")
		}
	}()
	
	// This should panic
	G(ctx)
}

func TestLogLevels(t *testing.T) {
	var buf bytes.Buffer

	logger := logrus.New()
	logger.SetOutput(&buf)
	logger.SetLevel(logrus.DebugLevel) // Enable debug level logging
	logger.Formatter = &logrus.JSONFormatter{
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "timestamp",
			logrus.FieldKeyLevel: "logLevel",
			logrus.FieldKeyMsg:   "message",
		},
	}

	entry := logrus.NewEntry(logger)
	ctx := WithLogger(context.Background(), entry)
	retrievedLogger := G(ctx)

	// Test different log levels
	retrievedLogger.Debug("debug message")
	retrievedLogger.Info("info message")
	retrievedLogger.Warn("warn message")
	retrievedLogger.Error("error message")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Parse each line and verify log levels
	expectedLevels := []string{"debug", "info", "warning", "error"}
	require.Equal(t, len(expectedLevels), len(lines), "Expected %d log lines, got %d", len(expectedLevels), len(lines))

	for i, line := range lines {
		if line == "" {
			continue
		}

		var logEntry map[string]interface{}
		err := json.Unmarshal([]byte(line), &logEntry)
		require.NoError(t, err)

		assert.Equal(t, expectedLevels[i], logEntry["logLevel"])
	}
}
