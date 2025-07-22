// Package monitor provides real-time monitoring of Solana blockchain transactions
// for the Pump.Fun program using Yellowstone gRPC streaming.
//
// This implementation uses proper Yellowstone gRPC protobuf definitions for
// real-time streaming with manual event decoding to avoid bundle compatibility issues.
package monitor

import (
	"context"
	"fmt"
	"time"

	"golang-pumpfun-sniper/internal/config"
	"golang-pumpfun-sniper/internal/logger"
	"golang-pumpfun-sniper/internal/parser"

	pb "github.com/rpcpool/yellowstone-grpc/examples/golang/proto"

	"github.com/gagliardetto/solana-go"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/keepalive"
)

// Monitor provides real-time monitoring using Yellowstone gRPC streaming
type Monitor struct {
	config         *config.Config
	grpcConn       *grpc.ClientConn
	geyserClient   pb.GeyserClient
	pumpFunProgram solana.PublicKey
	endpoint       string
	token          string
}

// tokenAuth implements gRPC credentials for Helius authentication
type tokenAuth struct {
	token string
}

func (t tokenAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"x-token": t.token,
	}, nil
}

func (t tokenAuth) RequireTransportSecurity() bool {
	return false
}

// NewMonitor creates a new Yellowstone gRPC monitor
func NewMonitor(cfg *config.Config) (*Monitor, error) {
	// Validate gRPC configuration
	if cfg.GRPCEndpoint == "" || cfg.GRPCToken == "" {
		return nil, fmt.Errorf("gRPC endpoint and token are required - no fallback available")
	}

	pumpFunProgram, err := solana.PublicKeyFromBase58(cfg.PumpFunProgramID)
	if err != nil {
		return nil, fmt.Errorf("invalid pump fun program ID: %w", err)
	}

	monitor := &Monitor{
		config:         cfg,
		pumpFunProgram: pumpFunProgram,
		endpoint:       cfg.GRPCEndpoint,
		token:          cfg.GRPCToken,
	}

	logrus.Info("✅ Yellowstone gRPC monitor ready")
	return monitor, nil
}

// Start begins Yellowstone gRPC streaming for Pump.Fun program transactions
func (m *Monitor) Start(ctx context.Context, rawTxChan chan<- *parser.RawTransaction) error {
	logrus.Info("🚀 Starting Yellowstone gRPC streaming for Pump.Fun events...")

	// Establish gRPC connection
	if err := m.connectYellowstone(ctx); err != nil {
		return fmt.Errorf("failed to connect to Yellowstone gRPC: %w", err)
	}

	logger.LogConnection("Yellowstone gRPC", "connected")

	// Start streaming Pump.Fun transactions
	return m.streamPumpFunTransactions(ctx, rawTxChan)
}

// connectYellowstone establishes authenticated gRPC connection to Yellowstone
func (m *Monitor) connectYellowstone(ctx context.Context) error {
	// Setup connection parameters for optimal performance
	kacp := keepalive.ClientParameters{
		Time:                10 * time.Second,
		Timeout:             5 * time.Second,
		PermitWithoutStream: true,
	}

	// Configure gRPC options
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(kacp),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(1024*1024*1024), // 1GB max message size
			grpc.UseCompressor(gzip.Name),
		),
		grpc.WithPerRPCCredentials(tokenAuth{token: m.token}),
	}

	// Establish connection
	conn, err := grpc.DialContext(ctx, m.endpoint, opts...)
	if err != nil {
		return fmt.Errorf("failed to dial Yellowstone gRPC: %w", err)
	}

	m.grpcConn = conn
	m.geyserClient = pb.NewGeyserClient(conn)
	
	logrus.Info("✅ Yellowstone gRPC connection established")
	return nil
}

// streamPumpFunTransactions subscribes to real-time Pump.Fun program transactions
func (m *Monitor) streamPumpFunTransactions(ctx context.Context, rawTxChan chan<- *parser.RawTransaction) error {
	logrus.Info("📡 Subscribing to Pump.Fun program transactions via Yellowstone...")

	// Create streaming subscription
	stream, err := m.geyserClient.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("failed to create subscription stream: %w", err)
	}

	// Build subscription request for Pump.Fun transactions
	req := m.buildPumpFunSubscriptionRequest()
	
	// Send subscription request
	if err := stream.Send(req); err != nil {
		return fmt.Errorf("failed to send subscription request: %w", err)
	}

	logrus.Info("⚡ Yellowstone streaming active - monitoring Pump.Fun transactions...")

	// Start receiving stream data
	return m.receiveStreamData(ctx, stream, rawTxChan)
}

// buildPumpFunSubscriptionRequest creates a subscription request for Pump.Fun transactions
func (m *Monitor) buildPumpFunSubscriptionRequest() *pb.SubscribeRequest {
	// Subscribe to transactions that include the Pump.Fun program
	transactions := make(map[string]*pb.SubscribeRequestFilterTransactions)
	transactions["pumpfun"] = &pb.SubscribeRequestFilterTransactions{
		Vote:           &[]bool{false}[0], // No vote transactions
		Failed:         &[]bool{false}[0], // No failed transactions  
		AccountInclude: []string{m.config.PumpFunProgramID}, // Only Pump.Fun program
	}

	// Set commitment level to confirmed for balance between speed and reliability
	commitment := pb.CommitmentLevel_CONFIRMED

	return &pb.SubscribeRequest{
		Transactions: transactions,
		Commitment:   &commitment,
	}
}

// receiveStreamData receives and processes streaming data from Yellowstone
func (m *Monitor) receiveStreamData(ctx context.Context, stream pb.Geyser_SubscribeClient, rawTxChan chan<- *parser.RawTransaction) error {
	for {
		select {
		case <-ctx.Done():
			logrus.Info("🛑 Yellowstone streaming stopping...")
			return nil
		default:
			// Receive update from stream
			update, err := stream.Recv()
			if err != nil {
				return fmt.Errorf("failed to receive stream update: %w", err)
			}

			// Process each transaction in the update
			if txUpdate := update.GetTransaction(); txUpdate != nil {
				if err := m.processTransactionUpdate(ctx, txUpdate, rawTxChan); err != nil {
					logrus.WithError(err).Warn("Failed to process transaction update")
				}
			}

			// Handle ping/pong to keep connection alive
			if ping := update.GetPing(); ping != nil {
				logrus.Debug("📡 Received ping from Yellowstone")
				// Send pong response
				pongReq := &pb.SubscribeRequest{
					Ping: &pb.SubscribeRequestPing{Id: 1},
				}
				if err := stream.Send(pongReq); err != nil {
					logrus.WithError(err).Warn("Failed to send pong response")
				}
			}
		}
	}
}

// processTransactionUpdate processes a single transaction update from Yellowstone
func (m *Monitor) processTransactionUpdate(ctx context.Context, txUpdate *pb.SubscribeUpdateTransaction, rawTxChan chan<- *parser.RawTransaction) error {
	if txUpdate == nil || txUpdate.Transaction == nil {
		return nil
	}

	tx := txUpdate.Transaction
	signature := m.extractSignatureFromUpdate(tx)
	
	if signature == "" {
		return fmt.Errorf("no signature found in transaction")
	}

	logrus.WithFields(logrus.Fields{
		"signature": signature[:8] + "...",
		"slot":      txUpdate.Slot,
		"is_vote":   tx.IsVote,
	}).Info("🎯 Pump.Fun transaction detected via Yellowstone")

	// Convert Yellowstone transaction to our format for manual parsing
	rawTx := &parser.RawTransaction{
		Signature:   signature,
		Transaction: m.convertYellowstoneTransaction(txUpdate),
	}

	// Send to parser
	select {
	case rawTxChan <- rawTx:
		logrus.WithField("signature", signature[:8]+"...").Debug("📤 Sent Yellowstone transaction to parser")
	case <-time.After(10 * time.Millisecond):
		logrus.Warn("🚫 Raw transaction channel full, dropping Yellowstone transaction")
	case <-ctx.Done():
		return nil
	}

	return nil
}

// extractSignatureFromUpdate extracts the signature from a Yellowstone transaction update
func (m *Monitor) extractSignatureFromUpdate(tx *pb.SubscribeUpdateTransactionInfo) string {
	if tx == nil || len(tx.Signature) == 0 {
		return ""
	}
	
	// Convert binary signature to base58 string manually
	// Solana signatures are 64 bytes, encoded as base58
	if len(tx.Signature) != 64 {
		logrus.Warn("Invalid signature length")
		return ""
	}
	
	// Create signature from bytes and convert to string
	var sigBytes [64]byte
	copy(sigBytes[:], tx.Signature)
	signature := solana.Signature(sigBytes)
	
	return signature.String()
}

// convertYellowstoneTransaction converts Yellowstone transaction format to parser format
// This allows us to reuse our existing manual parser without changes
func (m *Monitor) convertYellowstoneTransaction(txUpdate *pb.SubscribeUpdateTransaction) interface{} {
	// Pass the Yellowstone transaction directly to the parser
	// The parser will handle the conversion internally
	return txUpdate
}

// Close cleanup resources
func (m *Monitor) Close() error {
	if m.grpcConn != nil {
		logrus.Info("🔌 Closing Yellowstone gRPC connection")
		return m.grpcConn.Close()
	}
	return nil
}
