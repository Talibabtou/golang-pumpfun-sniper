package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang-pumpfun-sniper/internal/config"
	"golang-pumpfun-sniper/internal/monitor"

	"github.com/sirupsen/logrus"
)

func main() {
	// Parse command line flags
	simulate := flag.Bool("simulate", false, "Run in simulation mode (no actual trades)")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load configuration: %v", err)
	}

	// Set simulation mode if flag is provided
	cfg.SimulateMode = *simulate

	// Setup logging
	monitor.SetupLogger(cfg.LogLevel)

	// Initialize metrics
	metrics := monitor.NewMetrics()

	// Log startup with human-friendly format
	monitor.LogStartup(cfg)
	
	logrus.WithFields(logrus.Fields{
		"rpc_endpoint":     cfg.RPCEndpoint,
		"buy_amount":       cfg.BuyAmountSOL,
		"min_market_cap":   cfg.MinMarketCap,
		"max_slippage":     cfg.MaxSlippage,
		"simulate_mode":    cfg.SimulateMode,
	}).Info("🔧 Configuration loaded")

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the bot
	pumpMonitor, tokenChan, err := startBot(ctx, cfg, metrics)
	if err != nil {
		logrus.Fatalf("Failed to start bot: %v", err)
	}

	// Process tokens in main goroutine
	go processTokenLaunches(ctx, tokenChan, cfg, metrics)

	// Start metrics logger
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				metrics.LogMetrics()
			}
		}
	}()

	logrus.Info("🚀 Bot is now running and monitoring for new tokens")

	// Wait for shutdown signal
	<-sigChan
	logrus.Info("🛑 Received shutdown signal, stopping bot...")

	// Cancel context to stop all goroutines
	cancel()

	// Give goroutines time to cleanup
	time.Sleep(2 * time.Second)

	// Close connections
	if pumpMonitor != nil {
		pumpMonitor.Close()
	}

	// Log final metrics
	metrics.LogMetrics()
	logrus.Info("✅ Bot stopped successfully")
}

// startBot initializes and starts all bot components
func startBot(ctx context.Context, cfg *config.Config, metrics *monitor.Metrics) (*monitor.PumpFunMonitor, <-chan *monitor.TokenLaunch, error) {
	// Create Pump.Fun monitor
	pumpMonitor, err := monitor.NewPumpFunMonitor(cfg, metrics)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create pump.fun monitor: %w", err)
	}

	// Create token launch channel
	tokenChan := make(chan *monitor.TokenLaunch, 100)

	// Start monitoring
	if err := pumpMonitor.Start(ctx, tokenChan); err != nil {
		pumpMonitor.Close()
		return nil, nil, fmt.Errorf("failed to start monitoring: %w", err)
	}

	return pumpMonitor, tokenChan, nil
}

// processTokenLaunches processes incoming token launches
func processTokenLaunches(ctx context.Context, tokenChan <-chan *monitor.TokenLaunch, cfg *config.Config, metrics *monitor.Metrics) {
	for {
		select {
		case <-ctx.Done():
			logrus.Info("🛑 Stopping token processing")
			return
		case tokenLaunch := <-tokenChan:
			if tokenLaunch != nil {
				processTokenLaunch(tokenLaunch, cfg, metrics)
			}
		}
	}
}

// processTokenLaunch processes a single token launch
func processTokenLaunch(tokenLaunch *monitor.TokenLaunch, cfg *config.Config, metrics *monitor.Metrics) {
	monitor.LogNewToken(tokenLaunch.Mint, tokenLaunch.MarketCap)

	// Check if market cap meets our criteria
	if tokenLaunch.MarketCap < cfg.MinMarketCap {
		logrus.WithFields(logrus.Fields{
			"mint":           tokenLaunch.Mint,
			"market_cap":     tokenLaunch.MarketCap,
			"min_required":   cfg.MinMarketCap,
		}).Info("⏭️  Skipping token (market cap too low)")
		return
	}

	// This is where we would execute the trade
	if cfg.SimulateMode {
		logrus.WithFields(logrus.Fields{
			"mint":       tokenLaunch.Mint,
			"market_cap": tokenLaunch.MarketCap,
			"amount":     cfg.BuyAmountSOL,
		}).Info("📝 [SIMULATION] Would execute buy transaction")
		
		// Simulate success for metrics
		metrics.RecordSuccessfulTrade()
		monitor.LogTrade(tokenLaunch.Mint, cfg.BuyAmountSOL, true)
	} else {
		logrus.WithFields(logrus.Fields{
			"mint":       tokenLaunch.Mint,
			"market_cap": tokenLaunch.MarketCap,
		}).Info("🎯 Token meets criteria - ready to trade!")
		
		// TODO: Implement actual trading logic here
		// For now, just log that we would trade
		monitor.LogTrade(tokenLaunch.Mint, cfg.BuyAmountSOL, false)
	}
}
