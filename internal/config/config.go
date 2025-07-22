package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"golang-pumpfun-sniper/internal/price"
	"golang-pumpfun-sniper/internal/utils"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

// Config holds all configuration for the sniper bot
type Config struct {
	// GRPC Settings
	GRPCEndpoint string
	GRPCToken    string
	
	// RPC Settings
	RPCEndpoint string
	
	// Wallet Configuration
	PrivateKey string
	
	// Trading Parameters
	BuyAmountSOL  float64
	MinMarketCap  float64
	MaxSlippage   float64
	
	// Performance Settings
	MaxRetries     int
	TimeoutSeconds int
	LogLevel       string
	
	// Pump.Fun Configuration
	PumpFunProgramID             string
	GlobalAccount                string
	FeeRecipient                 string
	BuyInstructionDiscriminator  uint64
	
	// Market Cap Calculation Settings
	TotalSupply   float64
	
	// Price Service
	PriceService *price.PriceService
	
	// Runtime flags
	SimulateMode bool
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		logrus.Warn("No .env file found, using environment variables")
	}
	
	config := &Config{}
	
	// Required fields
	config.GRPCEndpoint = getEnvRequired("GRPC_ENDPOINT")
	config.GRPCToken = getEnvRequired("GRPC_TOKEN")
	config.RPCEndpoint = getEnvRequired("RPC_ENDPOINT")
	config.PrivateKey = getEnvRequired("PRIVATE_KEY")
	
	// Trading parameters with defaults
	config.BuyAmountSOL = getEnvFloat("BUY_AMOUNT_SOL", 0.01)
	config.MinMarketCap = getEnvFloat("MIN_MARKET_CAP", 8000.0)
	config.MaxSlippage = getEnvFloat("MAX_SLIPPAGE", 0.05)
	
	// Performance settings with defaults
	config.MaxRetries = getEnvInt("MAX_RETRIES", 3)
	config.TimeoutSeconds = getEnvInt("TIMEOUT_SECONDS", 10)
	config.LogLevel = getEnv("LOG_LEVEL", "info")
	
	// Pump.Fun configuration
	config.PumpFunProgramID = getEnv("PUMP_FUN_PROGRAM_ID", "6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P")
	config.GlobalAccount = getEnv("GLOBAL_ACCOUNT", "4wTV1YmiEkRvAtNtsSGPtUrqRYQMe5db6SJn7TDx3PSu")
	config.FeeRecipient = getEnv("FEE_RECIPIENT", "CebN5WGQ4jvEPvsVU4EoHEpgzq1VV1W2mYkQ8JDKX9E")
	config.BuyInstructionDiscriminator = getEnvUint64("BUY_INSTRUCTION_DISCRIMINATOR", 16927863322537952870)
	
	// Market cap calculation settings
	config.TotalSupply = getEnvFloat("TOTAL_SUPPLY", 1000000000) // 1 billion tokens (typical)
	
	// Initialize price service
	config.PriceService = price.NewPriceService()
	
	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}
	
	return config, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.BuyAmountSOL <= 0 {
		return fmt.Errorf("BUY_AMOUNT_SOL must be positive, got: %f", c.BuyAmountSOL)
	}
	
	if c.MinMarketCap < 0 {
		return fmt.Errorf("MIN_MARKET_CAP must be non-negative, got: %f", c.MinMarketCap)
	}
	
	if c.MaxSlippage < 0 || c.MaxSlippage > 1 {
		return fmt.Errorf("MAX_SLIPPAGE must be between 0 and 1, got: %f", c.MaxSlippage)
	}
	
	if c.MaxRetries < 1 {
		return fmt.Errorf("MAX_RETRIES must be at least 1, got: %d", c.MaxRetries)
	}
	
	if c.TimeoutSeconds < 1 {
		return fmt.Errorf("TIMEOUT_SECONDS must be at least 1, got: %d", c.TimeoutSeconds)
	}
	
	return nil
}

// GetTimeout returns the timeout duration
func (c *Config) GetTimeout() time.Duration {
	return time.Duration(c.TimeoutSeconds) * time.Second
}

// GetCurrentSOLPrice returns the current SOL price
func (c *Config) GetCurrentSOLPrice() float64 {
	return c.PriceService.GetPrice()
}

// LogConfig logs the current configuration
func (c *Config) LogConfig() {
	logrus.WithFields(logrus.Fields{
		"rpc_endpoint":    utils.SanitizeURL(c.RPCEndpoint),
		"grpc_endpoint":   utils.SanitizeURL(c.GRPCEndpoint),
		"private_key":     utils.SanitizePrivateKey(c.PrivateKey),
		"buy_amount":      fmt.Sprintf("%.3f SOL", c.BuyAmountSOL),
		"min_market_cap":  fmt.Sprintf("$%.0f", c.MinMarketCap),
		"simulate_mode":   c.SimulateMode,
	}).Info("📋 Configuration loaded")
}

// Helper functions for environment variable handling

func getEnvRequired(key string) string {
	value := os.Getenv(key)
	if value == "" {
		logrus.Fatalf("Required environment variable %s is not set", key)
	}
	return value
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
		logrus.Warnf("Invalid integer value for %s: %s, using default: %d", key, value, defaultValue)
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
			return floatVal
		}
		logrus.Warnf("Invalid float value for %s: %s, using default: %f", key, value, defaultValue)
	}
	return defaultValue
}

func getEnvUint64(key string, defaultValue uint64) uint64 {
	if value := os.Getenv(key); value != "" {
		if uint64Val, err := strconv.ParseUint(value, 10, 64); err == nil {
			return uint64Val
		}
		logrus.Warnf("Invalid uint64 value for %s: %s, using default: %d", key, value, defaultValue)
	}
	return defaultValue
}
