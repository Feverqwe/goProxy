package config

import (
	"sync"
)

// ConfigManager manages configuration with reload capability
type ConfigManager struct {
	config     *ProxyConfig
	configPath string
	mu         sync.RWMutex
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(configPath string) (*ConfigManager, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return &ConfigManager{
		config:     config,
		configPath: configPath,
	}, nil
}

// GetConfig returns a thread-safe copy of the configuration
func (cm *ConfigManager) GetConfig() *ProxyConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config
}

// ReloadConfig reloads the configuration from file
func (cm *ConfigManager) ReloadConfig() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	newConfig, err := LoadConfig(cm.configPath)
	if err != nil {
		return err
	}

	cm.config = newConfig
	return nil
}
