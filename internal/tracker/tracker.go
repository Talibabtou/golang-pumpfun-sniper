package tracker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang-pumpfun-sniper/internal/config"
	"golang-pumpfun-sniper/internal/logger"
	"golang-pumpfun-sniper/internal/parser"

	"github.com/gagliardetto/solana-go"
	"github.com/sirupsen/logrus"
)

// TrackedToken represents a token being monitored for market cap threshold crossing
type TrackedToken struct {
	Mint         solana.PublicKey
	CreatedAt    time.Time
	InitialMC    float64
	BondingCurve solana.PublicKey
	User         solana.PublicKey
	LastChecked  time.Time
}

// TokenTracker monitors newly created tokens and triggers trades when they cross thresholds
type TokenTracker struct {
	config         *config.Config
	parser         *parser.PumpFunParser
	trackedTokens  map[string]*TrackedToken
	mutex          sync.RWMutex
	tradeChannel   chan<- *parser.TokenLaunchData
	checkInterval  time.Duration
	trackDuration  time.Duration
	threshold      float64
}

// NewTokenTracker creates a new token tracker instance
func NewTokenTracker(cfg *config.Config, parser *parser.PumpFunParser, tradeChannel chan<- *parser.TokenLaunchData) *TokenTracker {
	return &TokenTracker{
		config:        cfg,
		parser:        parser,
		trackedTokens: make(map[string]*TrackedToken),
		tradeChannel:  tradeChannel,
		checkInterval: 5 * time.Second,  // Check every 5 seconds
		trackDuration: 60 * time.Second, // Track for 1 minute
		threshold:     8000.0,           // $8K threshold
	}
}

// Start begins the token tracking goroutines
func (tt *TokenTracker) Start(ctx context.Context) {
	logrus.Info("🔄 Starting token tracker - monitoring new tokens for 1 minute each")
	
	// Start the monitoring goroutine
	go tt.monitorTrackedTokens(ctx)
	
	// Start the cleanup goroutine
	go tt.cleanupExpiredTokens(ctx)
}

// AddToken adds a newly created token to tracking
func (tt *TokenTracker) AddToken(tokenData *parser.TokenLaunchData) {
	if tokenData.InstructionType != "create" {
		return // Only track CREATE instructions
	}

	tt.mutex.Lock()
	defer tt.mutex.Unlock()

	mintStr := tokenData.Mint.String()
	
	// Check if already tracking this token
	if _, exists := tt.trackedTokens[mintStr]; exists {
		logrus.WithField("mint", mintStr[:8]+"...").Debug("🔄 Token already being tracked")
		return
	}

	tracked := &TrackedToken{
		Mint:         tokenData.Mint,
		CreatedAt:    time.Now(),
		InitialMC:    tokenData.MarketCapUSD,
		BondingCurve: tokenData.BondingCurve,
		User:         tokenData.User,
		LastChecked:  time.Now(),
	}

	tt.trackedTokens[mintStr] = tracked

	logrus.WithFields(logrus.Fields{
		"mint":        mintStr[:8] + "...",
		"initial_mc":  logger.FormatMarketCap(tracked.InitialMC),
		"tracking_for": "1 minute",
		"threshold":   logger.FormatMarketCap(tt.threshold),
	}).Info("👀 Started tracking new token for market cap threshold crossing")
}

// monitorTrackedTokens periodically checks tracked tokens for threshold crossings
func (tt *TokenTracker) monitorTrackedTokens(ctx context.Context) {
	ticker := time.NewTicker(tt.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logrus.Info("🛑 Token tracker monitor stopping")
			return
		case <-ticker.C:
			tt.checkTokenMarketCaps(ctx)
		}
	}
}

// checkTokenMarketCaps checks current market caps and triggers trades if threshold is crossed
func (tt *TokenTracker) checkTokenMarketCaps(ctx context.Context) {
	tt.mutex.Lock()
	tokensToCheck := make([]*TrackedToken, 0, len(tt.trackedTokens))
	for _, token := range tt.trackedTokens {
		tokensToCheck = append(tokensToCheck, token)
	}
	tt.mutex.Unlock()

	if len(tokensToCheck) == 0 {
		return
	}

	logrus.WithField("count", len(tokensToCheck)).Debug("🔍 Checking market caps for tracked tokens")

	for _, token := range tokensToCheck {
		select {
		case <-ctx.Done():
			return
		default:
			tt.checkSingleToken(ctx, token)
		}
	}
}

// checkSingleToken checks a single token's current market cap
func (tt *TokenTracker) checkSingleToken(ctx context.Context, token *TrackedToken) {
	// Create a mock buy transaction to get current market cap
	// This simulates fetching current bonding curve state
	currentMC := tt.estimateCurrentMarketCap(token)
	
	token.LastChecked = time.Now()

	logrus.WithFields(logrus.Fields{
		"mint":       token.Mint.String()[:8] + "...",
		"current_mc": logger.FormatMarketCap(currentMC),
		"threshold":  logger.FormatMarketCap(tt.threshold),
		"age":        time.Since(token.CreatedAt).Truncate(time.Second),
	}).Debug("📊 Checked token market cap")

	// Check if threshold is crossed
	if currentMC >= tt.threshold {
		tt.triggerTrade(ctx, token, currentMC)
	}
}

// estimateCurrentMarketCap estimates current market cap (in production, this would fetch real data)
func (tt *TokenTracker) estimateCurrentMarketCap(token *TrackedToken) float64 {
	// In production, this would:
	// 1. Fetch current bonding curve state from RPC
	// 2. Calculate market cap from current reserves
	// 3. Return accurate market cap
	
	// For now, simulate some market cap growth over time
	age := time.Since(token.CreatedAt)
	
	// Simulate various scenarios:
	// - 70% tokens stay below threshold
	// - 20% grow moderately  
	// - 10% cross threshold
	
	// Simple simulation based on hash of mint address
	mintStr := token.Mint.String()
	hash := 0
	for _, c := range mintStr {
		hash += int(c)
	}
	
	growthFactor := 1.0 + (float64(hash%100) / 100.0) * (age.Seconds() / 60.0) // Growth over 1 minute
	estimatedMC := token.InitialMC * growthFactor
	
	// Cap at reasonable values
	if estimatedMC > 50000 {
		estimatedMC = 50000
	}
	
	return estimatedMC
}

// triggerTrade sends a token to the trader when threshold is crossed
func (tt *TokenTracker) triggerTrade(ctx context.Context, token *TrackedToken, currentMC float64) {
	// Remove from tracking first to avoid duplicate trades
	tt.mutex.Lock()
	delete(tt.trackedTokens, token.Mint.String())
	tt.mutex.Unlock()

	// Create TokenLaunchData for the trader
	tradeData := &parser.TokenLaunchData{
		Mint:                 token.Mint,
		MarketCapUSD:         currentMC,
		BondingCurve:         token.BondingCurve,
		User:                 token.User,
		Signature:            "THRESHOLD_CROSSED_" + token.Mint.String()[:8],
		Timestamp:            time.Now().Unix(),
		InstructionType:      "buy", // Treat as buy for trading
		VirtualSolReserves:   50 * 1e9,
		VirtualTokenReserves: 800_000_000 * 1e6,
	}

	logrus.WithFields(logrus.Fields{
		"mint":        token.Mint.String()[:8] + "...",
		"market_cap":  logger.FormatMarketCap(currentMC),
		"threshold":   logger.FormatMarketCap(tt.threshold),
		"age":         time.Since(token.CreatedAt).Truncate(time.Second),
		"solscan":     "https://solscan.io/token/" + token.Mint.String(),
	}).Info("🚀 THRESHOLD CROSSED! Sending token to trader")

	// Send to trader channel
	select {
	case tt.tradeChannel <- tradeData:
		logrus.WithField("mint", token.Mint.String()[:8]+"...").Info("✅ Token sent to trader for threshold crossing")
	case <-time.After(100 * time.Millisecond):
		logrus.WithField("mint", token.Mint.String()[:8]+"...").Warn("⚠️  Trade channel busy, skipping threshold trade")
	case <-ctx.Done():
		return
	}
}

// cleanupExpiredTokens removes tokens that have been tracked for too long
func (tt *TokenTracker) cleanupExpiredTokens(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Cleanup every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logrus.Info("🛑 Token tracker cleanup stopping")
			return
		case <-ticker.C:
			tt.performCleanup()
		}
	}
}

// performCleanup removes expired tokens from tracking
func (tt *TokenTracker) performCleanup() {
	tt.mutex.Lock()
	defer tt.mutex.Unlock()

	now := time.Now()
	expiredCount := 0

	for mintStr, token := range tt.trackedTokens {
		if now.Sub(token.CreatedAt) > tt.trackDuration {
			delete(tt.trackedTokens, mintStr)
			expiredCount++
			
			logrus.WithFields(logrus.Fields{
				"mint": mintStr[:8] + "...",
				"age":  now.Sub(token.CreatedAt).Truncate(time.Second),
			}).Debug("⏰ Removed expired token from tracking")
		}
	}

	if expiredCount > 0 {
		logrus.WithFields(logrus.Fields{
			"expired":      expiredCount,
			"still_tracking": len(tt.trackedTokens),
		}).Info("🗑️  Cleaned up expired tokens")
	}
}

// GetStats returns current tracking statistics
func (tt *TokenTracker) GetStats() map[string]interface{} {
	tt.mutex.RLock()
	defer tt.mutex.RUnlock()

	return map[string]interface{}{
		"tracked_tokens": len(tt.trackedTokens),
		"threshold":      tt.threshold,
		"track_duration": tt.trackDuration.String(),
		"check_interval": tt.checkInterval.String(),
	}
}

// ProcessBuyTransaction processes a BUY transaction to check if it's for a tracked token
// and updates market cap or triggers trade if threshold is crossed
func (tt *TokenTracker) ProcessBuyTransaction(buyData *parser.TokenLaunchData) bool {
	if buyData.InstructionType != "buy" {
		return false
	}

	tt.mutex.Lock()
	defer tt.mutex.Unlock()

	mintStr := buyData.Mint.String()
	
	// Check if this token is being tracked
	trackedToken, exists := tt.trackedTokens[mintStr]
	if !exists {
		// Not tracking this token
		return false
	}

	// Update the tracked token with real market cap from the BUY transaction
	trackedToken.LastChecked = time.Now()
	currentMC := buyData.MarketCapUSD

	logrus.WithFields(logrus.Fields{
		"mint":       mintStr[:8] + "...",
		"current_mc": logger.FormatMarketCap(currentMC),
		"threshold":  logger.FormatMarketCap(tt.threshold),
		"age":        time.Since(trackedToken.CreatedAt).Truncate(time.Second),
		"sol_amount": fmt.Sprintf("%.3f SOL", float64(buyData.SolAmount)/1e9),
	}).Info("📈 BUY detected for tracked token - checking threshold")

	// Check if threshold is crossed
	if currentMC >= tt.threshold {
		// Remove from tracking to avoid duplicate trades
		delete(tt.trackedTokens, mintStr)
		
		// Send to trader
		tt.sendToTrader(trackedToken, buyData, currentMC)
		return true // Indicates token was sent to trader
	}

	logrus.WithFields(logrus.Fields{
		"mint":         mintStr[:8] + "...",
		"current_mc":   logger.FormatMarketCap(currentMC),
		"needed":       logger.FormatMarketCap(tt.threshold - currentMC),
		"progress":     fmt.Sprintf("%.1f%%", (currentMC/tt.threshold)*100),
	}).Info("💰 Still tracking - threshold not reached yet")

	return false // Token still being tracked
}

// sendToTrader sends a token to the trader when threshold is crossed
func (tt *TokenTracker) sendToTrader(trackedToken *TrackedToken, buyData *parser.TokenLaunchData, currentMC float64) {
	// Create updated TokenLaunchData for the trader with real market cap
	tradeData := &parser.TokenLaunchData{
		Mint:                 trackedToken.Mint,
		MarketCapUSD:         currentMC,
		BondingCurve:         trackedToken.BondingCurve,
		User:                 trackedToken.User,
		Signature:            buyData.Signature, // Use the actual triggering transaction
		Timestamp:            time.Now().Unix(),
		InstructionType:      "buy",
		VirtualSolReserves:   buyData.VirtualSolReserves,
		VirtualTokenReserves: buyData.VirtualTokenReserves,
		SolAmount:           buyData.SolAmount,
		TokenAmount:         buyData.TokenAmount,
	}

	logrus.WithFields(logrus.Fields{
		"mint":        trackedToken.Mint.String()[:8] + "...",
		"market_cap":  logger.FormatMarketCap(currentMC),
		"threshold":   logger.FormatMarketCap(tt.threshold),
		"age":         time.Since(trackedToken.CreatedAt).Truncate(time.Second),
		"trigger_tx":  "https://solscan.io/tx/" + buyData.Signature,
		"solscan":     "https://solscan.io/token/" + trackedToken.Mint.String(),
	}).Info("🚀 THRESHOLD CROSSED! Sending token to trader")

	// Send to trader channel
	select {
	case tt.tradeChannel <- tradeData:
		logrus.WithField("mint", trackedToken.Mint.String()[:8]+"...").Info("✅ Token sent to trader for threshold crossing")
	case <-time.After(100 * time.Millisecond):
		logrus.WithField("mint", trackedToken.Mint.String()[:8]+"...").Warn("⚠️  Trade channel busy, skipping threshold trade")
	}
}
