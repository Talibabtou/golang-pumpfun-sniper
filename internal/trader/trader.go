// Package trader implements the trading component of the Pump.Fun sniper bot.
// It handles the execution of buy orders for qualifying token launches on Solana
// through the Pump.Fun program, with support for both live trading and simulation modes.
//
// The trader performs the following key functions:
// - Validates wallet setup and balance requirements
// - Constructs and signs Pump.Fun swap transactions
// - Executes real trades or simulates them based on configuration
// - Tracks transaction confirmations and provides detailed logging
//
// In simulation mode, trades are logged but not executed, allowing for safe testing.
// In live mode, real SOL is spent to purchase tokens that meet market cap criteria.
package trader

import (
	"context"
	"fmt"
	"time"

	"golang-pumpfun-sniper/internal/config"
	"golang-pumpfun-sniper/internal/logger"
	"golang-pumpfun-sniper/internal/parser"
	"golang-pumpfun-sniper/internal/utils"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/sirupsen/logrus"
)

// Trader handles the execution of token purchases on Pump.Fun.
// It manages wallet interactions, transaction building, and trade execution
// with support for both live trading and simulation modes.
type Trader struct {
	config     *config.Config
	rpcClient  *rpc.Client
	wallet     solana.PrivateKey
	publicKey  solana.PublicKey
	pumpFunProgram solana.PublicKey
	globalAccount  solana.PublicKey
	feeRecipient   solana.PublicKey
}

// TradeResult contains the outcome and metrics of a trade execution.
// It provides detailed information about transaction success, timing,
// and amounts for logging and monitoring purposes.
type TradeResult struct {
	Success     bool
	Signature   string
	Error       error
	TokenAmount uint64
	SolSpent    float64
	Timestamp   time.Time
}

// NewTrader creates and initializes a new Trader instance.
// It validates all required configuration parameters, establishes RPC connection,
// and verifies wallet setup including balance requirements.
//
// Returns an error if any validation fails, ensuring the trader is ready
// for immediate use upon successful creation.
func NewTrader(cfg *config.Config) (*Trader, error) {
	privateKey, err := solana.PrivateKeyFromBase58(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	pumpFunProgram, err := solana.PublicKeyFromBase58(cfg.PumpFunProgramID)
	if err != nil {
		return nil, fmt.Errorf("invalid pump fun program ID: %w", err)
	}

	globalAccount, err := solana.PublicKeyFromBase58(cfg.GlobalAccount)
	if err != nil {
		return nil, fmt.Errorf("invalid global account: %w", err)
	}

	feeRecipient, err := solana.PublicKeyFromBase58(cfg.FeeRecipient)
	if err != nil {
		return nil, fmt.Errorf("invalid fee recipient: %w", err)
	}

	rpcClient := rpc.New(cfg.RPCEndpoint)

	trader := &Trader{
		config:         cfg,
		rpcClient:      rpcClient,
		wallet:         privateKey,
		publicKey:      privateKey.PublicKey(),
		pumpFunProgram: pumpFunProgram,
		globalAccount:  globalAccount,
		feeRecipient:   feeRecipient,
	}

	if err := trader.validateSetup(context.Background()); err != nil {
		return nil, err
	}

	return trader, nil
}

// validateSetup verifies the trader's configuration and readiness for trading.
// It checks RPC connectivity, wallet balance, and ensures sufficient funds
// are available for the configured buy amount plus transaction fees.
func (t *Trader) validateSetup(ctx context.Context) error {
	_, err := t.rpcClient.GetHealth(ctx)
	if err != nil {
		sanitizedError := utils.SanitizeError(err, t.config.RPCEndpoint)
		return fmt.Errorf("RPC connection failed: %s", sanitizedError)
	}

	balance, err := t.rpcClient.GetBalance(ctx, t.publicKey, rpc.CommitmentConfirmed)
	if err != nil {
		sanitizedError := utils.SanitizeError(err, t.config.RPCEndpoint)
		return fmt.Errorf("failed to get wallet balance: %s", sanitizedError)
	}

	solBalance := float64(balance.Value) / 1e9
	
	logrus.WithFields(logrus.Fields{
		"wallet":     utils.SanitizeWalletAddress(t.publicKey.String()),
		"balance":    fmt.Sprintf("%.3f SOL", solBalance),
		"buy_amount": fmt.Sprintf("%.3f SOL", t.config.BuyAmountSOL),
	}).Info("💼 Trader wallet ready")

	requiredBalance := t.config.BuyAmountSOL + 0.01
	if solBalance < requiredBalance {
		return fmt.Errorf("insufficient balance: have %.3f SOL, need %.3f SOL", solBalance, requiredBalance)
	}

	return nil
}

// ExecuteBuy executes a buy order for the specified token launch.
// In simulation mode, it logs the trade without executing it.
// In live mode, it builds and sends a real Pump.Fun swap transaction.
//
// Returns a TradeResult containing execution details, timing metrics,
// and success/failure status for monitoring and logging.
func (t *Trader) ExecuteBuy(ctx context.Context, tokenLaunch *parser.TokenLaunchData) *TradeResult {
	startTime := time.Now()
	
	result := &TradeResult{
		Timestamp: startTime,
		SolSpent:  t.config.BuyAmountSOL,
	}

	logrus.WithFields(logrus.Fields{
		"mint":       tokenLaunch.Mint[:8] + "...",
		"market_cap": logger.FormatMarketCap(tokenLaunch.MarketCapUSD),
		"amount":     fmt.Sprintf("%.3f SOL", t.config.BuyAmountSOL),
		"sol_price":  fmt.Sprintf("$%.2f", t.config.GetCurrentSOLPrice()),
	}).Info("🚀 Executing REAL Pump.Fun buy order")

	if t.config.SimulateMode {
		return t.simulateBuy(tokenLaunch, result)
	}

	return t.executeRealPumpFunBuy(ctx, tokenLaunch, result)
}

// simulateBuy simulates a buy order without executing a real transaction.
// It provides realistic timing and logs the trade details as if it were
// executed, useful for testing and strategy development.
func (t *Trader) simulateBuy(tokenLaunch *parser.TokenLaunchData, result *TradeResult) *TradeResult {
	time.Sleep(50 * time.Millisecond)

	result.Success = true
	result.Signature = "SIMULATION_" + tokenLaunch.Mint[:8]
	result.TokenAmount = uint64(t.config.BuyAmountSOL * 1000000)

	logrus.WithFields(logrus.Fields{
		"mint":      tokenLaunch.Mint[:8] + "...",
		"signature": result.Signature,
		"tokens":    result.TokenAmount,
		"latency":   time.Since(result.Timestamp).Milliseconds(),
	}).Info("📝 [SIMULATION] Buy completed")

	return result
}

// executeRealPumpFunBuy executes a real buy transaction on the Pump.Fun program.
// It builds a complete swap transaction with associated token account creation,
// signs it with the trader's wallet, and submits it to the Solana network.
func (t *Trader) executeRealPumpFunBuy(ctx context.Context, tokenLaunch *parser.TokenLaunchData, result *TradeResult) *TradeResult {
	mintPubkey, err := solana.PublicKeyFromBase58(tokenLaunch.Mint)
	if err != nil {
		result.Error = fmt.Errorf("invalid mint address: %w", err)
		return result
	}

	tx, bondingCurve, err := t.buildRealPumpFunSwap(ctx, mintPubkey, tokenLaunch)
	if err != nil {
		result.Error = fmt.Errorf("failed to build Pump.Fun transaction: %w", err)
		return result
	}

	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(t.publicKey) {
			return &t.wallet
		}
		return nil
	})
	if err != nil {
		result.Error = fmt.Errorf("failed to sign transaction: %w", err)
		return result
	}

	signature, err := t.rpcClient.SendTransactionWithOpts(
		ctx,
		tx,
		rpc.TransactionOpts{
			SkipPreflight:       false,
			PreflightCommitment: rpc.CommitmentProcessed,
		},
	)
	if err != nil {
		result.Error = fmt.Errorf("failed to send Pump.Fun transaction: %w", err)
		return result
	}

	result.Success = true
	result.Signature = signature.String()

	logrus.WithFields(logrus.Fields{
		"mint":         tokenLaunch.Mint[:8] + "...",
		"signature":    signature.String()[:8] + "...",
		"bonding_curve": bondingCurve.String()[:8] + "...",
		"url":          "https://solscan.io/tx/" + signature.String(),
		"latency":      time.Since(result.Timestamp).Milliseconds(),
		"sol_price":    fmt.Sprintf("$%.2f", t.config.GetCurrentSOLPrice()),
	}).Info("🎉 [LIVE] REAL Pump.Fun swap executed!")

	go t.trackConfirmation(ctx, signature)

	return result
}

// buildRealPumpFunSwap constructs a complete Pump.Fun swap transaction.
// It creates instructions for associated token account creation and the swap itself,
// derives the bonding curve PDA, and builds a ready-to-sign transaction.
func (t *Trader) buildRealPumpFunSwap(ctx context.Context, mintPubkey solana.PublicKey, tokenLaunch *parser.TokenLaunchData) (*solana.Transaction, solana.PublicKey, error) {
	recentBlockhash, err := t.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return nil, solana.PublicKey{}, fmt.Errorf("failed to get recent blockhash: %w", err)
	}

	solAmountLamports := uint64(t.config.BuyAmountSOL * 1e9)

	ata, _, err := solana.FindAssociatedTokenAddress(t.publicKey, mintPubkey)
	if err != nil {
		return nil, solana.PublicKey{}, fmt.Errorf("failed to find ATA: %w", err)
	}

	bondingCurve, err := t.findBondingCurveAccount(mintPubkey)
	if err != nil {
		return nil, solana.PublicKey{}, fmt.Errorf("failed to find bonding curve: %w", err)
	}

	instructions := []solana.Instruction{}

	createATAInstruction := associatedtokenaccount.NewCreateInstruction(
		t.publicKey, // payer
		t.publicKey, // wallet
		mintPubkey,  // mint
	).Build()
	
	instructions = append(instructions, createATAInstruction)

	pumpFunSwapInstruction := &solana.GenericInstruction{
		AccountValues: solana.AccountMetaSlice{
			{PublicKey: t.globalAccount, IsWritable: false, IsSigner: false},
			{PublicKey: t.feeRecipient, IsWritable: true, IsSigner: false},
			{PublicKey: mintPubkey, IsWritable: false, IsSigner: false},
			{PublicKey: bondingCurve, IsWritable: true, IsSigner: false},
			{PublicKey: ata, IsWritable: true, IsSigner: false},
			{PublicKey: t.publicKey, IsWritable: true, IsSigner: true},
			{PublicKey: solana.SystemProgramID, IsWritable: false, IsSigner: false},
			{PublicKey: solana.TokenProgramID, IsWritable: false, IsSigner: false},
			{PublicKey: solana.SysVarRentPubkey, IsWritable: false, IsSigner: false},
		},
		ProgID: t.pumpFunProgram,
		DataBytes: t.buildSwapInstructionData(solAmountLamports, 0), // 0 min tokens for now
	}

	instructions = append(instructions, pumpFunSwapInstruction)

	tx, err := solana.NewTransaction(
		instructions,
		recentBlockhash.Value.Blockhash,
		solana.TransactionPayer(t.publicKey),
	)
	if err != nil {
		return nil, solana.PublicKey{}, fmt.Errorf("failed to create transaction: %w", err)
	}

	return tx, bondingCurve, nil
}

// findBondingCurveAccount derives the bonding curve Program Derived Address (PDA)
// for the specified token mint. This PDA is required for Pump.Fun swap transactions
// and follows the program's standard derivation pattern.
func (t *Trader) findBondingCurveAccount(mint solana.PublicKey) (solana.PublicKey, error) {
	seeds := [][]byte{
		[]byte("bonding-curve"),
		mint.Bytes(),
	}
	
	bondingCurve, _, err := solana.FindProgramAddress(seeds, t.pumpFunProgram)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("failed to derive bonding curve PDA: %w", err)
	}
	
	return bondingCurve, nil
}

// buildSwapInstructionData creates the instruction data for a Pump.Fun swap.
// It encodes the buy instruction discriminator, SOL amount, and minimum token
// amount into the binary format expected by the Pump.Fun program.
func (t *Trader) buildSwapInstructionData(solAmount, minTokens uint64) []byte {
	data := make([]byte, 24)
	
	discriminator := t.config.BuyInstructionDiscriminator
	for i := 0; i < 8; i++ {
		data[i] = byte(discriminator >> (8 * i))
	}
	
	for i := 0; i < 8; i++ {
		data[8+i] = byte(solAmount >> (8 * i))
	}
	
	for i := 0; i < 8; i++ {
		data[16+i] = byte(minTokens >> (8 * i))
	}
	
	return data
}

// trackConfirmation monitors a submitted transaction for confirmation.
// It polls the RPC for transaction status and logs updates until confirmed
// or timeout. Runs asynchronously to avoid blocking trade execution.
func (t *Trader) trackConfirmation(ctx context.Context, signature solana.Signature) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	
	timeout := time.After(30 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			logrus.WithField("signature", signature.String()[:8]+"...").Warn("⏰ Transaction confirmation timeout")
			return
		case <-ticker.C:
			statuses, err := t.rpcClient.GetSignatureStatuses(ctx, true, signature)
			if err != nil {
				continue
			}

			if len(statuses.Value) > 0 && statuses.Value[0] != nil {
				status := statuses.Value[0]
				if status.Err != nil {
					logrus.WithFields(logrus.Fields{
						"signature": signature.String()[:8] + "...",
						"error":     status.Err,
					}).Error("❌ Pump.Fun transaction failed")
					return
				}

				if status.ConfirmationStatus != "" {
					logrus.WithFields(logrus.Fields{
						"signature": signature.String()[:8] + "...",
						"status":    string(status.ConfirmationStatus),
						"slot":      status.Slot,
					}).Info("✅ Pump.Fun transaction confirmed!")
					return
				}
			}
		}
	}
}

// GetStats returns current trader statistics and configuration.
// Provides sanitized information suitable for logging and monitoring,
// excluding sensitive data like private keys.
func (t *Trader) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"wallet":           utils.SanitizeWalletAddress(t.publicKey.String()),
		"buy_amount":       t.config.BuyAmountSOL,
		"max_slippage":     t.config.MaxSlippage,
		"simulate_mode":    t.config.SimulateMode,
		"pump_fun_program": t.config.PumpFunProgramID,
		"current_sol_price": fmt.Sprintf("$%.2f", t.config.GetCurrentSOLPrice()),
	}
}
