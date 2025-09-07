# 🌱 Carbon Free Consensus - Ethereum Beacon Chain

## Overview

This repository implements the **consensus layer** component of a carbon-neutral Ethereum system. It extends Prysm (Ethereum Beacon Chain) with automatic carbon offset collection from validator rewards and carbon withdrawal generation to fund environmental initiatives.

## 🎯 What This Does

The consensus layer automatically:
- **Deducts 1% carbon offset** from all validator rewards and penalties
- **Accumulates carbon funds** in a treasury tracking system
- **Creates carbon withdrawals** that are sent to the execution layer
- **Transfers collected funds** to designated treasury addresses

## 🔧 Carbon System Architecture

### How It Works

1. **Validator earns rewards** (attestation + proposal rewards)
2. **1% carbon offset** is automatically deducted using `applyCarbonOffset()`
3. **Carbon funds accumulate** in the beacon state treasury balance
4. **Carbon withdrawal is created** with special validator index `0xFFFFFFFF`
5. **Execution layer processes** the withdrawal and transfers to treasury

### Key Components

- **Carbon Offset Rate**: 1% (100 basis points) configurable via `CarbonOffsetRate`
- **Treasury Tracking**: Beacon state tracks accumulated carbon funds
- **Carbon Withdrawals**: Special system withdrawals sent to execution layer
- **Treasury Address**: Configurable destination for carbon funds

## 🚀 Quick Start

### Prerequisites

- Go 1.21 or later
- Git

### Installation

1. **Clone the repository**
   ```bash
   git clone <your-carbon-free-consensus-repo>
   cd CarbonFreeConsensys
   ```

2. **Build the Beacon Chain**
   ```bash
   go build -o build/beacon-chain ./cmd/beacon-chain
   ```

3. **Build the Validator Client**
   ```bash
   go build -o build/validator ./cmd/validator
   ```

4. **Verify the build**
   ```bash
   ./build/beacon-chain --version
   ./build/validator --version
   ```

## 🧪 Testing

### Run Carbon System Tests

Test the carbon offset and withdrawal functionality:

```bash
# Test carbon offset calculation
go test -v ./beacon-chain/core/epoch/precompute -run TestCarbon

# Test all carbon-related functionality  
go test -v ./beacon-chain/core/epoch/precompute -run Test
```

### Expected Test Output

```
=== RUN   TestApplyCarbonOffset
=== RUN   TestApplyCarbonOffset/1%_carbon_offset_on_1_ETH_reward
=== RUN   TestApplyCarbonOffset/0.5%_carbon_offset_on_0.5_ETH_reward
--- PASS: TestApplyCarbonOffset (0.00s)

=== RUN   TestCarbonOffsetIntegration
--- PASS: TestCarbonOffsetIntegration (0.00s)

CARBON WITHDRAWAL CREATED: Index=0, ValidatorIndex=4294967295, Amount=5096 Gwei
CARBON WITHDRAWAL: 5096 Gwei → 0x1234567890123456789012345678901234567890
Treasury Balance: 0 → 5096 Gwei
```

## 📊 Carbon System Implementation Details

### Carbon Offset Collection

```go
// Apply 1% carbon offset to validator rewards
func applyCarbonOffset(totalReward uint64) uint64 {
    cfg := params.BeaconConfig()
    
    if cfg.CarbonOffsetRate == 0 {
        return 0
    }
    
    // Calculate carbon deduction: totalReward * rate / 10000 (basis points)
    carbonDeduction := totalReward * cfg.CarbonOffsetRate / 10000
    
    return carbonDeduction
}
```

### Carbon Withdrawal Creation

```go
// Create carbon system withdrawal to execution layer
func createCarbonWithdrawal(state state.BeaconState, treasuryAddress common.Address, amount uint64) error {
    withdrawal := &enginev1.Withdrawal{
        Index:          carbonWithdrawalCounter,
        ValidatorIndex: cfg.CarbonSystemValidatorIndex, // 0xFFFFFFFF
        Address:        treasuryAddress.Bytes(),
        Amount:         amount, // Amount in Gwei
    }
    
    // Send to execution layer for processing
    return storeCarbonWithdrawal(state, withdrawal)
}
```

### Reward Processing Integration

```go
// Integrated into reward processing pipeline
func ProcessRewardsAndPenaltiesPrecompute(state, pBal, vp, ...) {
    for i := 0; i < numOfVals; i++ {
        // Calculate normal rewards
        rewards := attsRewards[i] + proposerRewards[i]
        
        // Apply carbon offset deduction
        if params.BeaconConfig().CarbonOffsetActivationEpoch <= time.CurrentEpoch(state) {
            carbonDeduction := applyCarbonOffset(rewards)
            if carbonDeduction > 0 {
                validatorBals[i] = helpers.DecreaseBalanceWithVal(validatorBals[i], carbonDeduction)
                totalCarbonFundsCollected += carbonDeduction
            }
        }
    }
    
    // Transfer carbon funds to treasury
    return transferCarbonFundsToTreasury(state)
}
```

## ⚙️ Configuration

### Beacon Chain Configuration

The carbon system can be configured via `config/params/config.go`:

```go
type BeaconChainConfig struct {
    // Carbon offset configuration
    CarbonOffsetActivationEpoch primitives.Epoch // When to start carbon offset
    CarbonOffsetRate           uint64            // Rate in basis points (100 = 1%)
    CarbonTreasuryAddress      common.Address    // Treasury destination address
    CarbonSystemValidatorIndex uint64            // Special validator index (0xFFFFFFFF)
}
```

### Example Configuration

```go
// 1% carbon offset starting from epoch 0
CarbonOffsetActivationEpoch: 0
CarbonOffsetRate:           100  // 1% = 100 basis points
CarbonTreasuryAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890")
CarbonSystemValidatorIndex: 0xFFFFFFFF
```

## 🔄 Integration with Execution Layer

This consensus layer works with the **CarbonNeutralityEIP** execution layer:

1. **Consensus Layer** (this repo):
   - Collects carbon offset from validator rewards
   - Creates carbon withdrawals with special validator index
   - Sends withdrawals to execution layer via Engine API

2. **Execution Layer**: 
   - Receives carbon withdrawals from consensus layer
   - Processes them as special system transactions
   - Transfers funds to treasury addresses

### Carbon Withdrawal Flow

```
Validator Rewards (1000 Gwei)
    ↓
Carbon Offset Deduction (10 Gwei @ 1%)
    ↓
Validator Receives (990 Gwei)
    ↓
Carbon Funds Accumulate (10 Gwei)
    ↓
Carbon Withdrawal Created
    ↓
Execution Layer Processes
    ↓
Treasury Receives (10 Gwei)
```

## 📈 Monitoring Carbon Collection

### Console Output

```
CARBON WITHDRAWAL: 5096 Gwei → 0x1234567890123456789012345678901234567890
Treasury Balance: 0 → 5096 Gwei
CARBON WITHDRAWAL CREATED: Index=0, ValidatorIndex=4294967295, Amount=5096 Gwei
```

### State Tracking

The beacon state tracks:
- `CarbonTreasuryBalance()`: Total carbon funds collected
- `SetCarbonTreasuryBalance()`: Update treasury balance
- Carbon withdrawal counter for unique withdrawal indices

## 🌍 Production Deployment

### Running the Beacon Chain

```bash
# Start beacon chain with carbon configuration
./build/beacon-chain \
    --datadir=./beacon-data \
    --min-sync-peers=1 \
    --execution-endpoint=http://localhost:8551 \
    --accept-terms-of-use
```

### Running Validators

```bash
# Start validator with beacon chain connection
./build/validator \
    --datadir=./validator-data \
    --beacon-rpc-provider=localhost:4000 \
    --accept-terms-of-use
```

### Network Configuration

Ensure your `genesis.ssz` includes:
- Carbon system activation epoch
- Treasury address configuration
- Compatible execution layer setup

## 🧮 Carbon Offset Economics

### Impact Calculation

- **1% Offset Rate**: For every 1 ETH in validator rewards, 0.01 ETH goes to carbon offset
- **Daily Collection**: With ~1800 ETH daily rewards, collects ~18 ETH daily for carbon offset
- **Annual Impact**: Approximately 6,570 ETH annually for environmental initiatives

### Customizable Rates

The system supports configurable offset rates:
- `50 basis points` = 0.5%
- `100 basis points` = 1.0% (default)
- `200 basis points` = 2.0%

## 🤝 Contributing

1. Fork the repository
2. Create your feature branch
3. Run tests: `go test -v ./beacon-chain/core/epoch/precompute`
4. Ensure carbon system tests pass
5. Commit your changes
6. Push to the branch
7. Create a Pull Request

## 📝 License

This project extends Prysm and maintains the same GPL v3.0 license.

## 🔗 Related Repositories

- **CarbonNeutralityEIP**: The execution layer that processes carbon withdrawals
- **Original Prysm**: https://github.com/prysmaticlabs/prysm

## ❓ FAQ

**Q: How is the carbon rate determined?**  
A: Currently set to 1% (100 basis points) but configurable in beacon chain config.

**Q: When does carbon collection start?**  
A: Configurable via `CarbonOffsetActivationEpoch`, can be set to start from genesis or any epoch.

**Q: What happens to collected carbon funds?**  
A: They're automatically sent to the configured treasury address via carbon withdrawals.

**Q: Can validators opt out of carbon offset?**  
A: No, it's a protocol-level feature that applies to all validators equally.

**Q: How do I verify carbon collection is working?**  
A: Check the test outputs, beacon chain logs, and treasury address balance.

## 🆘 Support

For issues and questions:
- Create an issue in this repository  
- Check the test outputs for debugging
- Review the execution layer repository for the complete carbon system
- Run `go test -v` to verify functionality

## 📊 Technical Specifications

- **Carbon Rate**: 1% (100 basis points)
- **Activation**: Configurable epoch
- **Withdrawal Index**: Special validator index `0xFFFFFFFF`
- **Treasury Tracking**: Integrated into beacon state
- **Gas Efficiency**: Minimal overhead on reward processing

---

**⚡ Built for ETH Istanbul Hackathon - Making Ethereum Proof of Stake Carbon Neutral**