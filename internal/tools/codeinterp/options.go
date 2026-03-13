package codeinterp

import "time"

type Tier int

const (
	TierLightweight Tier = iota
	TierFull
)

type Config struct {
	MaxSteps       int
	MaxWallTime    time.Duration
	MaxTraceBytes  int
	MaxResultBytes int

	MaxLLMCalls     int
	MaxLLMTokens    int
	MaxLLMCostUSD   float64
	LLMConcurrency  int
	DefaultLLMModel string

	MaxStoreBytes     int64
	MaxStoreKeySize   int
	MaxStoreValueSize int64

	Tier Tier
}

func DefaultLightweightConfig() Config {
	return Config{
		MaxSteps:          50,
		MaxWallTime:       10 * time.Second,
		MaxTraceBytes:     256 * 1024,
		MaxResultBytes:    256 * 1024,
		MaxLLMCalls:       0,
		MaxLLMTokens:      0,
		MaxLLMCostUSD:     0,
		LLMConcurrency:    1,
		MaxStoreBytes:     16 * 1024 * 1024,
		MaxStoreKeySize:   512,
		MaxStoreValueSize: 4 * 1024 * 1024,
		Tier:              TierLightweight,
	}
}

func DefaultFullConfig() Config {
	return Config{
		MaxSteps:          500,
		MaxWallTime:       300 * time.Second,
		MaxTraceBytes:     4 * 1024 * 1024,
		MaxResultBytes:    1024 * 1024,
		MaxLLMCalls:       50,
		MaxLLMTokens:      200000,
		MaxLLMCostUSD:     20,
		LLMConcurrency:    5,
		MaxStoreBytes:     512 * 1024 * 1024,
		MaxStoreKeySize:   512,
		MaxStoreValueSize: 64 * 1024 * 1024,
		Tier:              TierFull,
	}
}

func (c Config) withDefaults() Config {
	if c.MaxSteps == 0 {
		if c.Tier == TierFull {
			c = DefaultFullConfig()
		} else {
			c = DefaultLightweightConfig()
		}
	}
	if c.LLMConcurrency <= 0 {
		c.LLMConcurrency = 1
	}
	if c.MaxStoreKeySize <= 0 {
		c.MaxStoreKeySize = 512
	}
	if c.MaxStoreValueSize <= 0 {
		c.MaxStoreValueSize = 4 * 1024 * 1024
	}
	return c
}
