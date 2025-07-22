package monitor

import (
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

// SetupLogger configures the global logger
func SetupLogger(level string) {
	// Parse log level
	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		logrus.Warn("Invalid log level, defaulting to info")
		logLevel = logrus.InfoLevel
	}
	
	logrus.SetLevel(logLevel)
	
	// Use text formatter for better readability during development
	// In production, you might want to switch to JSON formatter
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
		ForceColors:     true,
		PadLevelText:    true,
	})
	
	// Set output to stdout
	logrus.SetOutput(os.Stdout)
}

// Metrics holds performance metrics
type Metrics struct {
	TransactionsProcessed int64
	SuccessfulTrades     int64
	FailedTrades         int64
	TotalLatencyMS       int64
	StartTime            time.Time
}

// NewMetrics creates a new metrics instance
func NewMetrics() *Metrics {
	return &Metrics{
		StartTime: time.Now(),
	}
}

// RecordTransaction records a processed transaction
func (m *Metrics) RecordTransaction() {
	m.TransactionsProcessed++
}

// RecordSuccessfulTrade records a successful trade
func (m *Metrics) RecordSuccessfulTrade() {
	m.SuccessfulTrades++
}

// RecordFailedTrade records a failed trade
func (m *Metrics) RecordFailedTrade() {
	m.FailedTrades++
}

// RecordLatency records processing latency
func (m *Metrics) RecordLatency(latencyMS int64) {
	m.TotalLatencyMS += latencyMS
}

// GetAverageLatency returns average latency per transaction
func (m *Metrics) GetAverageLatency() float64 {
	if m.TransactionsProcessed == 0 {
		return 0
	}
	return float64(m.TotalLatencyMS) / float64(m.TransactionsProcessed)
}

// GetUptime returns the uptime duration
func (m *Metrics) GetUptime() time.Duration {
	return time.Since(m.StartTime)
}

// GetSuccessRate returns the success rate as a percentage
func (m *Metrics) GetSuccessRate() float64 {
	total := m.SuccessfulTrades + m.FailedTrades
	if total == 0 {
		return 0
	}
	return float64(m.SuccessfulTrades) / float64(total) * 100
}

// LogMetrics logs current metrics in a human-readable format
func (m *Metrics) LogMetrics() {
	logrus.WithFields(logrus.Fields{
		"transactions": m.TransactionsProcessed,
		"successful":   m.SuccessfulTrades,
		"failed":      m.FailedTrades,
		"avg_latency": m.GetAverageLatency(),
		"uptime":      m.GetUptime().Truncate(time.Second),
		"success_rate": m.GetSuccessRate(),
	}).Info("📊 Performance Metrics")
}

// LogStartup logs a human-friendly startup message
func LogStartup(cfg interface{}) {
	logrus.Info("🤖 Starting Pump.Fun Sniper Bot")
	logrus.Info("⚡ Bot is optimized for low-latency trading")
	logrus.Info("🔍 Monitoring Pump.Fun program for new token launches...")
}

// LogNewToken logs when a new token is detected
func LogNewToken(mint string, marketCap float64) {
	logrus.WithFields(logrus.Fields{
		"mint":       mint,
		"market_cap": marketCap,
		"url":        "https://solscan.io/token/" + mint,
	}).Info("🆕 New token detected")
}

// LogTrade logs trade attempts
func LogTrade(mint string, amount float64, success bool) {
	if success {
		logrus.WithFields(logrus.Fields{
			"mint":   mint,
			"amount": amount,
		}).Info("🚀 Trade executed successfully")
	} else {
		logrus.WithFields(logrus.Fields{
			"mint":   mint,
			"amount": amount,
		}).Error("❌ Trade failed")
	}
}

// LogConnection logs connection status
func LogConnection(service string, status string) {
	if status == "connected" {
		logrus.WithField("service", service).Info("✅ Connected")
	} else {
		logrus.WithField("service", service).Warn("⚠️  Connection issue")
	}
}
