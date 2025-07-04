package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateServeConfig(t *testing.T) {
	tests := []struct {
		name          string
		config        *ServeConfig
		expectedError string
	}{
		{
			name: "valid config",
			config: &ServeConfig{
				Host: "localhost",
				Port: 8080,
			},
		},
		{
			name: "valid IP address",
			config: &ServeConfig{
				Host: "127.0.0.1",
				Port: 8080,
			},
		},
		{
			name: "valid 0.0.0.0",
			config: &ServeConfig{
				Host: "0.0.0.0",
				Port: 3000,
			},
		},
		{
			name: "empty host",
			config: &ServeConfig{
				Host: "",
				Port: 8080,
			},
			expectedError: "host cannot be empty",
		},
		{
			name: "invalid host with space",
			config: &ServeConfig{
				Host: "local host",
				Port: 8080,
			},
			expectedError: "invalid host: local host",
		},
		{
			name: "invalid host with colon",
			config: &ServeConfig{
				Host: "localhost:8080",
				Port: 8080,
			},
			expectedError: "invalid host: localhost:8080",
		},
		{
			name: "port too low",
			config: &ServeConfig{
				Host: "localhost",
				Port: 0,
			},
			expectedError: "port must be between 1 and 65535",
		},
		{
			name: "port too high",
			config: &ServeConfig{
				Host: "localhost",
				Port: 65536,
			},
			expectedError: "port must be between 1 and 65535",
		},
		{
			name: "privileged port warning",
			config: &ServeConfig{
				Host: "localhost",
				Port: 80,
			},
			// No error expected, just a warning logged
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServeConfig(tt.config)
			
			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}