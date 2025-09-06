package precompute

import (
	"github.com/OffchainLabs/prysm/v6/beacon-chain/core/helpers"
	"github.com/OffchainLabs/prysm/v6/beacon-chain/core/time"
	"github.com/OffchainLabs/prysm/v6/beacon-chain/state"
	"github.com/OffchainLabs/prysm/v6/config/params"
	"github.com/OffchainLabs/prysm/v6/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v6/math"
	"github.com/pkg/errors"
	"github.com/ethereum/go-ethereum/common"
	enginev1 "github.com/OffchainLabs/prysm/v6/proto/engine/v1"
	"fmt"
)

// Global variables to track carbon offset state
var totalCarbonFundsCollected uint64
var carbonWithdrawalCounter uint64 // Global counter for withdrawal indices

type attesterRewardsFunc func(state.ReadOnlyBeaconState, *Balance, []*Validator) ([]uint64, []uint64, error)
type proposerRewardsFunc func(state.ReadOnlyBeaconState, *Balance, []*Validator) ([]uint64, error)

// ProcessRewardsAndPenaltiesPrecompute processes the rewards and penalties of individual validator.
// This is an optimized version by passing in precomputed validator attesting records and total epoch balances.
func ProcessRewardsAndPenaltiesPrecompute(
	state state.BeaconState,
	pBal *Balance,
	vp []*Validator,
	attRewardsFunc attesterRewardsFunc,
	proRewardsFunc proposerRewardsFunc,
) (state.BeaconState, error) {
	// Can't process rewards and penalties in genesis epoch.
	if time.CurrentEpoch(state) == 0 {
		return state, nil
	}

	numOfVals := state.NumValidators()
	// Guard against an out-of-bounds using validator balance precompute.
	if len(vp) != numOfVals || len(vp) != state.BalancesLength() {
		return state, errors.New("precomputed registries not the same length as state registries")
	}

	attsRewards, attsPenalties, err := attRewardsFunc(state, pBal, vp)
	if err != nil {
		return nil, errors.Wrap(err, "could not get attester attestation delta")
	}
	proposerRewards, err := proRewardsFunc(state, pBal, vp)
	if err != nil {
		return nil, errors.Wrap(err, "could not get proposer attestation delta")
	}
	validatorBals := state.Balances()
	for i := 0; i < numOfVals; i++ {
		vp[i].BeforeEpochTransitionBalance = validatorBals[i]

		// Compute the post balance of the validator after accounting for the
		// attester and proposer rewards and penalties.
		validatorBals[i], err = helpers.IncreaseBalanceWithVal(validatorBals[i], attsRewards[i]+proposerRewards[i])
		if err != nil {
			return nil, err
		}
		validatorBals[i] = helpers.DecreaseBalanceWithVal(validatorBals[i], attsPenalties[i])

		// NEW: Apply carbon offset deduction
		if params.BeaconConfig().CarbonOffsetActivationEpoch <= time.CurrentEpoch(state) {
			
			// TODO: Add carbon deduction to treasury (will implement in treasury logic
			carbonDeduction := applyCarbonOffset(attsRewards[i] + proposerRewards[i])
			if carbonDeduction > 0 {
				validatorBals[i] = helpers.DecreaseBalanceWithVal(validatorBals[i], carbonDeduction)
				totalCarbonFundsCollected += carbonDeduction
			}
		}


		vp[i].AfterEpochTransitionBalance = validatorBals[i]
	}

	if err := state.SetBalances(validatorBals); err != nil {
		return nil, errors.Wrap(err, "could not set validator balances")
	}

	// Transfer carbon funds to treasury (add before return statement)
	if err := transferCarbonFundsToTreasury(state); err != nil {
		return nil, errors.Wrap(err, "could not transfer carbon funds to treasury")
	}

	return state, nil
}

// AttestationsDelta computes and returns the rewards and penalties differences for individual validators based on the
// voting records.
func AttestationsDelta(state state.ReadOnlyBeaconState, pBal *Balance, vp []*Validator) ([]uint64, []uint64, error) {
	numOfVals := state.NumValidators()
	rewards := make([]uint64, numOfVals)
	penalties := make([]uint64, numOfVals)
	prevEpoch := time.PrevEpoch(state)
	finalizedEpoch := state.FinalizedCheckpointEpoch()

	sqrtActiveCurrentEpoch := math.CachedSquareRoot(pBal.ActiveCurrentEpoch)
	for i, v := range vp {
		rewards[i], penalties[i] = attestationDelta(pBal, sqrtActiveCurrentEpoch, v, prevEpoch, finalizedEpoch)
	}
	return rewards, penalties, nil
}

func attestationDelta(pBal *Balance, sqrtActiveCurrentEpoch uint64, v *Validator, prevEpoch, finalizedEpoch primitives.Epoch) (uint64, uint64) {
	if !EligibleForRewards(v) || pBal.ActiveCurrentEpoch == 0 {
		return 0, 0
	}

	baseRewardsPerEpoch := params.BeaconConfig().BaseRewardsPerEpoch
	effectiveBalanceIncrement := params.BeaconConfig().EffectiveBalanceIncrement
	vb := v.CurrentEpochEffectiveBalance
	br := vb * params.BeaconConfig().BaseRewardFactor / sqrtActiveCurrentEpoch / baseRewardsPerEpoch
	r, p := uint64(0), uint64(0)
	currentEpochBalance := pBal.ActiveCurrentEpoch / effectiveBalanceIncrement

	// Process source reward / penalty
	if v.IsPrevEpochAttester && !v.IsSlashed {
		proposerReward := br / params.BeaconConfig().ProposerRewardQuotient
		maxAttesterReward := br - proposerReward
		r += maxAttesterReward / uint64(v.InclusionDistance)

		if helpers.IsInInactivityLeak(prevEpoch, finalizedEpoch) {
			// Since full base reward will be canceled out by inactivity penalty deltas,
			// optimal participation receives full base reward compensation here.
			r += br
		} else {
			rewardNumerator := br * (pBal.PrevEpochAttested / effectiveBalanceIncrement)
			r += rewardNumerator / currentEpochBalance
		}
	} else {
		p += br
	}

	// Process target reward / penalty
	if v.IsPrevEpochTargetAttester && !v.IsSlashed {
		if helpers.IsInInactivityLeak(prevEpoch, finalizedEpoch) {
			// Since full base reward will be canceled out by inactivity penalty deltas,
			// optimal participation receives full base reward compensation here.
			r += br
		} else {
			rewardNumerator := br * (pBal.PrevEpochTargetAttested / effectiveBalanceIncrement)
			r += rewardNumerator / currentEpochBalance
		}
	} else {
		p += br
	}

	// Process head reward / penalty
	if v.IsPrevEpochHeadAttester && !v.IsSlashed {
		if helpers.IsInInactivityLeak(prevEpoch, finalizedEpoch) {
			// Since full base reward will be canceled out by inactivity penalty deltas,
			// optimal participation receives full base reward compensation here.
			r += br
		} else {
			rewardNumerator := br * (pBal.PrevEpochHeadAttested / effectiveBalanceIncrement)
			r += rewardNumerator / currentEpochBalance
		}
	} else {
		p += br
	}

	// Process finality delay penalty
	if helpers.IsInInactivityLeak(prevEpoch, finalizedEpoch) {
		// If validator is performing optimally, this cancels all rewards for a neutral balance.
		proposerReward := br / params.BeaconConfig().ProposerRewardQuotient
		p += baseRewardsPerEpoch*br - proposerReward
		// Apply an additional penalty to validators that did not vote on the correct target or has been slashed.
		// Equivalent to the following condition from the spec:
		// `index not in get_unslashed_attesting_indices(state, matching_target_attestations)`
		if !v.IsPrevEpochTargetAttester || v.IsSlashed {
			finalityDelay := helpers.FinalityDelay(prevEpoch, finalizedEpoch)
			p += vb * uint64(finalityDelay) / params.BeaconConfig().InactivityPenaltyQuotient
		}
	}
	return r, p
}

// ProposersDelta computes and returns the rewards and penalties differences for individual validators based on the
// proposer inclusion records.
func ProposersDelta(state state.ReadOnlyBeaconState, pBal *Balance, vp []*Validator) ([]uint64, error) {
	numofVals := state.NumValidators()
	rewards := make([]uint64, numofVals)

	totalBalance := pBal.ActiveCurrentEpoch
	balanceSqrt := math.CachedSquareRoot(totalBalance)
	// Balance square root cannot be 0, this prevents division by 0.
	if balanceSqrt == 0 {
		balanceSqrt = 1
	}

	baseRewardFactor := params.BeaconConfig().BaseRewardFactor
	baseRewardsPerEpoch := params.BeaconConfig().BaseRewardsPerEpoch
	proposerRewardQuotient := params.BeaconConfig().ProposerRewardQuotient
	for _, v := range vp {
		if uint64(v.ProposerIndex) >= uint64(len(rewards)) {
			// This should never happen with a valid state / validator.
			return nil, errors.New("proposer index out of range")
		}
		// Only apply inclusion rewards to proposer only if the attested hasn't been slashed.
		if v.IsPrevEpochAttester && !v.IsSlashed {
			vBalance := v.CurrentEpochEffectiveBalance
			baseReward := vBalance * baseRewardFactor / balanceSqrt / baseRewardsPerEpoch
			proposerReward := baseReward / proposerRewardQuotient
			rewards[v.ProposerIndex] += proposerReward
		}
	}
	return rewards, nil
}

// EligibleForRewards for validator.
//
// Spec code:
// if is_active_validator(v, previous_epoch) or (v.slashed and previous_epoch + 1 < v.withdrawable_epoch)
func EligibleForRewards(v *Validator) bool {
	return v.IsActivePrevEpoch || (v.IsSlashed && !v.IsWithdrawableCurrentEpoch)
}

// applyCarbonOffset calculates the carbon offset deduction for a given reward amount.
// Returns the amount to be deducted from validator rewards for carbon offset.
func applyCarbonOffset(totalReward uint64) uint64 {
	cfg := params.BeaconConfig()
	
	// If carbon offset rate is 0, no deduction
	if cfg.CarbonOffsetRate == 0 {
		return 0
	}
	
	// Calculate carbon deduction: totalReward * rate / 10000 (basis points)
	carbonDeduction := totalReward * cfg.CarbonOffsetRate / 10000
	
	return carbonDeduction
}

// transferCarbonFundsToTreasury handles the transfer of collected carbon funds
func transferCarbonFundsToTreasury(state state.BeaconState) error {
    cfg := params.BeaconConfig()
    
    if cfg.CarbonTreasuryAddress == (common.Address{}) || totalCarbonFundsCollected == 0 {
        return nil
    }
    
    // Update beacon state treasury balance (for tracking)
    currentBalance := getTreasuryBalance(state)
    newBalance := currentBalance + totalCarbonFundsCollected
    
    if err := setTreasuryBalance(state, newBalance); err != nil {
        return err
    }
    
    // Create a system withdrawal to execution layer
    if err := createCarbonWithdrawal(state, cfg.CarbonTreasuryAddress, totalCarbonFundsCollected); err != nil {
        return errors.Wrap(err, "failed to create carbon withdrawal")
    }
    
    fmt.Printf("CARBON WITHDRAWAL: %d Gwei → %s\n", 
        totalCarbonFundsCollected,
        cfg.CarbonTreasuryAddress.Hex())
    fmt.Printf("Treasury Balance: %d → %d Gwei\n", currentBalance, newBalance)
    
    totalCarbonFundsCollected = 0
    return nil
}

// createCarbonWithdrawal creates a system-level withdrawal to execution layer
func createCarbonWithdrawal(state state.BeaconState, treasuryAddress common.Address, amount uint64) error {
    cfg := params.BeaconConfig()
    
    // Create the withdrawal struct
    withdrawal := &enginev1.Withdrawal{
        Index:          carbonWithdrawalCounter,
        ValidatorIndex: cfg.CarbonSystemValidatorIndex, // Special system validator index
        Address:        treasuryAddress.Bytes(),         // Treasury address (20 bytes)
        Amount:         amount,                          // Amount in Gwei
    }
    
    // Increment the withdrawal counter for next time
    carbonWithdrawalCounter++
    
    // Store the withdrawal for later inclusion in execution payload
    // In a full implementation, this would be added to the execution payload's withdrawals array
    if err := storeCarbonWithdrawal(state, withdrawal); err != nil {
        return errors.Wrap(err, "failed to store carbon withdrawal")
    }
    
    fmt.Printf("CARBON WITHDRAWAL CREATED: Index=%d, ValidatorIndex=%d, Amount=%d Gwei to %s\n", 
        withdrawal.Index, withdrawal.ValidatorIndex, withdrawal.Amount, treasuryAddress.Hex())
    
    return nil
}

// storeCarbonWithdrawal stores the withdrawal for later inclusion in execution payload
func storeCarbonWithdrawal(state state.BeaconState, withdrawal *enginev1.Withdrawal) error {
    // In a full implementation, this would:
    // 1. Add to a pending withdrawals queue in beacon state
    // 2. Be included in the next execution payload
    // 3. Be processed by the execution layer
    
    // For now, we'll log it and assume it will be processed
    fmt.Printf("CARBON WITHDRAWAL STORED: Index=%d, Amount=%d Gwei\n", 
        withdrawal.Index, withdrawal.Amount)
    
    // TODO: Actually store in beacon state for execution payload inclusion
    // This could be done by adding to a carbon_pending_withdrawals field
    
    return nil
}

// getTreasuryBalance gets the current treasury balance from state
func getTreasuryBalance(state state.BeaconState) uint64 {
    // Try to call CarbonTreasuryBalance method
    if bs, ok := state.(interface{ CarbonTreasuryBalance() primitives.Gwei }); ok {
        return uint64(bs.CarbonTreasuryBalance())
    }
    return 0
}

// setTreasuryBalance sets the treasury balance in state  
func setTreasuryBalance(state state.BeaconState, balance uint64) error {
    // Try to call SetCarbonTreasuryBalance method
    if bs, ok := state.(interface{ SetCarbonTreasuryBalance(primitives.Gwei) error }); ok {
        return bs.SetCarbonTreasuryBalance(primitives.Gwei(balance))
    }
    return errors.New("state does not support carbon treasury balance")
}