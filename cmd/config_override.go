package cmd

import (
	"fmt"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// stringArrayFlag implements flag.Value for repeatable string flags.
type stringArrayFlag []string

func (a *stringArrayFlag) String() string {
	if a == nil {
		return ""
	}
	return strings.Join(*a, ", ")
}

func (a *stringArrayFlag) Set(value string) error {
	*a = append(*a, value)
	return nil
}

// configOverride represents a single key=value config override.
type configOverride struct {
	Key   string
	Value string
}

// parseConfigOverride parses a "key=value" string into a configOverride.
func parseConfigOverride(raw string) (configOverride, error) {
	idx := strings.Index(raw, "=")
	if idx <= 0 {
		return configOverride{}, fmt.Errorf("config override must be in key=value format, got %q", raw)
	}
	key := strings.TrimSpace(raw[:idx])
	value := strings.TrimSpace(raw[idx+1:])
	if key == "" {
		return configOverride{}, fmt.Errorf("config override key must not be empty in %q", raw)
	}
	return configOverride{Key: key, Value: value}, nil
}

// parseConfigOverridesFromFlag converts raw flag values into parsed configOverride slices.
func parseConfigOverridesFromFlag(rawValues []string) ([]configOverride, error) {
	overrides := make([]configOverride, 0, len(rawValues))
	for _, raw := range rawValues {
		o, err := parseConfigOverride(raw)
		if err != nil {
			return nil, err
		}
		overrides = append(overrides, o)
	}
	return overrides, nil
}

// applyConfigOverrides applies parsed overrides to a config. Each override
// uses the same validation as `assignConfigValue` in config.go.
func applyConfigOverrides(config internalconfig.Config, overrides []configOverride) (internalconfig.Config, error) {
	for _, o := range overrides {
		normalizedKey := normalizeConfigKey(o.Key)
		if err := assignConfigValue(&config, normalizedKey, o.Value); err != nil {
			return config, fmt.Errorf("config override %s=%s: %w", o.Key, o.Value, err)
		}
	}
	return config, nil
}
