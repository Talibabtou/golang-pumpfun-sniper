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
	"golang-pumpfun-sniper/internal/logger"
	"golang-pumpfun-sniper/internal/monitor"
	"golang-pumpfun-sniper/internal/parser"
	"golang-pumpfun-sniper/internal/trader"

	"github.com/sirupsen/logrus"
)

func main() {
	// Parse flags
	simulate := flag.Bool("simulate", false, "Simulation mode (no real trades)")
	flag.Parse()

	// Load config
	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}
	cfg.SimulateMode = *simulate

	// Setup logging
	logger.Setup(cfg.LogLevel)
	
	// Enhanced startup message
	if cfg.SimulateMode {
		logrus.Info("🧪 Starting Pump.Fun Sniper Bot in SIMULATION MODE")
		logrus.Info("📊 Bot will monitor and parse token launches but NOT execute real trades")
	} else {
		logrus.Info("🤖 Starting Pump.Fun Sniper Bot in LIVE TRADING MODE")
		logrus.Info("⚡ Bot will execute REAL trades on qualifying tokens")
		logrus.Info("💰 Minimum market cap: $8,000")
	}
	
	logrus.Info("🔍 Monitoring Pump.Fun program for new token launches...")

	// Create context
	ctx, cancel := context.WithCancel(context.Background())

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logrus.Info("🛑 Shutdown signal received, stopping gracefully...")
		cancel()
	}()

	// Start price service FIRST
	if err := cfg.PriceService.Start(ctx); err != nil {
		logrus.Fatalf("Failed to start price service: %v", err)
	}

	// Start the sniper pipeline
	if err := startSniper(ctx, cfg); err != nil {
		logrus.Fatalf("Failed to start sniper: %v", err)
	}

	<-ctx.Done()
	logrus.Info("✅ All services stopped, shutting down...")
	
	time.Sleep(2 * time.Second)
	logrus.Info("✅ Shutdown complete")
}

func startSniper(ctx context.Context, cfg *config.Config) error {
	// Increase channel buffers for high-volume trading
	rawTxChan := make(chan *parser.RawTransaction, 500)
	tokenChan := make(chan *parser.TokenLaunchData, 100)

	// Start monitor
	pumpMonitor, err := monitor.NewMonitor(cfg)
	if err != nil {
		return fmt.Errorf("failed to create monitor: %w", err)
	}
	defer pumpMonitor.Close()

	// Start price service
	if err := cfg.PriceService.Start(ctx); err != nil {
		return fmt.Errorf("failed to start price service: %w", err)
	}

	// Start monitor
	go func() {
		if err := pumpMonitor.Start(ctx, rawTxChan); err != nil {
			logrus.WithError(err).Error("Monitor failed")
		}
	}()

	// Start parser
	pumpParser := parser.NewPumpFunParser(cfg)
	go startParser(ctx, rawTxChan, tokenChan, pumpParser, cfg)

	// Start trader
	go startTrader(ctx, tokenChan, cfg)

	logrus.Info("🚀 Sniper pipeline started: Monitor → Parser → Trader")
	return nil
}

func startParser(ctx context.Context, rawTxChan <-chan *parser.RawTransaction, tokenChan chan<- *parser.TokenLaunchData, pumpParser *parser.PumpFunParser, cfg *config.Config) {
	defer close(tokenChan)
	
	transactionCount := 0
	
	for {
		select {
		case <-ctx.Done():
			logrus.Info("🛑 Parser stopping gracefully")
			return
		case rawTx, ok := <-rawTxChan:
			if !ok {
				logrus.Info("🛑 Parser input channel closed")
				return
			}
			
			transactionCount++
			logrus.WithFields(logrus.Fields{
				"signature": rawTx.Signature[:8] + "...",
				"count":     transactionCount,
			}).Info("🔍 Parsing transaction")
			
			tokenLaunch, err := pumpParser.ParseTransaction(rawTx)
			if err != nil {
				logrus.WithFields(logrus.Fields{
					"signature": rawTx.Signature[:8] + "...",
					"error":     err.Error(),
				}).Debug("Failed to parse transaction")
				continue
			}

			if tokenLaunch == nil {
				logrus.WithField("signature", rawTx.Signature[:8]+"...").Debug("❌ Not a relevant Pump.Fun transaction")
				continue
			}

			logrus.WithFields(logrus.Fields{
				"signature":  rawTx.Signature[:8] + "...",
				"mint":       tokenLaunch.Mint[:8] + "...",
				"market_cap": logger.FormatMarketCap(tokenLaunch.MarketCapUSD),
			}).Info("✅ Token successfully parsed!")

			// Send to trader
			select {
			case tokenChan <- tokenLaunch:
				logger.LogTokenParsed(tokenLaunch.Mint, tokenLaunch.MarketCapUSD, cfg.GetCurrentSOLPrice())
			case <-time.After(10 * time.Millisecond):
				logrus.Info("⚠️  Token channel busy, skipping token")
			case <-ctx.Done():
				return
			}
		}
	}
}

func startTrader(ctx context.Context, tokenChan <-chan *parser.TokenLaunchData, cfg *config.Config) {
	// Only create trader instance if not in simulation mode
	var traderInstance *trader.Trader
	var err error
	
	if !cfg.SimulateMode {
		traderInstance, err = trader.NewTrader(cfg)
		if err != nil {
			logrus.WithError(err).Error("Failed to create trader - running in monitor-only mode")
			cfg.SimulateMode = true // Fallback to simulation
		}
	}

	for {
		select {
		case <-ctx.Done():
			logrus.Info("🛑 Trader stopping gracefully")
			return
		case tokenLaunch, ok := <-tokenChan:
			if !ok {
				logrus.Info("🛑 Trader input channel closed")
				return
			}
			
			// Check if it meets criteria
			if tokenLaunch.MarketCapUSD < cfg.MinMarketCap {
				logrus.WithFields(logrus.Fields{
					"mint":         tokenLaunch.Mint[:8] + "...",
					"market_cap":   logger.FormatMarketCap(tokenLaunch.MarketCapUSD),
					"min_required": logger.FormatMarketCap(cfg.MinMarketCap),
				}).Debug("⏭️  Token skipped: market cap too low")
				continue
			}

			// Log eligible token
			logrus.WithFields(logrus.Fields{
				"mint":       tokenLaunch.Mint[:8] + "...",
				"market_cap": logger.FormatMarketCap(tokenLaunch.MarketCapUSD),
				"sol_price":  fmt.Sprintf("$%.2f", cfg.GetCurrentSOLPrice()),
			}).Info("🎯 Token eligible for trading!")

			if cfg.SimulateMode {
				// Simulation mode: just log what would happen
				logrus.WithFields(logrus.Fields{
					"mint":       tokenLaunch.Mint[:8] + "...",
					"market_cap": logger.FormatMarketCap(tokenLaunch.MarketCapUSD),
					"would_buy":  fmt.Sprintf("%.3f SOL", cfg.BuyAmountSOL),
				}).Info("📝 [SIMULATION] Would execute buy order")
			} else {
				// Real trading mode: execute the trade
				result := traderInstance.ExecuteBuy(ctx, tokenLaunch)
				
				if result.Success {
					logrus.WithFields(logrus.Fields{
						"mint":       tokenLaunch.Mint[:8] + "...",
						"signature":  result.Signature,
						"market_cap": logger.FormatMarketCap(tokenLaunch.MarketCapUSD),
						"latency":    fmt.Sprintf("%dms", time.Since(result.Timestamp).Milliseconds()),
					}).Info("✅ Trade completed successfully!")
				} else {
					logrus.WithFields(logrus.Fields{
						"mint":       tokenLaunch.Mint[:8] + "...",
						"market_cap": logger.FormatMarketCap(tokenLaunch.MarketCapUSD),
						"error":      result.Error,
					}).Error("❌ Trade failed")
				}
			}
		}
	}
}
