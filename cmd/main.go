// Package main implements a Pump.Fun sniper bot that monitors Solana blockchain
// for new token launches and executes automated trades based on configurable criteria.
//
// The bot operates in three main stages:
// 1. Monitor: Subscribes to Pump.Fun program logs via WebSocket
// 2. Parser: Analyzes transactions to identify token launches and calculate market caps
// 3. Trader: Executes buy orders for tokens meeting minimum market cap requirements
//
// Usage:
//   go run cmd/main.go                    # Live trading mode
//   go run cmd/main.go --simulate         # Simulation mode
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

// main is the entry point of the Pump.Fun sniper bot.
// It handles command-line flags, configuration loading, logging setup,
// and orchestrates the startup of all bot components.
func main() {
	simulate := flag.Bool("simulate", false, "Simulation mode (no real trades)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}
	cfg.SimulateMode = *simulate

	logger.Setup(cfg.LogLevel)
	
	if cfg.SimulateMode {
		logrus.Info("🧪 Starting Pump.Fun Sniper Bot in SIMULATION MODE")
		logrus.Info("📊 Bot will monitor and parse token launches but NOT execute real trades")
	} else {
		logrus.Info("🤖 Starting Pump.Fun Sniper Bot in LIVE TRADING MODE")
		logrus.Info("⚡ Bot will execute REAL trades on qualifying tokens")
		logrus.Info("💰 Minimum market cap: $8,000")
	}
	
	logrus.Info("🔍 Monitoring Pump.Fun program for new token launches...")

	ctx, cancel := context.WithCancel(context.Background())

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logrus.Info("🛑 Shutdown signal received, stopping gracefully...")
		cancel()
	}()

	if err := cfg.PriceService.Start(ctx); err != nil {
		logrus.Fatalf("Failed to start price service: %v", err)
	}

	if err := startSniper(ctx, cfg); err != nil {
		logrus.Fatalf("Failed to start sniper: %v", err)
	}

	<-ctx.Done()
	logrus.Info("✅ All services stopped, shutting down...")
	
	time.Sleep(2 * time.Second)
	logrus.Info("✅ Shutdown complete")
}

// startSniper initializes and starts the main sniper pipeline components.
// It creates communication channels between monitor, parser, and trader,
// then starts each component in separate goroutines.
//
// The pipeline flow:
// Monitor (WebSocket) -> RawTransaction -> Parser -> TokenLaunchData -> Trader
//
// Returns an error if any component fails to initialize.
func startSniper(ctx context.Context, cfg *config.Config) error {
	rawTxChan := make(chan *parser.RawTransaction, 500)
	tokenChan := make(chan *parser.TokenLaunchData, 100)

	pumpMonitor, err := monitor.NewMonitor(cfg)
	if err != nil {
		return fmt.Errorf("failed to create monitor: %w", err)
	}
	defer pumpMonitor.Close()

	go func() {
		if err := pumpMonitor.Start(ctx, rawTxChan); err != nil {
			logrus.WithError(err).Error("Monitor failed")
		}
	}()

	pumpParser := parser.NewPumpFunParser(cfg)
	go startParser(ctx, rawTxChan, tokenChan, pumpParser, cfg)

	go startTrader(ctx, tokenChan, cfg)

	logrus.Info("🚀 Sniper pipeline started: Monitor → Parser → Trader")
	return nil
}

// startParser processes raw transactions from the monitor and extracts
// token launch data for qualifying Pump.Fun transactions.
//
// It continuously reads from rawTxChan, parses each transaction,
// and forwards valid token launches to tokenChan for trading evaluation.
//
// The parser tracks transaction count and provides detailed logging
// for debugging and monitoring purposes.
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

// startTrader evaluates parsed token launches and executes buy orders
// for tokens that meet the configured minimum market cap requirements.
//
// In simulation mode, it logs what trades would be executed without
// actually performing them. In live mode, it executes real trades
// and reports success/failure with detailed metrics.
//
// The trader automatically falls back to simulation mode if trader
// initialization fails, ensuring the bot continues monitoring.
func startTrader(ctx context.Context, tokenChan <-chan *parser.TokenLaunchData, cfg *config.Config) {
	var traderInstance *trader.Trader
	var err error
	
	if !cfg.SimulateMode {
		traderInstance, err = trader.NewTrader(cfg)
		if err != nil {
			logrus.WithError(err).Error("Failed to create trader - running in monitor-only mode")
			cfg.SimulateMode = true
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
			
			if tokenLaunch.MarketCapUSD < cfg.MinMarketCap {
				logrus.WithFields(logrus.Fields{
					"mint":         tokenLaunch.Mint[:8] + "...",
					"market_cap":   logger.FormatMarketCap(tokenLaunch.MarketCapUSD),
					"min_required": logger.FormatMarketCap(cfg.MinMarketCap),
				}).Debug("⏭️  Token skipped: market cap too low")
				continue
			}

			logrus.WithFields(logrus.Fields{
				"mint":       tokenLaunch.Mint[:8] + "...",
				"market_cap": logger.FormatMarketCap(tokenLaunch.MarketCapUSD),
				"sol_price":  fmt.Sprintf("$%.2f", cfg.GetCurrentSOLPrice()),
			}).Info("🎯 Token eligible for trading!")

			if cfg.SimulateMode {
				logrus.WithFields(logrus.Fields{
					"mint":       tokenLaunch.Mint[:8] + "...",
					"market_cap": logger.FormatMarketCap(tokenLaunch.MarketCapUSD),
					"would_buy":  fmt.Sprintf("%.3f SOL", cfg.BuyAmountSOL),
				}).Info("📝 [SIMULATION] Would execute buy order")
			} else {
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
