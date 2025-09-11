package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	BotToken    string `envconfig:"BOT_TOKEN" required:"true"`
	AdminToken  string `envconfig:"ADMIN_TOKEN" required:"true"`
	
	// Database configuration - individual fields
	DBType     string `envconfig:"DB_TYPE" default:"sqlite"` // sqlite or postgres
	DBHost     string `envconfig:"DB_HOST" default:"localhost"`
	DBPort     string `envconfig:"DB_PORT" default:"5432"`
	DBName     string `envconfig:"DB_NAME" default:"shop.db"`
	DBUser     string `envconfig:"DB_USER" default:""`
	DBPassword string `envconfig:"DB_PASSWORD" default:""`
	DBSSLMode  string `envconfig:"DB_SSL_MODE" default:"disable"`
	
	// Legacy DB_DSN for backward compatibility
	DBDSN       string `envconfig:"DB_DSN" default:""`
	
	// Payment configuration
	EpayPID     string `envconfig:"EPAY_PID" default:""`
	EpayKey     string `envconfig:"EPAY_KEY" default:""`
	EpayGateway string `envconfig:"EPAY_GATEWAY" default:""`
	BaseURL     string `envconfig:"BASE_URL" default:"http://localhost:7832"`
	
	// Webhook configuration
	UseWebhook  bool   `envconfig:"USE_WEBHOOK" default:"false"`
	WebhookURL  string `envconfig:"WEBHOOK_URL"`
	WebhookPort int    `envconfig:"WEBHOOK_PORT" default:"9147"`
	
	// HTTP Server configuration
	Port        int    `envconfig:"PORT" default:"7832"`
	
	// Currency configuration
	Currency     string `envconfig:"CURRENCY" default:"CNY"` // CNY, USD, EUR, etc.
	CurrencySymbol string `envconfig:"CURRENCY_SYMBOL" default:"¥"` // ¥, $, €, etc.
	
	// Redis configuration - individual fields
	RedisHost     string `envconfig:"REDIS_HOST" default:"localhost"`
	RedisPort     string `envconfig:"REDIS_PORT" default:"6379"`
	RedisPassword string `envconfig:"REDIS_PASSWORD" default:""`
	RedisDB       int    `envconfig:"REDIS_DB" default:"0"`
	
	// Legacy REDIS_URL for backward compatibility
	RedisURL    string `envconfig:"REDIS_URL"`
	
	// Admin notification configuration
	AdminNotifications bool   `envconfig:"ADMIN_NOTIFICATIONS" default:"true"`
	AdminTelegramIDs   string `envconfig:"ADMIN_TELEGRAM_IDS" default:""` // Comma-separated list of Telegram user IDs
}

// GetDBDSN constructs the database DSN from individual fields or returns the legacy DSN
func (c *Config) GetDBDSN() string {
	// If legacy DB_DSN is provided, use it
	if c.DBDSN != "" {
		return c.DBDSN
	}
	
	// Otherwise, construct DSN from individual fields
	switch strings.ToLower(c.DBType) {
	case "postgres", "postgresql":
		dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
			c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode)
		return dsn
	case "sqlite", "sqlite3":
		// For SQLite, DBName is the file path
		if c.DBName == ":memory:" {
			return ":memory:"
		}
		return fmt.Sprintf("file:%s?_busy_timeout=5000&cache=shared", c.DBName)
	default:
		// Default to SQLite
		return fmt.Sprintf("file:%s?_busy_timeout=5000&cache=shared", c.DBName)
	}
}

// GetRedisURL constructs the Redis URL from individual fields or returns the legacy URL
func (c *Config) GetRedisURL() string {
	// If legacy REDIS_URL is provided, use it
	if c.RedisURL != "" {
		return c.RedisURL
	}
	
	// Otherwise, construct URL from individual fields
	if c.RedisPassword != "" {
		return fmt.Sprintf("redis://:%s@%s:%s/%d", c.RedisPassword, c.RedisHost, c.RedisPort, c.RedisDB)
	}
	return fmt.Sprintf("redis://%s:%s/%d", c.RedisHost, c.RedisPort, c.RedisDB)
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetAdminTelegramIDs returns a list of admin Telegram user IDs
func (c *Config) GetAdminTelegramIDs() []int64 {
	if c.AdminTelegramIDs == "" {
		return nil
	}
	
	parts := strings.Split(c.AdminTelegramIDs, ",")
	ids := make([]int64, 0, len(parts))
	
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		
		id, err := strconv.ParseInt(part, 10, 64)
		if err == nil {
			ids = append(ids, id)
		}
	}
	
	return ids
}