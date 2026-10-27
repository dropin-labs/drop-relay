package buffer

import (
	"fmt"

	"github.com/dropin-labs/drop-relay/internal/config"
)

// NewBuffer constructs the appropriate buffer engine based on configuration.
func NewBuffer(cfg *config.Config) (Buffer, error) {
	switch cfg.BufferType {
	case "redis":
		return NewRedisBuffer(cfg.RedisURL, cfg.RedisStream, cfg.RedisGroup)
	case "memory":
		fallthrough
	default:
		if cfg.BufferType != "memory" && cfg.BufferType != "" {
			return nil, fmt.Errorf("unsupported buffer type: %s (valid options: memory, redis)", cfg.BufferType)
		}
		return NewMemoryBuffer(cfg.BufferCapacity), nil
	}
}
