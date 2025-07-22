# 🤖 Golang Pump.Fun Sniper Bot

A high-performance, low-latency sniper bot for Pump.Fun new token launches built in Go. This bot monitors new token mints via WebSocket RPC, filters by market cap, and automatically executes buy transactions with minimal latency.

## 🎯 Project Overview

This bot implements a complete workflow for sniping new Pump.Fun tokens:
- **Monitor**: Subscribe to Pump.Fun program via WebSocket RPC for real-time updates
- **Parse**: Extract mint information and calculate market cap from transactions using real-time SOL prices
- **Filter**: Only target tokens with market cap above $8,000
- **Execute**: Build and submit REAL Pump.Fun swap transactions with optimized speed

## 🏗️ Architecture

### Core Components

1. **Config Management** (`internal/config/`)
   - Environment variable handling with `.env` support
   - Wallet key management
   - Network endpoint configuration
   - Real-time SOL price integration

2. **WebSocket Monitor** (`internal/monitor/`)
   - Real-time transaction streaming via Solana WebSocket
   - Pump.Fun program log subscription
   - Connection handling with retry logic

3. **Transaction Parser** (`internal/parser/`)
   - Real Pump.Fun transaction parsing using `tx-parser` library
   - Market cap calculation from bonding curve data
   - Mint extraction and validation

4. **Trading Engine** (`internal/trader/`)
   - Pump.Fun swap transaction construction
   - Solana transaction building with proper PDA derivation
   - Transaction submission and confirmation tracking

5. **Price Service** (`internal/price/`)
   - Real-time SOL price fetching from CoinGecko
   - Thread-safe price updates every 5 minutes
   - Accurate market cap calculations

### Data Flow

WebSocket RPC Stream → Transaction Parser → Market Cap Filter → Trading Engine → Solana Network
        ↓                     ↓                   ↓                    ↓              ↓
   Real-time data      Extract REAL info    Check ≥ $8k MC      Build REAL TX   Submit & confirm

### Key Features

- ✅ **Real-time monitoring** of Pump.Fun program logs
- ✅ **REAL transaction parsing** (no simulation/fake data)
- ✅ **Live SOL price updates** for accurate market cap calculation
- ✅ **Actual Pump.Fun swap transactions** (not placeholders)
- ✅ **Graceful shutdown** with Ctrl+C support
- ✅ **Simulation mode** for safe testing
- ✅ **Production-ready** error handling and logging

## 🚀 Quick Start

### Prerequisites
- Go 1.21+
- Solana wallet with SOL balance
- Helius RPC endpoint access

### Installation

```bash
# Clone and setup
git clone <repository>
cd golang-pumpfun-sniper
go mod tidy

# Configure your settings
cp .env.example .env
nano .env
```

Create a `.env` file with your configuration:

```env
# Helius Endpoints
GRPC_ENDPOINT=grpc_endpoint
GRPC_TOKEN=grpc_token
RPC_ENDPOINT=rpc_endpoint

# Wallet Configuration
PRIVATE_KEY=private_key

# Trading Parameters
BUY_AMOUNT_SOL=0.001
MIN_MARKET_CAP=8000
MAX_SLIPPAGE=0.05

# Performance Settings
MAX_RETRIES=3
TIMEOUT_SECONDS=10
LOG_LEVEL=info

# Pump.Fun Program ID
PUMP_FUN_PROGRAM_ID=6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P
```

### Running the Bot

```bash
# Run with live trading
go run cmd/main.go

# Run in simulation mode (no actual trades)
go run cmd/main.go --simulate

# Build binary
go build -o sniper cmd/main.go
./sniper
```

## 📊 Performance Optimizations

### Low Latency Design
- **Single GRPC Connection**: Persistent connection to Geyser for real-time data
- **Minimal RPC Calls**: All required data extracted from the initial transaction
- **Concurrent Processing**: Parallel parsing and transaction building
- **Pre-computed Instructions**: Template transactions ready for quick modification

### Memory Efficiency
- **Streaming Processing**: No buffering of large datasets
- **Pool Allocations**: Reused objects to minimize GC pressure
- **Efficient Parsing**: Direct binary parsing without intermediate JSON

## 🧪 Testing Strategy

### Unit Tests
- Transaction parsing accuracy
- Market cap calculation validation
- Configuration loading
- Error handling scenarios

### Integration Tests
- GRPC connection stability
- End-to-end transaction flow
- Network failure recovery

### Performance Tests
- Latency benchmarking
- Memory usage profiling
- Concurrent connection handling

## 📈 Monitoring & Analytics

### Real-time Metrics
- Transactions processed per second
- Average processing latency
- Success/failure rates
- Market cap distribution of detected tokens

### Logging
- Structured JSON logging
- Configurable log levels
- Transaction trace logging
- Performance metrics

## 🛠️ Development Methodology

### Clean Code Principles
- **Single Responsibility**: Each package has one clear purpose
- **Dependency Injection**: Testable, modular components
- **Error Handling**: Explicit error handling throughout
- **Documentation**: Comprehensive code comments and README

### Project Structure

```
├── cmd/
│ └── main.go # Application entry point
├── internal/
│ ├── config/ # Configuration management
│ ├── logger/ # Logging 
│ ├── monitor/ # Metrics
│ ├── parser/ # Transaction parsing
│ ├── price/ # Solana price update
│ └── trader/ # Trading logic
├── .env.example
├── go.mod
└── README.md
```


### Testing

```bash
# Start with simulation mode
go run cmd/main.go --simulate

# Check logs for activity
# Should see: Token parsing, market cap calculations, trade simulations
```

## 🔒 Security

- Private keys stored securely in environment variables
- No sensitive data in logs
- Connection encryption for all network communications
- Graceful shutdown prevents data corruption

---

**⚠️ Disclaimer**: This software is for educational purposes. Trading cryptocurrencies involves significant risk. Only use funds you can afford to lose.
