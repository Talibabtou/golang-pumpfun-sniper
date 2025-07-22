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

type PumpFunMonitor struct {
	config         *config.Config
	rpcClient      *rpc.Client
	wsClient       *ws.Client
	pumpFunProgram solana.PublicKey
}

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

func (m *PumpFunMonitor) Close() error {
	if m.wsClient != nil {
		logrus.Info("🔌 Closing WebSocket connection")
		m.wsClient.Close()
	}
	return nil
}

func convertHTTPToWebSocket(httpEndpoint string) string {
	if strings.HasPrefix(httpEndpoint, "https://") {
		return "wss://" + httpEndpoint[8:]
	} else if strings.HasPrefix(httpEndpoint, "http://") {
		return "ws://" + httpEndpoint[7:]
	}
	return httpEndpoint
}
