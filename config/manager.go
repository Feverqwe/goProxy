package config

import (
	"goProxy/logger"
	"path/filepath"
	"sync"
)

type ConfigManager struct {
	config     *ProxyConfig
	configPath string
	mu         sync.RWMutex
}

func NewConfigManager(configPath string) (*ConfigManager, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	logger.ReconfigureGlobalLogger(config)

	return &ConfigManager{
		config:     config,
		configPath: configPath,
	}, nil
}

func (cm *ConfigManager) GetConfig() *ProxyConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config
}

func (cm *ConfigManager) ReloadConfig() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	newConfig, err := LoadConfig(cm.configPath)
	if err != nil {
		return err
	}

	cm.config = newConfig

	logger.ReconfigureGlobalLogger(newConfig)

	return nil
}

func (cm *ConfigManager) RefreshExternalRules(httpClientFunc HTTPClientFunc) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	configDir := filepath.Dir(cm.configPath)
	cm.config.RefreshExternalRules(configDir, httpClientFunc)
}
