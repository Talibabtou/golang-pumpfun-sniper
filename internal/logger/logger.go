package logger

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/gagliardetto/solana-go"
)

func Setup(level string) {
	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		logrus.Warn("Invalid log level, defaulting to info")
		logLevel = logrus.InfoLevel
	}
	
	logrus.SetLevel(logLevel)
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
		ForceColors:     true,
		PadLevelText:    true,
	})
	logrus.SetOutput(os.Stdout)
}

func LogStartup(cfg interface{}) {
	logrus.Info("🤖 Starting Pump.Fun Sniper Bot")
	logrus.Info("⚡ Bot is optimized for low-latency trading")
	logrus.Info("🔍 Monitoring Pump.Fun program for new token launches...")
}

func FormatMarketCap(marketCap float64) string {
	if marketCap >= 1000000 {
		return fmt.Sprintf("$%.1fM", marketCap/1000000)
	} else if marketCap >= 1000 {
		return fmt.Sprintf("$%.1fK", marketCap/1000)
	} else {
		return fmt.Sprintf("$%.0f", marketCap)
	}
}

func LogTokenParsed(mint solana.PublicKey, marketCapUSD, solPrice float64) {
	logrus.WithFields(logrus.Fields{
		"mint":       mint.String()[:8] + "...",
		"market_cap": FormatMarketCap(marketCapUSD),
		"sol_price":  fmt.Sprintf("$%.2f", solPrice),
	}).Info("�� Token parsed")
}

func LogTokenSkipped(reason string) {
	logrus.WithField("reason", reason).Debug("⏭️  Token skipped")
}

func LogTokenEligible() {
	logrus.Info("🎯 Token eligible for trading")
}

func LogConnection(service string, status string) {
	if status == "connected" {
		logrus.WithField("service", service).Info("✅ Connected")
	} else {
		logrus.WithField("service", service).Warn("⚠️  Connection issue")
	}
}
