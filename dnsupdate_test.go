package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateConfig(t *testing.T) {
	testCases := []struct {
		name        string
		config      *Config
		expectedErr string
	}{
		{
			name: "ValidConfig",
			config: &Config{
				AuthToken: "test",
				Hosts: map[string]HostConfig{
					"host1": {Interface: "eth0", Domain: "example.com"},
					"host2": {Interface: "en0", Domain: "something.com"},
				},
			},
			expectedErr: "",
		},
		{
			name: "MissingAuthToken",
			config: &Config{
				AuthToken: "",
				Hosts: map[string]HostConfig{
					"host1": {Interface: "eth0", Domain: "example.com"},
				},
			},
			expectedErr: "provider auth token not set",
		},
		{
			name: "NoHostsConfig",
			config: &Config{
				AuthToken: "test",
				Hosts:     map[string]HostConfig{},
			},
			expectedErr: "no hosts configuration set",
		},
		{
			name: "InvalidHostname",
			config: &Config{
				AuthToken: "test",
				Hosts: map[string]HostConfig{
					"": {Interface: "eth0", Domain: "example.com"},
				},
			},
			expectedErr: "invalid hostname ''",
		},
		{
			name: "MissingDomain",
			config: &Config{
				AuthToken: "test",
				Hosts: map[string]HostConfig{
					"host1": {Interface: "eth0", Domain: ""},
				},
			},
			expectedErr: "domain not set for hostname 'host1'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfig(tc.config)
			if tc.expectedErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedErr)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	want := &Config{
		AuthToken: "DIGITALOCEAN_TOKEN",
		Hosts: map[string]HostConfig{
			"db":  {Domain: "example.com"},
			"web": {Domain: "example.com", Interface: "en0"},
		},
	}
	got, err := loadConfig("./dnsupdate.toml")
	assert.NoError(t, err)
	assert.Equal(t, got, want)
}
