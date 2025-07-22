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

type PriceService struct {
	currentPrice float64
	lastUpdated  time.Time
	mutex        sync.RWMutex
	client       *http.Client
}

type CoinGeckoResponse struct {
	Solana struct {
		USD float64 `json:"usd"`
	} `json:"solana"`
}

func NewPriceService() *PriceService {
	return &PriceService{
		currentPrice: 190.0,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

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

func (ps *PriceService) priceUpdateLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logrus.Info("🛑 SOL price service stopping")
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

func (ps *PriceService) GetPrice() float64 {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()
	return ps.currentPrice
}

func (ps *PriceService) GetLastUpdated() time.Time {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()
	return ps.lastUpdated
}

func (ps *PriceService) GetStats() map[string]interface{} {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()
	
	return map[string]interface{}{
		"current_price":  ps.currentPrice,
		"last_updated":   ps.lastUpdated.Format(time.RFC3339),
		"age_minutes":    time.Since(ps.lastUpdated).Minutes(),
	}
}
