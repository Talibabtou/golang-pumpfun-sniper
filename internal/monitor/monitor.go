// Package monitor provides real-time monitoring of Solana blockchain transactions
// for the Pump.Fun program. It establishes WebSocket connections to track program
// logs and fetches full transaction details for analysis.
//
// The monitor operates by:
// 1. Subscribing to Pump.Fun program log mentions via WebSocket
// 2. Fetching complete transaction data for each detected signature
// 3. Forwarding raw transactions to the parser pipeline
//
// It handles connection failures gracefully with automatic reconnection
// and provides detailed logging for monitoring and debugging.
package monitor

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"golang-pumpfun-sniper/internal/config"
	"golang-pumpfun-sniper/internal/logger"
	"golang-pumpfun-sniper/internal/parser"
	"golang-pumpfun-sniper/internal/utils"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
	"github.com/sirupsen/logrus"
)

// PumpFunMonitor manages real-time monitoring of Pump.Fun program transactions
// on the Solana blockchain. It maintains both RPC and WebSocket connections
// to efficiently track new token launches and related activities.
type PumpFunMonitor struct {
	config         *config.Config
	rpcClient      *rpc.Client
	wsClient       *ws.Client
	pumpFunProgram solana.PublicKey
}

// NewMonitor creates a new PumpFunMonitor instance with the provided configuration.
// It initializes the RPC client and validates the Pump.Fun program ID.
//
// Returns an error if the program ID is invalid or RPC client creation fails.
func NewMonitor(cfg *config.Config) (*PumpFunMonitor, error) {
	pumpFunProgram, err := solana.PublicKeyFromBase58(cfg.PumpFunProgramID)
	if err != nil {
		return nil, err
	}

	rpcClient := rpc.New(cfg.RPCEndpoint)

	return &PumpFunMonitor{
		config:         cfg,
		rpcClient:      rpcClient,
		pumpFunProgram: pumpFunProgram,
	}, nil
}

// Start begins the monitoring process by establishing connections and subscribing
// to Pump.Fun program logs. It first validates RPC connectivity, then establishes
// a WebSocket connection for real-time log monitoring.
//
// The method runs continuously until the context is cancelled, automatically
// handling connection failures with retry logic.
func (m *PumpFunMonitor) Start(ctx context.Context, rawTxChan chan<- *parser.RawTransaction) error {
	logrus.Info("🔌 Connecting to Solana RPC...")

	_, err := m.rpcClient.GetHealth(ctx)
	if err != nil {
		return err
	}
	logger.LogConnection("Solana RPC", "connected")

	if err := m.connectWebSocket(ctx); err != nil {
		return err
	}

	return m.monitorLogs(ctx, rawTxChan)
}

// connectWebSocket establishes a WebSocket connection to the Solana RPC endpoint
// for real-time transaction monitoring. It converts HTTP(S) endpoints to their
// WebSocket equivalents automatically.
func (m *PumpFunMonitor) connectWebSocket(ctx context.Context) error {
	logrus.Info("🔌 Connecting to Solana WebSocket...")
	
	wsEndpoint := convertHTTPToWebSocket(m.config.RPCEndpoint)
	wsClient, err := ws.Connect(ctx, wsEndpoint)
	if err != nil {
		return err
	}

	m.wsClient = wsClient
	logger.LogConnection("Solana WebSocket", "connected")
	return nil
}

// monitorLogs continuously monitors Pump.Fun program logs through WebSocket
// subscription. It implements automatic reconnection on failures with a
// 5-second delay between retry attempts.
//
// The method runs until the context is cancelled or an unrecoverable error occurs.
func (m *PumpFunMonitor) monitorLogs(ctx context.Context, rawTxChan chan<- *parser.RawTransaction) error {
	logrus.Info("📡 Subscribing to Pump.Fun program logs...")

	for {
		select {
		case <-ctx.Done():
			logrus.Info("🛑 Context cancelled, stopping log monitoring")
			return nil
		default:
			if err := m.subscribeAndListen(ctx, rawTxChan); err != nil {
				logrus.WithError(err).Error("Subscription failed, retrying in 5 seconds...")
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(5 * time.Second):
					continue
				}
			}
		}
	}
}

// subscribeAndListen establishes a log subscription for the Pump.Fun program
// and continuously listens for new transaction signatures. Each received
// signature triggers a full transaction fetch and forwarding to the parser.
//
// The subscription uses the "processed" commitment level for fastest detection
// of new transactions.
func (m *PumpFunMonitor) subscribeAndListen(ctx context.Context, rawTxChan chan<- *parser.RawTransaction) error {
	sub, err := m.wsClient.LogsSubscribeMentions(m.pumpFunProgram, rpc.CommitmentProcessed)
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	logrus.Info("✅ Successfully subscribed to Pump.Fun logs")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			result, err := sub.Recv(ctx)
			if err != nil {
				select {
				case <-ctx.Done():
					return nil
				default:
					return err
				}
			}

			m.sendRawTransaction(ctx, result.Value.Signature, rawTxChan)
		}
	}
}

// sendRawTransaction fetches the complete transaction data for a given signature
// and forwards it to the parser pipeline. It uses the "confirmed" commitment
// level to ensure transaction data stability while maintaining low latency.
//
// Failed transaction fetches are logged but don't stop the monitoring process.
// The channel send is non-blocking to prevent pipeline stalls.
func (m *PumpFunMonitor) sendRawTransaction(ctx context.Context, signature solana.Signature, rawTxChan chan<- *parser.RawTransaction) {
	logrus.WithField("signature", signature.String()[:8]+"...").Info("📥 Processing Pump.Fun transaction")
	
	version := uint64(0)
	tx, err := m.rpcClient.GetTransaction(ctx, signature, &rpc.GetTransactionOpts{
		MaxSupportedTransactionVersion: &version,
		Commitment:                     rpc.CommitmentConfirmed,
		Encoding:                       solana.EncodingBase64,
	})
	
	if err != nil {
		sanitizedError := utils.SanitizeError(err, m.config.RPCEndpoint)
		logrus.WithFields(logrus.Fields{
			"signature": signature.String()[:8] + "...",
			"error":     sanitizedError,
		}).Warn("⚠️ Failed to get transaction")
		return
	}
	
	if tx == nil {
		logrus.Info("Transaction is nil, skipping")
		return
	}

	_, err = json.Marshal([]*rpc.GetTransactionResult{tx})
	if err != nil {
		logrus.WithError(err).Info("Failed to marshal transaction")
		return
	}

	rawTx := &parser.RawTransaction{
		Signature:   signature.String(),
		Transaction: tx,
	}

	select {
	case rawTxChan <- rawTx:
		logrus.WithField("signature", signature.String()[:8]+"...").Debug("📤 Sent to parser")
	case <-ctx.Done():
		return
	default:
		logrus.Warn("🚫 Raw transaction channel full, dropping transaction")
	}
}

// Close gracefully shuts down the monitor by closing the WebSocket connection.
// It should be called when the monitor is no longer needed to free resources.
func (m *PumpFunMonitor) Close() error {
	if m.wsClient != nil {
		logrus.Info("🔌 Closing WebSocket connection")
		m.wsClient.Close()
	}
	return nil
}

// convertHTTPToWebSocket converts HTTP(S) RPC endpoints to their WebSocket
// equivalents by replacing the protocol scheme. This enables real-time
// data streaming from the same endpoint used for RPC calls.
//
// Examples:
//   - "https://api.mainnet-beta.solana.com" -> "wss://api.mainnet-beta.solana.com"
//   - "http://localhost:8899" -> "ws://localhost:8899"
func convertHTTPToWebSocket(httpEndpoint string) string {
	if strings.HasPrefix(httpEndpoint, "https://") {
		return "wss://" + httpEndpoint[8:]
	} else if strings.HasPrefix(httpEndpoint, "http://") {
		return "ws://" + httpEndpoint[7:]
	}
	return httpEndpoint
}
