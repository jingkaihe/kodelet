package logger

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetLogLevel(t *testing.T) {
	originalLevel := L.Logger.GetLevel()
	t.Cleanup(func() {
		L.Logger.SetLevel(originalLevel)
	})

	require.NoError(t, SetLogLevel("debug"))
	assert.Equal(t, logrus.DebugLevel, L.Logger.GetLevel())

	err := SetLogLevel("not-a-level")
	require.Error(t, err)
	assert.Equal(t, logrus.DebugLevel, L.Logger.GetLevel())
}

func TestSetLogLevelForLogger(t *testing.T) {
	testLogger := logrus.New()

	require.NoError(t, SetLogLevelForLogger(testLogger, "warn"))
	assert.Equal(t, logrus.WarnLevel, testLogger.GetLevel())

	err := SetLogLevelForLogger(testLogger, "bogus")
	require.Error(t, err)
	assert.Equal(t, logrus.WarnLevel, testLogger.GetLevel())
}

func TestSetLogFormat(t *testing.T) {
	originalFormatter := L.Logger.Formatter
	t.Cleanup(func() {
		L.Logger.Formatter = originalFormatter
	})

	SetLogFormat("json")
	assert.IsType(t, &logrus.JSONFormatter{}, L.Logger.Formatter)

	SetLogFormat("text")
	assert.IsType(t, &logrus.TextFormatter{}, L.Logger.Formatter)

	SetLogFormat("unknown")
	assert.IsType(t, &logrus.TextFormatter{}, L.Logger.Formatter)
}

func TestSetLogFormatForLogger(t *testing.T) {
	testLogger := logrus.New()

	SetLogFormatForLogger(testLogger, "json")
	jsonFormatter, ok := testLogger.Formatter.(*logrus.JSONFormatter)
	require.True(t, ok)
	assert.Equal(t, "timestamp", jsonFormatter.FieldMap[logrus.FieldKeyTime])
	assert.Equal(t, "logLevel", jsonFormatter.FieldMap[logrus.FieldKeyLevel])
	assert.Equal(t, "message", jsonFormatter.FieldMap[logrus.FieldKeyMsg])

	SetLogFormatForLogger(testLogger, "fmt")
	textFormatter, ok := testLogger.Formatter.(*logrus.TextFormatter)
	require.True(t, ok)
	assert.True(t, textFormatter.FullTimestamp)
}

func TestSetLogOutput(t *testing.T) {
	originalOutput := L.Logger.Out
	originalFormatter := L.Logger.Formatter
	t.Cleanup(func() {
		L.Logger.SetOutput(originalOutput)
		L.Logger.Formatter = originalFormatter
	})

	var buf bytes.Buffer
	SetLogOutput(&buf)
	SetLogFormat("json")

	L.WithField("component", "test").Info("setter output")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "setter output", entry["message"])
	assert.Equal(t, "test", entry["component"])
}
