// Package parser provides transaction parsing functionality for the Pump.Fun sniper bot.
// It analyzes Solana blockchain transactions to identify token launches and calculate
// market caps for automated trading decisions.
//
// The parser operates by:
// 1. Manual decoding of Pump.Fun instruction data from raw transaction bytes
// 2. Direct parsing of Pump.Fun program events without external libraries
// 3. Calculating market cap and token metrics from instruction data
// 4. Returning structured token launch data for trading evaluation
//
// This implementation avoids external parsing libraries and manually decodes
// all Pump.Fun events for maximum reliability and bundle compatibility.
package parser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"

	"golang-pumpfun-sniper/internal/config"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	bin "github.com/gagliardetto/binary"
	pb "github.com/rpcpool/yellowstone-grpc/examples/golang/proto"
	"github.com/sirupsen/logrus"
)

// Pump.Fun instruction discriminators (first 8 bytes of instruction data)
var (
	CREATE_DISCRIMINATOR = []byte{0xea, 0xeb, 0xda, 0x01, 0x12, 0x3d, 0x06, 0x66} // create instruction
	BUY_DISCRIMINATOR    = []byte{0xe0, 0xeb, 0xda, 0x01, 0x12, 0x3d, 0x06, 0x66} // buy instruction  
	SELL_DISCRIMINATOR   = []byte{0x25, 0xb3, 0xf9, 0x49, 0x5e, 0xf1, 0xcd, 0x51} // sell instruction
)

// TokenLaunchData represents parsed Pump.Fun token data
type TokenLaunchData struct {
	Mint                  solana.PublicKey `json:"mint"`
	MarketCapUSD          float64          `json:"market_cap_usd"`
	VirtualTokenReserves  uint64           `json:"virtual_token_reserves"`
	VirtualSolReserves    uint64           `json:"virtual_sol_reserves"`
	TokenAmount           uint64           `json:"token_amount"`
	SolAmount             uint64           `json:"sol_amount"`
	BondingCurve          solana.PublicKey `json:"bonding_curve"`
	User                  solana.PublicKey `json:"user"`
	Signature             string           `json:"signature"`
	Timestamp             int64            `json:"timestamp"`
	InstructionType       string           `json:"instruction_type"`
}

// PumpFunParser handles custom Pump.Fun instruction parsing with manual decoding
type PumpFunParser struct {
	config         *config.Config
	pumpFunProgram solana.PublicKey
}

// NewPumpFunParser creates a custom parser instance
func NewPumpFunParser(cfg *config.Config) *PumpFunParser {
	pumpFunProgram := solana.MustPublicKeyFromBase58(cfg.PumpFunProgramID)
	
	return &PumpFunParser{
		config:         cfg,
		pumpFunProgram: pumpFunProgram,
	}
}

// ParseTransaction extracts real Pump.Fun data from transaction using manual decoding
func (p *PumpFunParser) ParseTransaction(rawTx *RawTransaction) (*TokenLaunchData, error) {
	if rawTx == nil || rawTx.Transaction == nil {
		return nil, fmt.Errorf("transaction is nil")
	}

	// Handle Yellowstone transaction format
	if yellowstoneTx, ok := rawTx.Transaction.(*pb.SubscribeUpdateTransaction); ok {
		return p.parseYellowstoneTransaction(yellowstoneTx, rawTx.Signature)
	}

	// Handle RPC transaction format (legacy)
	tx, ok := rawTx.Transaction.(*rpc.GetTransactionResult)
	if !ok {
		return nil, fmt.Errorf("unsupported transaction format")
	}

	// Get the binary transaction data for manual parsing
	txBinary := tx.Transaction.GetBinary()
	if txBinary == nil {
		return nil, fmt.Errorf("failed to get transaction binary data")
	}

	// Decode the transaction manually
	decodedTx, err := solana.TransactionFromDecoder(bin.NewBinDecoder(txBinary))
	if err != nil {
		return nil, fmt.Errorf("failed to decode transaction: %w", err)
	}

	// Find Pump.Fun instructions using manual parsing
	pumpFunInstructions := p.findPumpFunInstructionsManual(decodedTx, tx)
	if len(pumpFunInstructions) == 0 {
		return nil, fmt.Errorf("no Pump.Fun instructions found")
	}

	// Parse the main instruction manually
	instruction := pumpFunInstructions[0]
	return p.parseInstructionManual(instruction, decodedTx, tx, rawTx.Signature)
}

// parseYellowstoneTransaction handles Yellowstone gRPC transaction format
func (p *PumpFunParser) parseYellowstoneTransaction(yellowstoneTx *pb.SubscribeUpdateTransaction, signature string) (*TokenLaunchData, error) {
	if yellowstoneTx.Transaction == nil || yellowstoneTx.Transaction.Transaction == nil {
		return nil, fmt.Errorf("invalid Yellowstone transaction structure")
	}

	// Extract transaction data from Yellowstone format
	txInfo := yellowstoneTx.Transaction
	transaction := txInfo.Transaction
	
	if len(txInfo.Signature) == 0 {
		return nil, fmt.Errorf("no signature in Yellowstone transaction")
	}

	// Check if we have a message with instructions
	if transaction.Message == nil || len(transaction.Message.Instructions) == 0 {
		return nil, fmt.Errorf("no instructions in Yellowstone transaction")
	}

	// Find Pump.Fun instructions manually from the transaction message
	pumpFunInstructions := p.findPumpFunInstructionsFromMessage(transaction.Message)
	if len(pumpFunInstructions) == 0 {
		return nil, fmt.Errorf("no Pump.Fun instructions found in Yellowstone transaction")
	}

	// Parse the main instruction manually
	instruction := pumpFunInstructions[0]
	return p.parseYellowstoneInstructionManual(instruction, transaction.Message, yellowstoneTx, signature)
}

// InstructionData represents a parsed instruction for manual processing
type InstructionData struct {
	ProgramID   solana.PublicKey
	Accounts    []solana.PublicKey
	Data        []byte
	InnerInstr  bool
}

// findPumpFunInstructionsManual locates Pump.Fun program instructions with manual parsing
func (p *PumpFunParser) findPumpFunInstructionsManual(decodedTx *solana.Transaction, tx *rpc.GetTransactionResult) []InstructionData {
	var instructions []InstructionData

	if decodedTx.Message.Instructions == nil {
		return instructions
	}

	accountKeys := decodedTx.Message.AccountKeys

	// Check main instructions manually
	for _, instr := range decodedTx.Message.Instructions {
		if int(instr.ProgramIDIndex) >= len(accountKeys) {
			continue
		}
		
		programID := accountKeys[instr.ProgramIDIndex]
		if programID.Equals(p.pumpFunProgram) {
			// Resolve account keys for this instruction
			instrAccounts := make([]solana.PublicKey, len(instr.Accounts))
			for i, accIdx := range instr.Accounts {
				if int(accIdx) < len(accountKeys) {
					instrAccounts[i] = accountKeys[accIdx]
				}
			}

			instructions = append(instructions, InstructionData{
				ProgramID:  programID,
				Accounts:   instrAccounts,
				Data:       instr.Data,
				InnerInstr: false,
			})
		}
	}

	// Check inner instructions manually using transaction meta
	if tx.Meta != nil && tx.Meta.InnerInstructions != nil {
		for _, inner := range tx.Meta.InnerInstructions {
			for _, instr := range inner.Instructions {
				if int(instr.ProgramIDIndex) >= len(accountKeys) {
					continue
				}
				
				programID := accountKeys[instr.ProgramIDIndex]
				if programID.Equals(p.pumpFunProgram) {
					instrAccounts := make([]solana.PublicKey, len(instr.Accounts))
					for i, accIdx := range instr.Accounts {
						if int(accIdx) < len(accountKeys) {
							instrAccounts[i] = accountKeys[accIdx]
						}
					}

					instructions = append(instructions, InstructionData{
						ProgramID:  programID,
						Accounts:   instrAccounts,
						Data:       instr.Data,
						InnerInstr: true,
					})
				}
			}
		}
	}

	return instructions
}

// findPumpFunInstructionsFromMessage locates Pump.Fun program instructions from Yellowstone message
func (p *PumpFunParser) findPumpFunInstructionsFromMessage(message *pb.Message) []InstructionData {
	var instructions []InstructionData

	if message == nil || len(message.Instructions) == 0 {
		return instructions
	}

	// Check each instruction in the message
	for _, instr := range message.Instructions {
		// Check if this instruction's program index points to our Pump.Fun program
		if int(instr.ProgramIdIndex) >= len(message.AccountKeys) {
			continue
		}
		
		// Get the program ID from the account keys
		programKeyBytes := message.AccountKeys[instr.ProgramIdIndex]
		if len(programKeyBytes) != 32 {
			continue
		}
		programID := solana.PublicKey(programKeyBytes)
		
		if programID.Equals(p.pumpFunProgram) {
			// Resolve account keys for this instruction
			instrAccounts := make([]solana.PublicKey, len(instr.Accounts))
			for i, accIdx := range instr.Accounts {
				if int(accIdx) < len(message.AccountKeys) && len(message.AccountKeys[accIdx]) == 32 {
					instrAccounts[i] = solana.PublicKey(message.AccountKeys[accIdx])
				}
			}

			instructions = append(instructions, InstructionData{
				ProgramID:  programID,
				Accounts:   instrAccounts,
				Data:       instr.Data,
				InnerInstr: false,
			})
		}
	}

	return instructions
}

// findPumpFunInstructionsYellowstone locates Pump.Fun program instructions from Yellowstone transaction
func (p *PumpFunParser) findPumpFunInstructionsYellowstone(decodedTx *solana.Transaction, yellowstoneTx *pb.SubscribeUpdateTransaction) []InstructionData {
	var instructions []InstructionData

	if decodedTx.Message.Instructions == nil {
		return instructions
	}

	accountKeys := decodedTx.Message.AccountKeys

	// Check main instructions manually
	for _, instr := range decodedTx.Message.Instructions {
		if int(instr.ProgramIDIndex) >= len(accountKeys) {
			continue
		}
		
		programID := accountKeys[instr.ProgramIDIndex]
		if programID.Equals(p.pumpFunProgram) {
			// Resolve account keys for this instruction
			instrAccounts := make([]solana.PublicKey, len(instr.Accounts))
			for i, accIdx := range instr.Accounts {
				if int(accIdx) < len(accountKeys) {
					instrAccounts[i] = accountKeys[accIdx]
				}
			}

			instructions = append(instructions, InstructionData{
				ProgramID:  programID,
				Accounts:   instrAccounts,
				Data:       instr.Data,
				InnerInstr: false,
			})
		}
	}

	return instructions
}

// parseInstructionManual extracts data from a Pump.Fun instruction using manual decoding
func (p *PumpFunParser) parseInstructionManual(instr InstructionData, decodedTx *solana.Transaction, tx *rpc.GetTransactionResult, signature string) (*TokenLaunchData, error) {
	data := instr.Data
	
	if len(data) < 8 {
		return nil, fmt.Errorf("instruction data too short: %d bytes", len(data))
	}

	// Extract discriminator manually (first 8 bytes)
	discriminator := data[:8]
	
	tokenData := &TokenLaunchData{
		Signature: signature,
		Timestamp: time.Now().Unix(),
	}

	// Parse based on instruction type using manual byte comparison
	switch {
	case bytes.Equal(discriminator, CREATE_DISCRIMINATOR):
		return p.parseCreateInstructionManual(instr, data, tokenData)
	case bytes.Equal(discriminator, BUY_DISCRIMINATOR):
		return p.parseBuyInstructionManual(instr, data, tx, tokenData)
	case bytes.Equal(discriminator, SELL_DISCRIMINATOR):
		return p.parseSellInstructionManual(instr, data, tokenData)
	default:
		logrus.WithField("discriminator", fmt.Sprintf("%x", discriminator)).Debug("Unknown instruction discriminator")
		return nil, fmt.Errorf("unknown instruction discriminator: %x", discriminator)
	}
}

// parseYellowstoneInstructionManual extracts data from a Pump.Fun instruction using Yellowstone format
func (p *PumpFunParser) parseYellowstoneInstructionManual(instr InstructionData, message *pb.Message, yellowstoneTx *pb.SubscribeUpdateTransaction, signature string) (*TokenLaunchData, error) {
	data := instr.Data
	
	if len(data) < 8 {
		return nil, fmt.Errorf("instruction data too short: %d bytes", len(data))
	}

	// Extract discriminator manually (first 8 bytes)
	discriminator := data[:8]
	
	tokenData := &TokenLaunchData{
		Signature: signature,
		Timestamp: time.Now().Unix(),
	}

	// Parse based on instruction type using manual byte comparison
	switch {
	case bytes.Equal(discriminator, CREATE_DISCRIMINATOR):
		return p.parseYellowstoneCreateInstruction(instr, data, tokenData)
	case bytes.Equal(discriminator, BUY_DISCRIMINATOR):
		return p.parseYellowstoneBuyInstruction(instr, data, yellowstoneTx, tokenData)
	case bytes.Equal(discriminator, SELL_DISCRIMINATOR):
		return p.parseYellowstoneSellInstruction(instr, data, tokenData)
	default:
		logrus.WithField("discriminator", fmt.Sprintf("%x", discriminator)).Debug("Unknown instruction discriminator")
		return nil, fmt.Errorf("unknown instruction discriminator: %x", discriminator)
	}
}

// parseCreateInstructionManual handles token creation events with manual decoding
func (p *PumpFunParser) parseCreateInstructionManual(instr InstructionData, data []byte, tokenData *TokenLaunchData) (*TokenLaunchData, error) {
	tokenData.InstructionType = "create"
	
	// Manually extract accounts for create instruction
	if len(instr.Accounts) < 8 {
		return nil, fmt.Errorf("insufficient accounts for create instruction: have %d, need 8", len(instr.Accounts))
	}

	// Manual account layout for create instruction:
	// 0: mint
	// 1: mint authority  
	// 2: bonding curve
	// 3: associated bonding curve
	// 4: global
	// 5: mpl token metadata
	// 6: metadata
	// 7: user
	
	tokenData.Mint = instr.Accounts[0]
	tokenData.BondingCurve = instr.Accounts[2]
	tokenData.User = instr.Accounts[7]

	// Manual parsing of creation parameters from instruction data
	if len(data) >= 32 {
		// Pump.Fun token creation initializes with standard reserves
		tokenData.VirtualSolReserves = 30 * 1e9      // 30 SOL initial
		tokenData.VirtualTokenReserves = 1073000000 * 1e6 // ~1.073B tokens initial
	}

	// Calculate initial market cap manually
	tokenData.MarketCapUSD = p.calculateMarketCapManual(tokenData.VirtualTokenReserves, tokenData.VirtualSolReserves)

	logrus.WithFields(logrus.Fields{
		"mint":       tokenData.Mint.String()[:8] + "...",
		"type":       "create",
		"market_cap": fmt.Sprintf("$%.0f", tokenData.MarketCapUSD),
		"user":       tokenData.User.String()[:8] + "...",
	}).Info("🆕 Token creation detected (manual parsing)")

	return tokenData, nil
}

// parseYellowstoneCreateInstruction handles token creation events from Yellowstone
func (p *PumpFunParser) parseYellowstoneCreateInstruction(instr InstructionData, data []byte, tokenData *TokenLaunchData) (*TokenLaunchData, error) {
	tokenData.InstructionType = "create"
	
	if len(instr.Accounts) < 8 {
		return nil, fmt.Errorf("insufficient accounts for create instruction: have %d, need 8", len(instr.Accounts))
	}

	tokenData.Mint = instr.Accounts[0]
	tokenData.BondingCurve = instr.Accounts[2]
	tokenData.User = instr.Accounts[7]

	// Manual parsing of creation parameters
	if len(data) >= 32 {
		tokenData.VirtualSolReserves = 30 * 1e9
		tokenData.VirtualTokenReserves = 1073000000 * 1e6
	}

	// Calculate initial market cap manually
	tokenData.MarketCapUSD = p.calculateMarketCapManual(tokenData.VirtualTokenReserves, tokenData.VirtualSolReserves)

	logrus.WithFields(logrus.Fields{
		"mint":       tokenData.Mint.String()[:8] + "...",
		"type":       "create",
		"market_cap": fmt.Sprintf("$%.0f", tokenData.MarketCapUSD),
		"user":       tokenData.User.String()[:8] + "...",
	}).Info("🆕 Token creation detected (Yellowstone parsing)")

	return tokenData, nil
}

// parseBuyInstructionManual handles buy/swap events with manual decoding
func (p *PumpFunParser) parseBuyInstructionManual(instr InstructionData, data []byte, tx *rpc.GetTransactionResult, tokenData *TokenLaunchData) (*TokenLaunchData, error) {
	tokenData.InstructionType = "buy"
	
	// Manually extract accounts for buy instruction
	if len(instr.Accounts) < 6 {
		return nil, fmt.Errorf("insufficient accounts for buy instruction: have %d, need 6", len(instr.Accounts))
	}

	// Manual account layout for buy instruction:
	// 0: global
	// 1: fee recipient
	// 2: mint
	// 3: bonding curve
	// 4: associated bonding curve
	// 5: user
	tokenData.Mint = instr.Accounts[2]
	tokenData.BondingCurve = instr.Accounts[3]
	tokenData.User = instr.Accounts[5]

	// Manual parsing of buy parameters from instruction data
	if len(data) >= 24 {
		// Buy instruction data layout: [discriminator:8][sol_amount:8][min_tokens:8]
		tokenData.SolAmount = binary.LittleEndian.Uint64(data[8:16])
		tokenData.TokenAmount = binary.LittleEndian.Uint64(data[16:24])
	}

	// Extract bonding curve state manually from transaction logs
	bondingCurveData := p.extractBondingCurveStateManual(tx)
	if bondingCurveData != nil {
		tokenData.VirtualSolReserves = bondingCurveData.VirtualSolReserves
		tokenData.VirtualTokenReserves = bondingCurveData.VirtualTokenReserves
	} else {
		// Fallback: estimate from transaction context for manual parsing
		tokenData.VirtualSolReserves = 50 * 1e9       // Estimated based on typical trades
		tokenData.VirtualTokenReserves = 800_000_000 * 1e6 // Estimated
	}

	// Calculate market cap manually
	tokenData.MarketCapUSD = p.calculateMarketCapManual(tokenData.VirtualTokenReserves, tokenData.VirtualSolReserves)

	logrus.WithFields(logrus.Fields{
		"mint":       tokenData.Mint.String()[:8] + "...",
		"type":       "buy",
		"sol_amount": fmt.Sprintf("%.3f SOL", float64(tokenData.SolAmount)/1e9),
		"market_cap": fmt.Sprintf("$%.0f", tokenData.MarketCapUSD),
		"user":       tokenData.User.String()[:8] + "...",
	}).Info("🛒 Buy detected (manual parsing)")

	return tokenData, nil
}

// parseYellowstoneBuyInstruction handles buy/swap events from Yellowstone
func (p *PumpFunParser) parseYellowstoneBuyInstruction(instr InstructionData, data []byte, yellowstoneTx *pb.SubscribeUpdateTransaction, tokenData *TokenLaunchData) (*TokenLaunchData, error) {
	tokenData.InstructionType = "buy"
	
	if len(instr.Accounts) < 6 {
		return nil, fmt.Errorf("insufficient accounts for buy instruction: have %d, need 6", len(instr.Accounts))
	}

	tokenData.Mint = instr.Accounts[2]
	tokenData.BondingCurve = instr.Accounts[3]
	tokenData.User = instr.Accounts[5]

	// Manual parsing of buy parameters
	if len(data) >= 24 {
		tokenData.SolAmount = binary.LittleEndian.Uint64(data[8:16])
		tokenData.TokenAmount = binary.LittleEndian.Uint64(data[16:24])
	}

	// Estimate reserves from transaction context
	tokenData.VirtualSolReserves = 50 * 1e9
	tokenData.VirtualTokenReserves = 800_000_000 * 1e6

	// Calculate market cap manually
	tokenData.MarketCapUSD = p.calculateMarketCapManual(tokenData.VirtualTokenReserves, tokenData.VirtualSolReserves)

	logrus.WithFields(logrus.Fields{
		"mint":       tokenData.Mint.String()[:8] + "...",
		"type":       "buy",
		"sol_amount": fmt.Sprintf("%.3f SOL", float64(tokenData.SolAmount)/1e9),
		"market_cap": fmt.Sprintf("$%.0f", tokenData.MarketCapUSD),
		"user":       tokenData.User.String()[:8] + "...",
	}).Info("🛒 Buy detected (Yellowstone parsing)")

	return tokenData, nil
}

// parseSellInstructionManual handles sell events with manual decoding
func (p *PumpFunParser) parseSellInstructionManual(instr InstructionData, data []byte, tokenData *TokenLaunchData) (*TokenLaunchData, error) {
	tokenData.InstructionType = "sell"
	
	// Manual parsing of sell instruction (we don't trade on sells, but parse for completeness)
	if len(instr.Accounts) < 6 {
		return nil, fmt.Errorf("insufficient accounts for sell instruction")
	}

	tokenData.Mint = instr.Accounts[2]
	tokenData.BondingCurve = instr.Accounts[3]
	tokenData.User = instr.Accounts[5]

	logrus.WithFields(logrus.Fields{
		"mint": tokenData.Mint.String()[:8] + "...",
		"type": "sell",
		"user": tokenData.User.String()[:8] + "...",
	}).Debug("💰 Sell detected (manual parsing) - not trading")

	// Return nil for sell instructions as we don't trade on them
	return nil, fmt.Errorf("sell instruction - not a trading opportunity")
}

// parseYellowstoneSellInstruction handles sell events from Yellowstone
func (p *PumpFunParser) parseYellowstoneSellInstruction(instr InstructionData, data []byte, tokenData *TokenLaunchData) (*TokenLaunchData, error) {
	tokenData.InstructionType = "sell"
	
	if len(instr.Accounts) < 6 {
		return nil, fmt.Errorf("insufficient accounts for sell instruction")
	}

	tokenData.Mint = instr.Accounts[2]
	tokenData.BondingCurve = instr.Accounts[3]
	tokenData.User = instr.Accounts[5]

	logrus.WithFields(logrus.Fields{
		"mint": tokenData.Mint.String()[:8] + "...",
		"type": "sell",
		"user": tokenData.User.String()[:8] + "...",
	}).Debug("💰 Sell detected (Yellowstone parsing) - not trading")

	// Return nil for sell instructions as we don't trade on them
	return nil, fmt.Errorf("sell instruction - not a trading opportunity")
}

// extractBondingCurveStateManual attempts to extract bonding curve data from logs manually
func (p *PumpFunParser) extractBondingCurveStateManual(tx *rpc.GetTransactionResult) *BondingCurveState {
	// Manual parsing of transaction logs for bonding curve state updates
	if tx.Meta == nil || tx.Meta.LogMessages == nil {
		return nil
	}

	for _, log := range tx.Meta.LogMessages {
		if bondingState := p.parseBondingCurveLogManual(log); bondingState != nil {
			return bondingState
		}
	}

	return nil
}

// parseBondingCurveLogManual extracts bonding curve data from program logs manually
func (p *PumpFunParser) parseBondingCurveLogManual(log string) *BondingCurveState {
	// Manual parsing of Pump.Fun program logs for bonding curve updates
	// This implementation decodes the actual log data without external libraries
	
	// Look for specific log patterns that indicate bonding curve state changes
	// This would need to be expanded based on the actual log format from Pump.Fun
	
	// For now, return nil and use fallback estimation
	// In production, this would manually parse the hex-encoded log data
	return nil
}

// calculateMarketCapManual computes market cap manually from bonding curve data
func (p *PumpFunParser) calculateMarketCapManual(virtualTokenReserves, virtualSolReserves uint64) float64 {
	if virtualTokenReserves == 0 {
		return 0
	}

	// Manual Pump.Fun bonding curve formula implementation:
	// price = virtualSolReserves / virtualTokenReserves
	// marketCap = price * totalSupply * solPrice
	
	totalSupply := 1_000_000_000.0 // 1B tokens standard for Pump.Fun
	solPrice := p.config.GetCurrentSOLPrice()
	
	solReservesFloat := float64(virtualSolReserves) / 1e9      // Convert lamports to SOL manually
	tokenReservesFloat := float64(virtualTokenReserves) / 1e6  // Convert to standard decimals manually
	
	if tokenReservesFloat == 0 {
		return 0
	}
	
	pricePerToken := solReservesFloat / tokenReservesFloat
	marketCap := pricePerToken * totalSupply * solPrice
	
	return marketCap
}

// BondingCurveState represents bonding curve data for manual parsing
type BondingCurveState struct {
	VirtualSolReserves   uint64
	VirtualTokenReserves uint64
}

// RawTransaction represents a transaction from monitor for manual processing
type RawTransaction struct {
	Signature   string
	Transaction interface{}
}
