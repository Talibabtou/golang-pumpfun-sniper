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

type Trader struct {
	config     *config.Config
	rpcClient  *rpc.Client
	wallet     solana.PrivateKey
	publicKey  solana.PublicKey
	pumpFunProgram solana.PublicKey
	globalAccount  solana.PublicKey
	feeRecipient   solana.PublicKey
}

type TradeResult struct {
	Success     bool
	Signature   string
	Error       error
	TokenAmount uint64
	SolSpent    float64
	Timestamp   time.Time
}

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
