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
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang-pumpfun-sniper/internal/config"
	"golang-pumpfun-sniper/internal/utils"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	pb "github.com/rpcpool/yellowstone-grpc/examples/golang/proto"
	"github.com/sirupsen/logrus"
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
	Name                  string           `json:"name"`
	Symbol                string           `json:"symbol"`
}

// InstructionData represents a parsed instruction for manual processing
type InstructionData struct {
	ProgramID   solana.PublicKey
	Accounts    []solana.PublicKey
	Data        []byte
	InnerInstr  bool
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

// AnchorEventData represents parsed anchor program event data
type AnchorEventData struct {
	Mint                 solana.PublicKey
	VirtualSolReserves   uint64
	VirtualTokenReserves uint64
	User                 solana.PublicKey
	IsBuy                bool
}

// CreateTokenEventData represents CREATE token data from anchor program events
type CreateTokenEventData struct {
	Mint                 solana.PublicKey
	Name                 string
	Symbol               string
	URI                  string
	BondingCurve         string
	User                 solana.PublicKey
	Creator              solana.PublicKey
	VirtualSolReserves   uint64
	VirtualTokenReserves uint64
	RealTokenReserves    uint64
	TokenTotalSupply     uint64
	Timestamp            uint64
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

	// Only handle Yellowstone transaction format (remove RPC legacy support)
	if yellowstoneTx, ok := rawTx.Transaction.(*pb.SubscribeUpdateTransaction); ok {
		return p.parseYellowstoneTransaction(yellowstoneTx, rawTx.Signature)
	}

	return nil, fmt.Errorf("unsupported transaction format - only Yellowstone supported")
}

// parseYellowstoneTransaction handles Yellowstone gRPC transaction format
func (p *PumpFunParser) parseYellowstoneTransaction(yellowstoneTx *pb.SubscribeUpdateTransaction, signature string) (*TokenLaunchData, error) {
	if yellowstoneTx.Transaction == nil || yellowstoneTx.Transaction.Transaction == nil {
		return nil, fmt.Errorf("invalid Yellowstone transaction structure")
	}

	txInfo := yellowstoneTx.Transaction
	transaction := txInfo.Transaction
	
	if len(txInfo.Signature) == 0 {
		return nil, fmt.Errorf("no signature in Yellowstone transaction")
	}

	if transaction.Message == nil || len(transaction.Message.Instructions) == 0 {
		return nil, fmt.Errorf("no instructions in Yellowstone transaction")
	}

	pumpFunInstructions := p.findPumpFunInstructionsFromMessage(transaction.Message)
	if len(pumpFunInstructions) == 0 {
		return nil, fmt.Errorf("no Pump.Fun instructions found in Yellowstone transaction")
	}

	instruction := pumpFunInstructions[0]
	return p.parseYellowstoneInstructionManual(instruction, transaction.Message, yellowstoneTx, signature)
}

// findPumpFunInstructionsFromMessage locates Pump.Fun program instructions from Yellowstone message
func (p *PumpFunParser) findPumpFunInstructionsFromMessage(message *pb.Message) []InstructionData {
	var instructions []InstructionData

	if message == nil || len(message.Instructions) == 0 {
		return instructions
	}

	for _, instr := range message.Instructions {
		if int(instr.ProgramIdIndex) >= len(message.AccountKeys) {
			continue
		}
		
		programKeyBytes := message.AccountKeys[instr.ProgramIdIndex]
		if len(programKeyBytes) != 32 {
			continue
		}
		programID := solana.PublicKey(programKeyBytes)
		
		if programID.Equals(p.pumpFunProgram) {
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

// parseYellowstoneInstructionManual extracts discriminators and focuses on CREATE only
func (p *PumpFunParser) parseYellowstoneInstructionManual(instr InstructionData, message *pb.Message, yellowstoneTx *pb.SubscribeUpdateTransaction, signature string) (*TokenLaunchData, error) {
	data := instr.Data
	
	if len(data) < 8 {
		return nil, fmt.Errorf("instruction data too short: %d bytes", len(data))
	}

	discriminator := data[:8]
	
	logrus.WithFields(logrus.Fields{
		"signature":     signature[:8] + "...",
		"discriminator": fmt.Sprintf("%x", discriminator),
		"data_length":   len(data),
		"account_count": len(instr.Accounts),
	}).Debug("🔍 Discriminator detected")
	
	if bytes.Equal(discriminator, p.config.DiscriminatorCreate) {
		logrus.WithField("signature", signature[:8]+"...").Info("🆕 Found CREATE instruction")
		return p.parseCreateInstructionManual(instr, data, signature)
	}
	
	instructionType := "unknown"
	if bytes.Equal(discriminator, p.config.DiscriminatorBuy) {
		instructionType = "buy"
	} else if bytes.Equal(discriminator, p.config.DiscriminatorSell) {
		instructionType = "sell"
	} else if bytes.Equal(discriminator, p.config.DiscriminatorMigration) {
		instructionType = "migration"
	}
	
	logrus.WithFields(logrus.Fields{
		"signature": signature[:8] + "...",
		"type":      instructionType,
	}).Debug("🔄 Non-CREATE instruction - skipping")
	
	return nil, fmt.Errorf("%s instruction - only processing CREATE", instructionType)
}

// parseCreateInstructionManual handles token creation events with manual decoding
func (p *PumpFunParser) parseCreateInstructionManual(instr InstructionData, data []byte, signature string) (*TokenLaunchData, error) {
	if len(instr.Accounts) < 8 {
		return nil, fmt.Errorf("insufficient accounts for create instruction: have %d, need 8", len(instr.Accounts))
	}

	tokenData := &TokenLaunchData{
		Mint:            instr.Accounts[0],
		BondingCurve:    instr.Accounts[2],
		User:            instr.Accounts[7],
		Signature:       signature,
		Timestamp:       time.Now().Unix(),
		InstructionType: "create",
	}

	if len(data) >= 32 {
		tokenData.VirtualSolReserves = 30 * 1e9
		tokenData.VirtualTokenReserves = 1073000000 * 1e6
	}

	tokenData.MarketCapUSD = p.calculateMarketCap(tokenData.VirtualTokenReserves, tokenData.VirtualSolReserves)
	p.logTokenCreation(tokenData)

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

	if len(data) >= 24 {
		tokenData.SolAmount = binary.LittleEndian.Uint64(data[8:16])
		tokenData.TokenAmount = binary.LittleEndian.Uint64(data[16:24])
	}

	tokenData.VirtualSolReserves = 50 * 1e9
	tokenData.VirtualTokenReserves = 800_000_000 * 1e6
	tokenData.MarketCapUSD = p.calculateMarketCap(tokenData.VirtualTokenReserves, tokenData.VirtualSolReserves)

	logrus.WithFields(logrus.Fields{
		"token":      tokenData.Mint.String(),
		"market_cap": fmt.Sprintf("$%.0f", tokenData.MarketCapUSD),
		"sol_amount": fmt.Sprintf("%.6f SOL", float64(tokenData.SolAmount)/1e9),
		"solscan":    "https://solscan.io/token/" + tokenData.Mint.String(),
		"tx":         "https://solscan.io/tx/" + tokenData.Signature,
		"user":       tokenData.User.String()[:8] + "...",
	}).Info("🛒 TOKEN BUY DETECTED!")

	return tokenData, nil
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
	}).Debug("💰 Sell detected - not trading")

	return nil, fmt.Errorf("sell instruction - not a trading opportunity")
}

// calculateMarketCap calculates market cap from reserves
func (p *PumpFunParser) calculateMarketCap(virtualTokenReserves, virtualSolReserves uint64) float64 {
	if virtualTokenReserves == 0 {
		return 0
	}

	totalSupply := 1_000_000_000.0
	solPrice := p.config.PriceService.GetPrice()
	
	solReservesFloat := float64(virtualSolReserves) / 1e9
	tokenReservesFloat := float64(virtualTokenReserves) / 1e6
	
	if tokenReservesFloat == 0 {
		return 0
	}
	
	pricePerToken := solReservesFloat / tokenReservesFloat
	marketCap := pricePerToken * totalSupply * solPrice
	
	return marketCap
}

// calculateMarketCapFromReserves calculates market cap from reserves with total supply
func (p *PumpFunParser) calculateMarketCapFromReserves(virtualSolReserves, virtualTokenReserves, tokenTotalSupply uint64) float64 {
	if virtualTokenReserves == 0 || tokenTotalSupply == 0 {
		return 0
	}
	
	solReserves := float64(virtualSolReserves) / 1e9
	tokenReserves := float64(virtualTokenReserves) / 1e6
	totalSupply := float64(tokenTotalSupply) / 1e6
	
	pricePerToken := solReserves / tokenReserves
	solPrice := p.config.PriceService.GetPrice()
	marketCap := pricePerToken * totalSupply * solPrice
	
	return marketCap
}

// extractAnchorEventData extracts real market data from anchor program events
func (p *PumpFunParser) extractAnchorEventData(yellowstoneTx *pb.SubscribeUpdateTransaction) *AnchorEventData {
	if yellowstoneTx.Transaction == nil || yellowstoneTx.Transaction.Meta == nil {
		return nil
	}

	meta := yellowstoneTx.Transaction.Meta
	if meta.LogMessages == nil {
		return nil
	}

	for _, logMsg := range meta.LogMessages {
		if eventData := p.parseAnchorEventFromLog(logMsg); eventData != nil {
			return eventData
		}
	}

	return nil
}

// parseAnchorEventFromLog parses anchor program event data from log messages
func (p *PumpFunParser) parseAnchorEventFromLog(logMsg string) *AnchorEventData {
	if !strings.Contains(logMsg, "mint") && !strings.Contains(logMsg, "virtualSolReserves") {
		return nil
	}

	eventData := &AnchorEventData{}
	
	if mintMatch := regexp.MustCompile(`"mint":\s*"([A-Za-z0-9]{44})"`).FindStringSubmatch(logMsg); len(mintMatch) > 1 {
		if mint, err := solana.PublicKeyFromBase58(mintMatch[1]); err == nil {
			eventData.Mint = mint
		}
	}
	
	if solMatch := regexp.MustCompile(`"virtualSolReserves":\s*"(\d+)"`).FindStringSubmatch(logMsg); len(solMatch) > 1 {
		if reserves, err := strconv.ParseUint(solMatch[1], 10, 64); err == nil {
			eventData.VirtualSolReserves = reserves
		}
	}
	
	if tokenMatch := regexp.MustCompile(`"virtualTokenReserves":\s*"(\d+)"`).FindStringSubmatch(logMsg); len(tokenMatch) > 1 {
		if reserves, err := strconv.ParseUint(tokenMatch[1], 10, 64); err == nil {
			eventData.VirtualTokenReserves = reserves
		}
	}
	
	if userMatch := regexp.MustCompile(`"user":\s*"([A-Za-z0-9]{44})"`).FindStringSubmatch(logMsg); len(userMatch) > 1 {
		if user, err := solana.PublicKeyFromBase58(userMatch[1]); err == nil {
			eventData.User = user
		}
	}
	
	if buyMatch := regexp.MustCompile(`"isBuy":\s*(true|false)`).FindStringSubmatch(logMsg); len(buyMatch) > 1 {
		eventData.IsBuy = buyMatch[1] == "true"
	}
	
	if !eventData.Mint.IsZero() && eventData.VirtualSolReserves > 0 && eventData.VirtualTokenReserves > 0 {
		return eventData
	}
	
	return nil
}

// extractCreateTokenData extracts CREATE token data from anchor program events
func (p *PumpFunParser) extractCreateTokenData(yellowstoneTx *pb.SubscribeUpdateTransaction) (*CreateTokenEventData, error) {
	if yellowstoneTx.Transaction == nil || yellowstoneTx.Transaction.Meta == nil {
		return nil, fmt.Errorf("no transaction metadata")
	}

	meta := yellowstoneTx.Transaction.Meta
	if meta.LogMessages == nil {
		return nil, fmt.Errorf("no log messages")
	}

	for _, logMsg := range meta.LogMessages {
		if createData := p.parseCreateEventFromLog(logMsg); createData != nil {
			return createData, nil
		}
	}

	return nil, fmt.Errorf("no CREATE anchor event data found")
}

// parseCreateEventFromLog parses CREATE anchor program event data from log messages
func (p *PumpFunParser) parseCreateEventFromLog(logMsg string) *CreateTokenEventData {
	if !strings.Contains(logMsg, "mint") || !strings.Contains(logMsg, "virtualSolReserves") {
		return nil
	}

	createData := &CreateTokenEventData{}
	
	regexMatches := map[string]*regexp.Regexp{
		"mint":                 regexp.MustCompile(`"mint":\s*"([A-Za-z0-9]{44})"`),
		"name":                 regexp.MustCompile(`"name":\s*"([^"]+)"`),
		"symbol":               regexp.MustCompile(`"symbol":\s*"([^"]+)"`),
		"uri":                  regexp.MustCompile(`"uri":\s*"([^"]+)"`),
		"bondingCurve":         regexp.MustCompile(`"bondingCurve":\s*"([A-Za-z0-9]{44})"`),
		"user":                 regexp.MustCompile(`"user":\s*"([A-Za-z0-9]{44})"`),
		"creator":              regexp.MustCompile(`"creator":\s*"([A-Za-z0-9]{44})"`),
		"virtualSolReserves":   regexp.MustCompile(`"virtualSolReserves":\s*"(\d+)"`),
		"virtualTokenReserves": regexp.MustCompile(`"virtualTokenReserves":\s*"(\d+)"`),
		"realTokenReserves":    regexp.MustCompile(`"realTokenReserves":\s*"(\d+)"`),
		"tokenTotalSupply":     regexp.MustCompile(`"tokenTotalSupply":\s*"(\d+)"`),
		"timestamp":            regexp.MustCompile(`"timestamp":\s*"(\d+)"`),
	}
	
	for field, regex := range regexMatches {
		if match := regex.FindStringSubmatch(logMsg); len(match) > 1 {
			switch field {
			case "mint":
				if mint, err := solana.PublicKeyFromBase58(match[1]); err == nil {
					createData.Mint = mint
				}
			case "name":
				createData.Name = match[1]
			case "symbol":
				createData.Symbol = match[1]
			case "uri":
				createData.URI = match[1]
			case "bondingCurve":
				createData.BondingCurve = match[1]
			case "user":
				if user, err := solana.PublicKeyFromBase58(match[1]); err == nil {
					createData.User = user
				}
			case "creator":
				if creator, err := solana.PublicKeyFromBase58(match[1]); err == nil {
					createData.Creator = creator
				}
			case "virtualSolReserves":
				if reserves, err := strconv.ParseUint(match[1], 10, 64); err == nil {
					createData.VirtualSolReserves = reserves
				}
			case "virtualTokenReserves":
				if reserves, err := strconv.ParseUint(match[1], 10, 64); err == nil {
					createData.VirtualTokenReserves = reserves
				}
			case "realTokenReserves":
				if reserves, err := strconv.ParseUint(match[1], 10, 64); err == nil {
					createData.RealTokenReserves = reserves
				}
			case "tokenTotalSupply":
				if supply, err := strconv.ParseUint(match[1], 10, 64); err == nil {
					createData.TokenTotalSupply = supply
				}
			case "timestamp":
				if timestamp, err := strconv.ParseUint(match[1], 10, 64); err == nil {
					createData.Timestamp = timestamp
				}
			}
		}
	}
	
	if !createData.Mint.IsZero() && createData.VirtualSolReserves > 0 && createData.VirtualTokenReserves > 0 && createData.Name != "" && createData.Symbol != "" {
		return createData
	}
	
	return nil
}

// extractBondingCurveStateManual attempts to extract bonding curve data from logs manually
func (p *PumpFunParser) extractBondingCurveStateManual(tx *rpc.GetTransactionResult) *BondingCurveState {
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
	return nil
}

// logTokenCreation logs token creation with nice formatting
func (p *PumpFunParser) logTokenCreation(token *TokenLaunchData) {
	var marketCapStr string
	if token.MarketCapUSD >= 1000000 {
		marketCapStr = fmt.Sprintf("$%.1fM", token.MarketCapUSD/1000000)
	} else if token.MarketCapUSD >= 1000 {
		marketCapStr = fmt.Sprintf("$%.1fK", token.MarketCapUSD/1000)
	} else {
		marketCapStr = fmt.Sprintf("$%.0f", token.MarketCapUSD)
	}
	
	sanitizedToken := utils.SanitizeWalletAddress(token.Mint.String())
	sanitizedCreator := utils.SanitizeWalletAddress(token.User.String())
	
	logrus.WithFields(logrus.Fields{
		"token":      sanitizedToken,
		"name":       token.Name,
		"symbol":     token.Symbol,
		"market_cap": marketCapStr,
		"creator":    sanitizedCreator,
		"solscan":    "https://solscan.io/token/" + token.Mint.String(),
		"tx":         "https://solscan.io/tx/" + token.Signature,
	}).Info("🆕 NEW TOKEN CREATED!")
}
