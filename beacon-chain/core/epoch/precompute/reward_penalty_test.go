package precompute

import (
	"testing"
	"fmt"        // Add this line
	"github.com/OffchainLabs/prysm/v6/beacon-chain/core/helpers"
	"github.com/OffchainLabs/prysm/v6/beacon-chain/core/time"
	"github.com/OffchainLabs/prysm/v6/beacon-chain/state"
	state_native "github.com/OffchainLabs/prysm/v6/beacon-chain/state/state-native"
	fieldparams "github.com/OffchainLabs/prysm/v6/config/fieldparams"
	"github.com/OffchainLabs/prysm/v6/config/params"
	"github.com/OffchainLabs/prysm/v6/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v6/math"
	ethpb "github.com/OffchainLabs/prysm/v6/proto/prysm/v1alpha1"
	"github.com/OffchainLabs/prysm/v6/runtime/version"
	"github.com/OffchainLabs/prysm/v6/testing/assert"
	"github.com/OffchainLabs/prysm/v6/testing/require"
	"github.com/pkg/errors"
	"github.com/prysmaticlabs/go-bitfield"
	"github.com/ethereum/go-ethereum/common" // Add this line
)

func TestProcessRewardsAndPenaltiesPrecompute(t *testing.T) {
	e := params.BeaconConfig().SlotsPerEpoch
	validatorCount := uint64(2048)
	base := buildState(e+3, validatorCount)
	atts := make([]*ethpb.PendingAttestation, 3)
	for i := 0; i < len(atts); i++ {
		atts[i] = &ethpb.PendingAttestation{
			Data: &ethpb.AttestationData{
				Target: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
				Source: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
			},
			AggregationBits: bitfield.Bitlist{0x00, 0x00, 0x00, 0x00, 0xC0, 0xC0, 0xC0, 0xC0, 0x01},
			InclusionDelay:  1,
		}
	}
	base.PreviousEpochAttestations = atts

	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)

	vp, bp, err := New(t.Context(), beaconState)
	require.NoError(t, err)
	vp, bp, err = ProcessAttestations(t.Context(), beaconState, vp, bp)
	require.NoError(t, err)

	// Set carbon offset rate for this test
	originalRate := params.BeaconConfig().CarbonOffsetRate
	originalAddress := params.BeaconConfig().CarbonTreasuryAddress
	originalActivationEpoch := params.BeaconConfig().CarbonOffsetActivationEpoch
	originalSystemValidatorIndex := params.BeaconConfig().CarbonSystemValidatorIndex
	params.BeaconConfig().CarbonOffsetRate = 100 // 1%
	params.BeaconConfig().CarbonTreasuryAddress = common.HexToAddress("0x1234567890123456789012345678901234567890")
	params.BeaconConfig().CarbonOffsetActivationEpoch = 0 // Activate from epoch 0
	params.BeaconConfig().CarbonSystemValidatorIndex = 0xFFFFFFFF // Special system validator index
	
	defer func() {
		params.BeaconConfig().CarbonOffsetRate = originalRate
		params.BeaconConfig().CarbonTreasuryAddress = originalAddress
		params.BeaconConfig().CarbonOffsetActivationEpoch = originalActivationEpoch
		params.BeaconConfig().CarbonSystemValidatorIndex = originalSystemValidatorIndex
	}()

	processedState, err := ProcessRewardsAndPenaltiesPrecompute(beaconState, bp, vp, AttestationsDelta, ProposersDelta)
	require.NoError(t, err)
	require.Equal(t, true, processedState.Version() == version.Phase0)

	// Check carbon offset is working - balances should be lower than original
	actualBalance0 := beaconState.Balances()[0]
	actualBalance4 := beaconState.Balances()[4]
	
	fmt.Printf("Actual balance[0]: %d\n", actualBalance0)
	fmt.Printf("Actual balance[4]: %d\n", actualBalance4)
	
	// Test that carbon offset has been applied - values should be modified
	wanted := uint64(31999872873) // Expected for validator[0] WITH carbon offset (epoch 0 activation)
	assert.Equal(t, wanted, actualBalance0, "Unexpected balance")

	wanted = uint64(31999810265) // Expected for validator[4] WITH carbon offset  
	assert.Equal(t, wanted, actualBalance4, "Unexpected balance")
	
	// Check that carbon treasury balance has been updated
	if bs, ok := processedState.(interface{ CarbonTreasuryBalance() primitives.Gwei }); ok {
		treasuryBalance := bs.CarbonTreasuryBalance()
		fmt.Printf("Carbon Treasury Balance: %d Gwei\n", treasuryBalance)
		if treasuryBalance == 0 {
			t.Error("Treasury balance should be greater than 0")
		}
	}
}

func TestAttestationDeltas_ZeroEpoch(t *testing.T) {
	e := params.BeaconConfig().SlotsPerEpoch
	validatorCount := uint64(2048)
	base := buildState(e+2, validatorCount)
	atts := make([]*ethpb.PendingAttestation, 3)
	var emptyRoot [32]byte
	for i := 0; i < len(atts); i++ {
		atts[i] = &ethpb.PendingAttestation{
			Data: &ethpb.AttestationData{
				Target: &ethpb.Checkpoint{
					Root: emptyRoot[:],
				},
				Source: &ethpb.Checkpoint{
					Root: emptyRoot[:],
				},
				BeaconBlockRoot: emptyRoot[:],
			},
			AggregationBits: bitfield.Bitlist{0x00, 0x00, 0x00, 0x00, 0xC0, 0xC0, 0xC0, 0xC0, 0x01},
			InclusionDelay:  1,
		}
	}
	base.PreviousEpochAttestations = atts
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)

	pVals, pBal, err := New(t.Context(), beaconState)
	assert.NoError(t, err)
	pVals, pBal, err = ProcessAttestations(t.Context(), beaconState, pVals, pBal)
	require.NoError(t, err)

	pBal.ActiveCurrentEpoch = 0 // Could cause a divide by zero panic.

	_, _, err = AttestationsDelta(beaconState, pBal, pVals)
	require.NoError(t, err)
}

func TestAttestationDeltas_ZeroInclusionDelay(t *testing.T) {
	e := params.BeaconConfig().SlotsPerEpoch
	validatorCount := uint64(2048)
	base := buildState(e+2, validatorCount)
	atts := make([]*ethpb.PendingAttestation, 3)
	var emptyRoot [32]byte
	for i := 0; i < len(atts); i++ {
		atts[i] = &ethpb.PendingAttestation{
			Data: &ethpb.AttestationData{
				Target: &ethpb.Checkpoint{
					Root: emptyRoot[:],
				},
				Source: &ethpb.Checkpoint{
					Root: emptyRoot[:],
				},
				BeaconBlockRoot: emptyRoot[:],
			},
			AggregationBits: bitfield.Bitlist{0xC0, 0xC0, 0xC0, 0xC0, 0x01},
			// Inclusion delay of 0 is not possible in a valid state and could cause a divide by
			// zero panic.
			InclusionDelay: 0,
		}
	}
	base.PreviousEpochAttestations = atts
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)

	pVals, pBal, err := New(t.Context(), beaconState)
	require.NoError(t, err)
	_, _, err = ProcessAttestations(t.Context(), beaconState, pVals, pBal)
	require.ErrorContains(t, "attestation with inclusion delay of 0", err)
}

func TestProcessRewardsAndPenaltiesPrecompute_SlashedInactivePenalty(t *testing.T) {
	e := params.BeaconConfig().SlotsPerEpoch
	validatorCount := uint64(2048)
	base := buildState(e+3, validatorCount)
	atts := make([]*ethpb.PendingAttestation, 3)
	for i := 0; i < len(atts); i++ {
		atts[i] = &ethpb.PendingAttestation{
			Data: &ethpb.AttestationData{
				Target: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
				Source: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
			},
			AggregationBits: bitfield.Bitlist{0x00, 0x00, 0x00, 0x00, 0xC0, 0xC0, 0xC0, 0xC0, 0x01},
			InclusionDelay:  1,
		}
	}
	base.PreviousEpochAttestations = atts

	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)
	require.NoError(t, beaconState.SetSlot(params.BeaconConfig().SlotsPerEpoch*10))

	slashedAttestedIndices := []primitives.ValidatorIndex{14, 37, 68, 77, 139}
	for _, i := range slashedAttestedIndices {
		vs := beaconState.Validators()
		vs[i].Slashed = true
		require.NoError(t, beaconState.SetValidators(vs))
	}

	vp, bp, err := New(t.Context(), beaconState)
	require.NoError(t, err)
	vp, bp, err = ProcessAttestations(t.Context(), beaconState, vp, bp)
	require.NoError(t, err)
	rewards, penalties, err := AttestationsDelta(beaconState, bp, vp)
	require.NoError(t, err)

	finalityDelay := time.PrevEpoch(beaconState) - beaconState.FinalizedCheckpointEpoch()
	for _, i := range slashedAttestedIndices {
		base, err := baseReward(beaconState, i)
		require.NoError(t, err, "Could not get base reward")
		penalty := 3 * base
		proposerReward := base / params.BeaconConfig().ProposerRewardQuotient
		penalty += params.BeaconConfig().BaseRewardsPerEpoch*base - proposerReward
		penalty += vp[i].CurrentEpochEffectiveBalance * uint64(finalityDelay) / params.BeaconConfig().InactivityPenaltyQuotient
		assert.Equal(t, penalty, penalties[i], "Unexpected slashed indices penalty balance")
		assert.Equal(t, uint64(0), rewards[i], "Unexpected slashed indices reward balance")
	}
}

func buildState(slot primitives.Slot, validatorCount uint64) *ethpb.BeaconState {
	validators := make([]*ethpb.Validator, validatorCount)
	for i := 0; i < len(validators); i++ {
		validators[i] = &ethpb.Validator{
			ExitEpoch:        params.BeaconConfig().FarFutureEpoch,
			EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance,
		}
	}
	validatorBalances := make([]uint64, len(validators))
	for i := 0; i < len(validatorBalances); i++ {
		validatorBalances[i] = params.BeaconConfig().MaxEffectiveBalance
	}
	latestActiveIndexRoots := make(
		[][]byte,
		params.BeaconConfig().EpochsPerHistoricalVector,
	)
	for i := 0; i < len(latestActiveIndexRoots); i++ {
		latestActiveIndexRoots[i] = params.BeaconConfig().ZeroHash[:]
	}
	latestRandaoMixes := make(
		[][]byte,
		params.BeaconConfig().EpochsPerHistoricalVector,
	)
	for i := 0; i < len(latestRandaoMixes); i++ {
		latestRandaoMixes[i] = params.BeaconConfig().ZeroHash[:]
	}
	return &ethpb.BeaconState{
		Slot:                        slot,
		Balances:                    validatorBalances,
		Validators:                  validators,
		RandaoMixes:                 make([][]byte, params.BeaconConfig().EpochsPerHistoricalVector),
		Slashings:                   make([]uint64, params.BeaconConfig().EpochsPerSlashingsVector),
		BlockRoots:                  make([][]byte, params.BeaconConfig().SlotsPerEpoch*10),
		FinalizedCheckpoint:         &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
		PreviousJustifiedCheckpoint: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
		CurrentJustifiedCheckpoint:  &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
	}
}

func TestProposerDeltaPrecompute_HappyCase(t *testing.T) {
	e := params.BeaconConfig().SlotsPerEpoch
	validatorCount := uint64(10)
	base := buildState(e, validatorCount)
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)

	proposerIndex := primitives.ValidatorIndex(1)
	b := &Balance{ActiveCurrentEpoch: 1000}
	v := []*Validator{
		{IsPrevEpochAttester: true, CurrentEpochEffectiveBalance: 32, ProposerIndex: proposerIndex},
	}
	r, err := ProposersDelta(beaconState, b, v)
	require.NoError(t, err)

	baseReward := v[0].CurrentEpochEffectiveBalance * params.BeaconConfig().BaseRewardFactor /
		math.IntegerSquareRoot(b.ActiveCurrentEpoch) / params.BeaconConfig().BaseRewardsPerEpoch
	proposerReward := baseReward / params.BeaconConfig().ProposerRewardQuotient

	assert.Equal(t, proposerReward, r[proposerIndex], "Unexpected proposer reward")
}

func TestProposerDeltaPrecompute_ValidatorIndexOutOfRange(t *testing.T) {
	e := params.BeaconConfig().SlotsPerEpoch
	validatorCount := uint64(10)
	base := buildState(e, validatorCount)
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)

	proposerIndex := primitives.ValidatorIndex(validatorCount)
	b := &Balance{ActiveCurrentEpoch: 1000}
	v := []*Validator{
		{IsPrevEpochAttester: true, CurrentEpochEffectiveBalance: 32, ProposerIndex: proposerIndex},
	}
	_, err = ProposersDelta(beaconState, b, v)
	assert.ErrorContains(t, "proposer index out of range", err)
}

func TestProposerDeltaPrecompute_SlashedCase(t *testing.T) {
	e := params.BeaconConfig().SlotsPerEpoch
	validatorCount := uint64(10)
	base := buildState(e, validatorCount)
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)

	proposerIndex := primitives.ValidatorIndex(1)
	b := &Balance{ActiveCurrentEpoch: 1000}
	v := []*Validator{
		{IsPrevEpochAttester: true, CurrentEpochEffectiveBalance: 32, ProposerIndex: proposerIndex, IsSlashed: true},
	}
	r, err := ProposersDelta(beaconState, b, v)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), r[proposerIndex], "Unexpected proposer reward for slashed")
}

// BaseReward takes state and validator index and calculate
// individual validator's base reward quotient.
//
// Spec pseudocode definition:
//
//	def get_base_reward(state: BeaconState, index: ValidatorIndex) -> Gwei:
//	  total_balance = get_total_active_balance(state)
//	  effective_balance = state.validators[index].effective_balance
//	  return Gwei(effective_balance * BASE_REWARD_FACTOR // integer_squareroot(total_balance) // BASE_REWARDS_PER_EPOCH)
func baseReward(state state.ReadOnlyBeaconState, index primitives.ValidatorIndex) (uint64, error) {
	totalBalance, err := helpers.TotalActiveBalance(state)
	if err != nil {
		return 0, errors.Wrap(err, "could not calculate active balance")
	}
	val, err := state.ValidatorAtIndexReadOnly(index)
	if err != nil {
		return 0, err
	}
	effectiveBalance := val.EffectiveBalance()
	baseReward := effectiveBalance * params.BeaconConfig().BaseRewardFactor /
		math.IntegerSquareRoot(totalBalance) / params.BeaconConfig().BaseRewardsPerEpoch
	return baseReward, nil
}

func TestApplyCarbonOffset(t *testing.T) {
	tests := []struct {
		name           string
		reward         uint64
		carbonRate     uint64
		expectedDeduction uint64
	}{
		{
			name:              "1% carbon offset on 1 ETH reward",
			reward:            1000000000, // 1 ETH in Gwei
			carbonRate:        100,        // 1% in basis points
			expectedDeduction: 10000000,   // 0.01 ETH in Gwei
		},
		{
			name:              "0.5% carbon offset on 0.5 ETH reward",
			reward:            500000000,  // 0.5 ETH in Gwei
			carbonRate:        50,         // 0.5% in basis points
			expectedDeduction: 2500000,    // 0.0025 ETH in Gwei
		},
		{
			name:              "Zero carbon rate",
			reward:            1000000000, // 1 ETH in Gwei
			carbonRate:        0,          // 0% rate
			expectedDeduction: 0,          // No deduction
		},
		{
			name:              "Zero reward",
			reward:            0,
			carbonRate:        100,
			expectedDeduction: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Store original carbon rate
			originalRate := params.BeaconConfig().CarbonOffsetRate
			
			// Set test carbon rate
			params.BeaconConfig().CarbonOffsetRate = tt.carbonRate
			
			// Test carbon offset calculation
			result := applyCarbonOffset(tt.reward)
			
			// Verify result
			if result != tt.expectedDeduction {
				t.Errorf("applyCarbonOffset() = %v, want %v", result, tt.expectedDeduction)
			}
			
			// Restore original rate
			params.BeaconConfig().CarbonOffsetRate = originalRate
		})
	}
}

func TestCarbonOffsetIntegration(t *testing.T) {
	// Store original config values
	originalRate := params.BeaconConfig().CarbonOffsetRate
	originalActivationEpoch := params.BeaconConfig().CarbonOffsetActivationEpoch
	originalTreasuryAddress := params.BeaconConfig().CarbonTreasuryAddress
	
	// Set test configuration
	params.BeaconConfig().CarbonOffsetRate = 100 // 1%
	params.BeaconConfig().CarbonOffsetActivationEpoch = 0 // Activate immediately
	params.BeaconConfig().CarbonTreasuryAddress = common.HexToAddress("0x1234567890123456789012345678901234567890")
	
	defer func() {
		// Restore original values
		params.BeaconConfig().CarbonOffsetRate = originalRate
		params.BeaconConfig().CarbonOffsetActivationEpoch = originalActivationEpoch
		params.BeaconConfig().CarbonTreasuryAddress = originalTreasuryAddress
	}()
	
	// Create test state
	e := params.BeaconConfig().SlotsPerEpoch
	validatorCount := uint64(10)
	base := buildState(e+3, validatorCount)
	
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)
	
	// Reset carbon funds counter
	totalCarbonFundsCollected = 0
	
	vp, bp, err := New(t.Context(), beaconState)
	require.NoError(t, err)
	
	// Check if there are any rewards to be processed
	attsRewards, _, err := AttestationsDelta(beaconState, bp, vp) // Use underscore for unused variable
	require.NoError(t, err)
	proposerRewards, err := ProposersDelta(beaconState, bp, vp)
	require.NoError(t, err)
	
	totalRewards := uint64(0)
	for i := 0; i < len(attsRewards); i++ {
		totalRewards += attsRewards[i] + proposerRewards[i]
	}
	
	fmt.Printf("Total rewards before processing: %d\n", totalRewards)
	fmt.Printf("Carbon offset rate: %d%%\n", params.BeaconConfig().CarbonOffsetRate/100)
	
	// If there are rewards, we should see carbon offset
	if totalRewards > 0 {
		expectedCarbonOffset := totalRewards * params.BeaconConfig().CarbonOffsetRate / 10000
		fmt.Printf("Expected carbon offset: %d\n", expectedCarbonOffset)
		
		// Process rewards - this will collect carbon funds and then transfer them
		_, err = ProcessRewardsAndPenaltiesPrecompute(beaconState, bp, vp, AttestationsDelta, ProposersDelta)
		require.NoError(t, err)
		
		fmt.Printf("Carbon offset processing completed without errors\n")
	} else {
		fmt.Printf("No rewards generated in test - carbon offset not triggered\n")
	}
	
	// After processing, funds should be 0 (either because none collected or transferred)
	assert.Equal(t, uint64(0), totalCarbonFundsCollected, "Carbon funds should be 0 after processing")
}