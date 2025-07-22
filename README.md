# 🤖 Golang Pump.Fun Sniper Bot

A high-performance, low-latency sniper bot for Pump.Fun new token launches built in Go. This bot monitors new token mints via Geyser GRPC, filters by market cap, and automatically executes buy transactions with minimal latency.

## 🎯 Project Overview

This bot implements a complete workflow for sniping new Pump.Fun tokens:
- **Monitor**: Subscribe to Pump.Fun program via Geyser GRPC for real-time updates
- **Parse**: Extract mint information and calculate market cap from transactions
- **Filter**: Only target tokens with market cap above $8,000
- **Execute**: Build and submit buy transactions with optimized speed

## 🏗️ Architecture

### Core Components

1. **Config Management** (`config/`)
   - Environment variable handling
   - Wallet key management
   - Network endpoint configuration

2. **GRPC Client** (`grpc/`)
   - Geyser subscription management
   - Real-time transaction streaming
   - Connection handling with retry logic

3. **Transaction Parser** (`parser/`)
   - Pump.Fun transaction parsing
   - Market cap calculation
   - Mint extraction and validation

4. **Trading Engine** (`trader/`)
   - Buy transaction construction
   - Solana transaction building
   - Transaction submission and confirmation

5. **Monitoring & Logging** (`monitor/`)
   - Performance metrics
   - Success/failure tracking
   - Structured logging

### Data Flow

Geyser GRPC Stream → Transaction Parser → Market Cap Filter → Trading Engine → Solana Network
↓ ↓ ↓ ↓ ↓
Real-time data Extract mint info Check ≥ $8k MC Build buy TX Submit & confirm


## 🚀 Quick Start

### Prerequisites
- Go 1.21+
- Solana wallet with SOL balance
- Access to Helius Geyser GRPC and Rciesod download

# Configure your settings
nano .env
Create a `.env` file with the following variables:

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

## 🔒 Security Considerations

- Private keys handled securely with environment variables
- No sensitive data in logs
- Connection encryption for all network communications
- Graceful error handling to prevent crashes

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
│ ├── grpc/ # Geyser GRPC client
│ ├── parser/ # Transaction parsing
│ ├── trader/ # Trading logic
│ └── monitor/ # Metrics and logging
└── tests/
  ├── unit/ # Unit tests
  └── integration/ #
```
