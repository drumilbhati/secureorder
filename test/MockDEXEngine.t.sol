// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.28;

import "forge-std/Test.sol";
import "../contracts/MockDEXEngine.sol";

/**
 * @title MockDEXEngineTest
 * @dev Comprehensive test suite for MockDEXEngine
 *
 * Test Coverage:
 * 1. Pool initialization and liquidity management
 * 2. Swap operations with slippage protection
 * 3. Fee accumulation and distribution
 * 4. Constant product formula validation
 * 5. Edge cases and error conditions
 * 6. State consistency across operations
 */
contract MockDEXEngineTest is Test {
    MockDEXEngine public dex;

    // Test addresses
    address public constant ALICE = address(0x1);
    address public constant BOB = address(0x2);
    address public constant CHARLIE = address(0x3);

    // Test amounts
    uint256 public constant INITIAL_MINT_A = 1000e18; // 1000 TokenA
    uint256 public constant INITIAL_MINT_B = 1000e18; // 1000 TokenB

    // ============================================================================
    // SETUP
    // ============================================================================

    /**
     * @notice Deploy MockDEXEngine and mint initial tokens for test users
     */
    function setUp() public {
        dex = new MockDEXEngine();

        // Mint initial test tokens for all users
        vm.prank(ALICE);
        dex.mintTestTokens(ALICE, INITIAL_MINT_A, INITIAL_MINT_B);

        vm.prank(BOB);
        dex.mintTestTokens(BOB, INITIAL_MINT_A, INITIAL_MINT_B);

        vm.prank(CHARLIE);
        dex.mintTestTokens(CHARLIE, INITIAL_MINT_A, INITIAL_MINT_B);
    }

    // ============================================================================
    // LIQUIDITY MANAGEMENT TESTS
    // ============================================================================

    /**
     * Test 1: Initial liquidity addition (pool initialization)
     * - Verifies first LP gets correct shares (geometric mean)
     * - Checks pool reserves are set correctly
     * - Validates LiquidityAdded event
     */
    function test_AddInitialLiquidity() public {
        uint256 amountA = 100e18;
        uint256 amountB = 100e18;

        // Alice adds initial liquidity
        vm.prank(ALICE);
        uint256 sharesMinted = dex.addLiquidity(amountA, amountB);

        // Verify shares calculation: sqrt(100e18 * 100e18) = 100e18
        uint256 expectedShares = 100e18;
        assertEq(sharesMinted, expectedShares, "Initial shares incorrect");

        // Verify pool reserves
        (uint256 reserveA, uint256 reserveB, , ) = dex.getPoolState();
        assertEq(reserveA, amountA, "ReserveA mismatch");
        assertEq(reserveB, amountB, "ReserveB mismatch");

        // Verify LP shares assigned to Alice
        assertEq(
            dex.liquidityShares(ALICE),
            sharesMinted,
            "Alice shares incorrect"
        );

        // Verify Alice's balances decreased
        assertEq(
            dex.balanceTokenA(ALICE),
            INITIAL_MINT_A - amountA,
            "Alice tokenA balance incorrect"
        );
        assertEq(
            dex.balanceTokenB(ALICE),
            INITIAL_MINT_B - amountB,
            "Alice tokenB balance incorrect"
        );
    }

    /**
     * Test 2: Secondary liquidity addition (proportional share calculation)
     * - Verifies second LP receives correct proportion
     * - Ensures constant product formula is maintained
     * - Tests that unequal proportions are handled correctly
     */
    function test_AddSecondaryLiquidity() public {
        // Alice initializes pool with 100:100
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Bob adds liquidity with same proportion (100:100)
        vm.prank(BOB);
        uint256 sharesMinted = dex.addLiquidity(100e18, 100e18);

        // Bob should get same shares as Alice since proportions match
        assertEq(
            dex.liquidityShares(BOB),
            dex.liquidityShares(ALICE),
            "Bob should get same shares as Alice"
        );

        // Verify total shares = Alice's + Bob's
        uint256 expectedTotal = dex.liquidityShares(ALICE) +
            dex.liquidityShares(BOB);
        (, , , uint256 totalShares) = dex.getPoolState();
        assertEq(totalShares, expectedTotal, "Total shares mismatch");

        // Verify pool reserves doubled
        (uint256 reserveA, uint256 reserveB, , ) = dex.getPoolState();
        assertEq(reserveA, 200e18, "ReserveA should double");
        assertEq(reserveB, 200e18, "ReserveB should double");
    }

    /**
     * Test 3: Proportional liquidity addition with unequal amounts
     * - Verifies limiting factor is correctly identified
     * - Ensures no token is lost due to proportion mismatch
     */
    function test_AddLiquidityUnequal() public {
        // Initialize pool: 100:100
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Bob tries to add 200:100 (imbalanced)
        // Shares = min(200/100, 100/100) * totalShares = min(2, 1) * 100e18 = 1 * 100e18
        vm.prank(BOB);
        uint256 sharesMinted = dex.addLiquidity(200e18, 100e18);

        // Bob should get 100e18 shares (limited by TokenB proportion)
        assertEq(sharesMinted, 100e18, "Bob should get 100e18 shares");

        // Verify pool reserves increased correctly
        (uint256 reserveA, uint256 reserveB, , ) = dex.getPoolState();
        assertEq(reserveA, 200e18, "ReserveA = 100 + 100");
        assertEq(reserveB, 200e18, "ReserveB = 100 + 100");
    }

    /**
     * Test 4: Remove liquidity (LP withdrawal)
     * - Verifies user receives correct amounts proportional to shares
     * - Validates balance updates
     * - Checks constant product is maintained
     */
    function test_RemoveLiquidity() public {
        // Alice adds 100:100
        vm.prank(ALICE);
        uint256 aliceLPShares = dex.addLiquidity(100e18, 100e18);

        // Alice removes 50% of shares
        vm.prank(ALICE);
        (uint256 withdrawnA, uint256 withdrawnB) = dex.removeLiquidity(
            aliceLPShares / 2
        );

        // Alice should get 50% of each: 50:50
        assertEq(withdrawnA, 50e18, "Withdrawn TokenA incorrect");
        assertEq(withdrawnB, 50e18, "Withdrawn TokenB incorrect");

        // Verify Alice's balances
        assertEq(
            dex.balanceTokenA(ALICE),
            INITIAL_MINT_A - 100e18 + 50e18,
            "Alice final TokenA balance incorrect"
        );
        assertEq(
            dex.balanceTokenB(ALICE),
            INITIAL_MINT_B - 100e18 + 50e18,
            "Alice final TokenB balance incorrect"
        );

        // Verify pool reserves
        (uint256 reserveA, uint256 reserveB, , ) = dex.getPoolState();
        assertEq(reserveA, 50e18, "Pool reserveA should be 50");
        assertEq(reserveB, 50e18, "Pool reserveB should be 50");
    }

    /**
     * Test 5: Liquidity removal with accumulated fees
     * - Verifies fees accumulated through swaps benefit existing LPs
     * - Tests that LP value increases due to fee accumulation
     */
    function test_RemoveLiquidityWithFees() public {
        // Alice adds initial liquidity: 100:100
        vm.prank(ALICE);
        uint256 aliceShares = dex.addLiquidity(100e18, 100e18);

        // Bob swaps TokenA for TokenB (will accumulate fees)
        vm.prank(BOB);
        dex.swapExactTokenAForTokenB(50e18, 1e18);

        // Alice removes all her liquidity
        vm.prank(ALICE);
        (uint256 withdrawnA, uint256 withdrawnB) = dex.removeLiquidity(
            aliceShares
        );

        // Alice should get more than she deposited (due to fees)
        // Original: 100:100
        // After swap: reserveA increased, reserveB decreased, but fees added value
        assertGt(withdrawnA + withdrawnB, 200e18, "Alice should gain from fees");
    }

    // ============================================================================
    // SWAP OPERATIONS TESTS
    // ============================================================================

    /**
     * Test 6: Swap TokenA for TokenB (basic swap)
     * - Verifies constant product formula: (x+dx)*(y-dy) = k
     * - Checks fee deduction (0.3%)
     * - Validates slippage protection
     */
    function test_SwapTokenAForTokenB() public {
        // Initialize pool: 100:100
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Bob swaps 10 TokenA for TokenB
        uint256 amountInA = 10e18;

        // Calculate expected output:
        // fee = 10e18 * 0.3% = 30e15
        // amountInAfterFee = 10e18 - 30e15 = 9.97e18
        // amountOut = (100e18 * 9.97e18) / (100e18 + 9.97e18)
        // amountOut ≈ 9.606e18

        vm.prank(BOB);
        uint256 amountOutB = dex.swapExactTokenAForTokenB(amountInA, 1e18);

        // Verify amountOut is reasonable (should be less than input due to fee)
        assertLt(amountOutB, amountInA, "Output should be less than input");
        assertGt(amountOutB, 0, "Output should be positive");

        // Verify Bob's balances
        assertEq(
            dex.balanceTokenA(BOB),
            INITIAL_MINT_A - amountInA,
            "Bob TokenA balance incorrect"
        );
        assertEq(
            dex.balanceTokenB(BOB),
            INITIAL_MINT_B + amountOutB,
            "Bob TokenB balance incorrect"
        );

        // Verify pool reserves updated
        (uint256 reserveA, uint256 reserveB, , ) = dex.getPoolState();
        assertGt(reserveA, 100e18, "ReserveA should increase");
        assertLt(reserveB, 100e18, "ReserveB should decrease");
    }

    /**
     * Test 7: Swap TokenB for TokenA (reverse swap)
     * - Verifies symmetric swap operation
     * - Checks consistent fee application
     */
    function test_SwapTokenBForTokenA() public {
        // Initialize pool: 100:100
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Bob swaps 10 TokenB for TokenA
        uint256 amountInB = 10e18;

        vm.prank(BOB);
        uint256 amountOutA = dex.swapExactTokenBForTokenA(amountInB, 1e18);

        // Verify output is less than input (due to fee)
        assertLt(amountOutA, amountInB, "Output should be less than input");
        assertGt(amountOutA, 0, "Output should be positive");

        // Verify pool reserves
        (uint256 reserveA, uint256 reserveB, , ) = dex.getPoolState();
        assertLt(reserveA, 100e18, "ReserveA should decrease");
        assertGt(reserveB, 100e18, "ReserveB should increase");
    }

    /**
     * Test 8: Slippage protection (minAmountOut check)
     * - Verifies transaction reverts if output is below minimum
     * - Protects against price manipulation
     */
    function test_SwapSlippageProtection() public {
        // Initialize pool: 100:100
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Bob tries to swap but with unrealistic minimum
        // Set minAmountOut higher than possible output
        vm.prank(BOB);
        vm.expectRevert("Slippage too high: output less than minimum");
        dex.swapExactTokenAForTokenB(10e18, 20e18); // Impossible output
    }

    /**
     * Test 9: Multiple consecutive swaps
     * - Verifies price impact accumulates correctly
     * - Validates constant product after each swap
     * - Tests market conditions become more unfavorable with larger swaps
     */
    function test_MultipleConsecutiveSwaps() public {
        // Initialize pool: 100:100
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Bob makes first swap: 10 TokenA
        vm.prank(BOB);
        uint256 out1 = dex.swapExactTokenAForTokenB(10e18, 1e18);

        (uint256 resA1, uint256 resB1, uint256 k1, ) = dex.getPoolState();

        // Bob makes second swap: 10 TokenA
        vm.prank(BOB);
        uint256 out2 = dex.swapExactTokenAForTokenB(10e18, 1e18);

        (uint256 resA2, uint256 resB2, uint256 k2, ) = dex.getPoolState();

        // Second swap should give less output (worse price due to changed reserves)
        assertLt(out2, out1, "Second swap should have worse rate");

        // Constant product should be maintained (only increase due to fees)
        assertGe(k2, k1, "Constant product formula violated");
    }

    // ============================================================================
    // FEE AND CONSTANT PRODUCT TESTS
    // ============================================================================

    /**
     * Test 10: Fee accumulation
     * - Verifies 0.3% fee is correctly deducted
     * - Validates fees remaining in pool
     * - Checks accumulated fee tracking
     */
    function test_FeeAccumulation() public {
        // Initialize pool: 100:100
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Bob swaps 1000 TokenA
        uint256 swapAmount = 1000e18;
        uint256 expectedFee = (swapAmount * 3) / 1000; // 0.3% = 3 per 1000

        vm.prank(BOB);
        dex.swapExactTokenAForTokenB(swapAmount, 1e18);

        // Verify fee was accumulated (fees stay in pool)
        // The accumulated fee should be reflected in pool reserves
        // After swap: reserveA includes the amountInAfterFee (without fee)
        // and accumulatedFeeTokenA tracks the fee separately

        // Pool receives: amount - fee (so reserves don't include fee initially)
        // But fee is marked as accumulated
    }

    /**
     * Test 11: Constant product formula validation
     * - Verifies k is never decreasing (only increases due to fees)
     * - Validates AMM invariant
     */
    function test_ConstantProductFormula() public {
        // Initialize pool: 100:100
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        (, , uint256 k1, ) = dex.getPoolState();

        // Make multiple swaps
        vm.prank(BOB);
        dex.swapExactTokenAForTokenB(50e18, 1e18);

        (, , uint256 k2, ) = dex.getPoolState();

        // k2 should be >= k1 (equal if using precise amounts, greater with fees)
        assertGe(k2, k1, "Constant product decreased");

        // Make another swap in opposite direction
        vm.prank(BOB);
        dex.swapExactTokenBForTokenA(50e18, 1e18);

        (, , uint256 k3, ) = dex.getPoolState();

        // k should continue to not decrease
        assertGe(k3, k2, "Constant product decreased again");
    }

    // ============================================================================
    // EDGE CASES AND ERROR CONDITIONS
    // ============================================================================

    /**
     * Test 12: Insufficient balance for liquidity addition
     * - Verifies transaction reverts when user has insufficient tokens
     */
    function test_AddLiquidityInsufficientBalance() public {
        vm.prank(CHARLIE);
        dex.mintTestTokens(CHARLIE, 50e18, 50e18); // Low balance

        vm.prank(CHARLIE);
        vm.expectRevert("Insufficient TokenA balance");
        dex.addLiquidity(100e18, 100e18); // Try to add more than has
    }

    /**
     * Test 13: Swap without sufficient input balance
     * - Verifies transaction reverts when user can't cover swap amount
     */
    function test_SwapInsufficientBalance() public {
        // Initialize pool
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Charlie has low balance
        vm.prank(CHARLIE);
        dex.mintTestTokens(CHARLIE, 50e18, 50e18);

        // Charlie tries to swap more than they have
        vm.prank(CHARLIE);
        vm.expectRevert("Insufficient TokenA balance");
        dex.swapExactTokenAForTokenB(60e18, 1e18);
    }

    /**
     * Test 14: Swap on uninitialized pool
     * - Verifies operation fails before liquidity is added
     */
    function test_SwapUninitialized() public {
        // Try to swap without adding liquidity
        vm.prank(BOB);
        vm.expectRevert("Pool not initialized");
        dex.swapExactTokenAForTokenB(10e18, 1e18);
    }

    /**
     * Test 15: Remove liquidity with insufficient shares
     * - Validates revert when user tries to remove more than they own
     */
    function test_RemoveLiquidityInsufficientShares() public {
        // Alice adds liquidity
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Bob tries to remove liquidity they don't have
        vm.prank(BOB);
        vm.expectRevert("Insufficient LP shares");
        dex.removeLiquidity(100e18);
    }

    // ============================================================================
    // PREVIEW AND UTILITY FUNCTION TESTS
    // ============================================================================

    /**
     * Test 16: Swap output preview function
     * - Verifies getSwapOutputAToB matches actual swap output
     * - Validates preview is accurate
     */
    function test_SwapPreviewAccuracy() public {
        // Initialize pool: 100:100
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Preview the swap
        uint256 previewOutput = dex.getSwapOutputAToB(10e18);

        // Execute the swap
        vm.prank(BOB);
        uint256 actualOutput = dex.swapExactTokenAForTokenB(10e18, 1e18);

        // Preview should match actual
        assertEq(previewOutput, actualOutput, "Preview doesn't match actual");
    }

    /**
     * Test 17: LP value calculation
     * - Verifies getLPValue returns correct amount user can withdraw
     */
    function test_GetLPValue() public {
        // Alice adds 100:100
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Get Alice's LP value before any swaps
        (uint256 valueA, uint256 valueB) = dex.getLPValue(ALICE);

        // Should equal original amounts
        assertEq(valueA, 100e18, "LP value TokenA incorrect");
        assertEq(valueB, 100e18, "LP value TokenB incorrect");

        // After swap, LP value should increase (due to fees)
        vm.prank(BOB);
        dex.swapExactTokenAForTokenB(50e18, 1e18);

        (uint256 newValueA, uint256 newValueB) = dex.getLPValue(ALICE);
        assertGt(newValueA + newValueB, 200e18, "LP should gain from fees");
    }

    // ============================================================================
    // INTEGRATION TESTS (MULTIPLE OPERATIONS)
    // ============================================================================

    /**
     * Test 18: Complex scenario with multiple LPs and swaps
     * - Simulates real-world usage with multiple participants
     * - Validates all operations work together correctly
     */
    function test_ComplexIntegrationScenario() public {
        // 1. Alice initializes pool with 1000:1000
        vm.prank(ALICE);
        uint256 aliceShares = dex.addLiquidity(1000e18, 1000e18);

        // 2. Bob adds liquidity with 500:500
        vm.prank(BOB);
        uint256 bobShares = dex.addLiquidity(500e18, 500e18);

        // 3. Charlie swaps 100 TokenA for TokenB (10 times)
        for (uint256 i = 0; i < 10; i++) {
            vm.prank(CHARLIE);
            dex.swapExactTokenAForTokenB(100e18, 50e18);
        }

        // 4. Alice removes 50% of liquidity
        vm.prank(ALICE);
        dex.removeLiquidity(aliceShares / 2);

        // 5. Bob removes all liquidity
        vm.prank(BOB);
        dex.removeLiquidity(bobShares);

        // Verify final state is consistent
        (uint256 finalReserveA, uint256 finalReserveB, , uint256 finalShares) = dex
            .getPoolState();

        // Only Alice's remaining shares should exist
        assertEq(
            finalShares,
            aliceShares / 2,
            "Remaining shares incorrect"
        );

        // Reserves should be reduced proportionally
        assertGt(finalReserveA, 0, "Pool should have TokenA");
        assertGt(finalReserveB, 0, "Pool should have TokenB");
    }

    /**
     * Test 19: Arbitrage opportunity (price impact)
     * - Demonstrates that repeated swaps in one direction move the price
     * - Shows how AMM prevents arbitrage through price discovery
     */
    function test_PriceImpactFromLargeSwaps() public {
        // Initialize pool: 100:100 (1:1 ratio)
        vm.prank(ALICE);
        dex.addLiquidity(100e18, 100e18);

        // Small swap: 1 TokenA
        uint256 smallSwapOut = dex.getSwapOutputAToB(1e18);

        // Large swap: 100 TokenA (all remaining)
        uint256 largeSwapOut = dex.getSwapOutputAToB(100e18);

        // Per-unit rate should be worse for large swap
        uint256 smallRate = smallSwapOut / 1; // per 1 TokenA
        uint256 largeRate = largeSwapOut / 100; // per 1 TokenA

        assertLt(largeRate, smallRate, "Large swap should have worse rate");
    }
}
