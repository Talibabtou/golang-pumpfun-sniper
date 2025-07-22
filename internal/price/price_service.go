// Package price provides real-time SOL price tracking using the CoinGecko API.
// It maintains an up-to-date SOL/USD price for market cap calculations and
// trading decisions with automatic refresh and fallback mechanisms.
//
// The service operates by:
// 1. Fetching initial SOL price from CoinGecko API
// 2. Updating price every 5 minutes in background
// 3. Providing thread-safe access to current price
// 4. Detecting and logging significant price movements
//
// It handles API failures gracefully by using cached values and provides
// detailed statistics for monitoring price data freshness.
package price

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// PriceService manages real-time SOL price tracking with automatic updates
// from the CoinGecko API. It provides thread-safe access to current price
// data and maintains statistics about price movements and data freshness.
type PriceService struct {
	currentPrice float64      // Current SOL price in USD
	lastUpdated  time.Time    // Timestamp of last successful price fetch
	mutex        sync.RWMutex // Protects concurrent access to price data
	client       *http.Client // HTTP client for API requests
}

// CoinGeckoResponse represents the JSON response structure from CoinGecko's
// simple price API endpoint for SOL/USD price queries.
type CoinGeckoResponse struct {
	Solana struct {
		USD float64 `json:"usd"`
	} `json:"solana"`
}

// NewPriceService creates a new PriceService instance with default configuration.
// It initializes with a fallback price of $190 and sets up HTTP client with
// appropriate timeout for reliable API communication.
func NewPriceService() *PriceService {
	return &PriceService{
		currentPrice: 190.0, // Default fallback price
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Start initializes the price service by fetching the initial SOL price
// and starting the background update loop. It gracefully handles initial
// fetch failures by using the default price and continues operation.
//
// The service runs continuously until the context is cancelled.
func (ps *PriceService) Start(ctx context.Context) error {
	logrus.Info("💰 Starting SOL price service...")
	
	if err := ps.fetchPrice(); err != nil {
		logrus.WithError(err).Warn("Failed to fetch initial SOL price, using default")
	} else {
		logrus.WithField("price", fmt.Sprintf("$%.2f", ps.GetPrice())).Info("✅ Initial SOL price fetched")
	}

	go ps.priceUpdateLoop(ctx)
	
	return nil
}

// priceUpdateLoop runs the background price update mechanism, fetching
// fresh SOL prices every 5 minutes. It handles context cancellation
// gracefully and logs update status for monitoring.
func (ps *PriceService) priceUpdateLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logrus.Info("🛑 Price service stopping")
			return
		case <-ticker.C:
			if err := ps.fetchPrice(); err != nil {
				logrus.WithError(err).Warn("Failed to update SOL price")
			} else {
				logrus.WithField("price", fmt.Sprintf("$%.2f", ps.GetPrice())).Debug("💰 SOL price updated")
			}
		}
	}
}

// fetchPrice retrieves the current SOL price from CoinGecko API and updates
// the internal price state. It validates the response and detects significant
// price changes (>2% movement) for enhanced monitoring.
//
// Returns an error if the API request fails or returns invalid data.
func (ps *PriceService) fetchPrice() error {
	url := "https://api.coingecko.com/api/v3/simple/price?ids=solana&vs_currencies=usd"
	
	resp, err := ps.client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch price: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("price API returned status %d", resp.StatusCode)
	}

	var priceResp CoinGeckoResponse
	if err := json.NewDecoder(resp.Body).Decode(&priceResp); err != nil {
		return fmt.Errorf("failed to decode price response: %w", err)
	}

	newPrice := priceResp.Solana.USD
	if newPrice <= 0 {
		return fmt.Errorf("invalid price received: %f", newPrice)
	}

	ps.mutex.Lock()
	oldPrice := ps.currentPrice
	ps.currentPrice = newPrice
	ps.lastUpdated = time.Now()
	ps.mutex.Unlock()

	if oldPrice > 0 {
		change := ((newPrice - oldPrice) / oldPrice) * 100
		if change > 2 || change < -2 {
			logrus.WithFields(logrus.Fields{
				"old_price": fmt.Sprintf("$%.2f", oldPrice),
				"new_price": fmt.Sprintf("$%.2f", newPrice),
				"change":    fmt.Sprintf("%.1f%%", change),
			}).Info("💹 Significant SOL price change detected")
		}
	}

	return nil
}

// GetPrice returns the current SOL price in USD with thread-safe access.
// This method can be called concurrently from multiple goroutines.
func (ps *PriceService) GetPrice() float64 {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()
	return ps.currentPrice
}

// GetLastUpdated returns the timestamp of the last successful price update.
// This can be used to determine the freshness of the current price data.
func (ps *PriceService) GetLastUpdated() time.Time {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()
	return ps.lastUpdated
}

// GetStats returns comprehensive statistics about the price service state
// including current price, last update time, and data age in minutes.
// This is useful for monitoring and debugging price service health.
func (ps *PriceService) GetStats() map[string]interface{} {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()
	
	return map[string]interface{}{
		"current_price":  ps.currentPrice,
		"last_updated":   ps.lastUpdated.Format(time.RFC3339),
		"age_minutes":    time.Since(ps.lastUpdated).Minutes(),
	}
}
