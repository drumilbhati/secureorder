// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.28;

/**
 * @title MockDEXEngine
 * @dev Mock DEX implementation using the Constant Product AMM formula (x * y = k)
 *      Similar to Uniswap v2, supporting swaps and liquidity management.
 *      This is a testing/simulation engine with simulated token balances using mappings.
 *
 * Architecture:
 * - TokenA and TokenB are tracked via internal balance mappings
 * - Liquidity providers receive LP shares (tracked separately)
 * - Swaps follow the constant product formula with 0.3% fee
 * - All operations are atomic - either fully execute or revert
 * - FIFO order sequencing is assumed to be enforced off-chain
 */

contract MockDEXEngine {
    // ============================================================================
    // STATE VARIABLES
    // ============================================================================

    /// @notice Balance tracking for TokenA (simulated ERC20)
    /// Maps user address -> balance
    mapping(address => uint256) public balanceTokenA;

    /// @notice Balance tracking for TokenB (simulated ERC20)
    /// Maps user address -> balance
    mapping(address => uint256) public balanceTokenB;

    /// @notice Liquidity Pool shares (LP tokens)
    /// Maps LP provider address -> share amount
    mapping(address => uint256) public liquidityShares;

    /// @notice Total liquidity shares in circulation
    uint256 public totalLiquidityShares;

    /// @notice Current reserve of TokenA in the pool
    uint256 public reserveTokenA;

    /// @notice Current reserve of TokenB in the pool
    uint256 public reserveTokenB;

    /// @notice Accumulated fees in TokenA balance
    /// These are reinvested in the pool to benefit all LPs
    uint256 public accumulatedFeeTokenA;

    /// @notice Accumulated fees in TokenB balance
    /// These are reinvested in the pool to benefit all LPs
    uint256 public accumulatedFeeTokenB;

    /// @notice Fee percentage: 0.3% = 3 per 1000
    /// Format: fee = amount * FEE_NUMERATOR / FEE_DENOMINATOR
    uint256 public constant FEE_NUMERATOR = 3;
    uint256 public constant FEE_DENOMINATOR = 1000;

    /// @notice Minimum liquidity that must remain locked (1000 wei to prevent rounding errors)
    uint256 public constant MINIMUM_LIQUIDITY = 1000;

    /// @notice Constant product k = reserveA * reserveB (updated after each operation)
    /// Used for sanity checks and validation
    uint256 public lastK;

    // ============================================================================
    // EVENTS
    // ============================================================================

    /// @notice Emitted when liquidity is added to the pool
    /// @param provider Address of the LP provider
    /// @param amountA Amount of TokenA added
    /// @param amountB Amount of TokenB added
    /// @param sharesIssued Number of LP shares issued
    event LiquidityAdded(
        address indexed provider,
        uint256 amountA,
        uint256 amountB,
        uint256 sharesIssued
    );

    /// @notice Emitted when liquidity is removed from the pool
    /// @param provider Address of the LP provider
    /// @param amountA Amount of TokenA withdrawn
    /// @param amountB Amount of TokenB withdrawn
    /// @param sharesBurned Number of LP shares burned
    event LiquidityRemoved(
        address indexed provider,
        uint256 amountA,
        uint256 amountB,
        uint256 sharesBurned
    );

    /// @notice Emitted when TokenA is swapped for TokenB
    /// @param trader Address performing the swap
    /// @param amountAIn Amount of TokenA provided
    /// @param amountBOut Amount of TokenB received
    /// @param feeTokenA Fee charged in TokenA
    event SwapTokenAForTokenB(
        address indexed trader,
        uint256 amountAIn,
        uint256 amountBOut,
        uint256 feeTokenA
    );

    /// @notice Emitted when TokenB is swapped for TokenA
    /// @param trader Address performing the swap
    /// @param amountBIn Amount of TokenB provided
    /// @param amountAOut Amount of TokenA received
    /// @param feeTokenB Fee charged in TokenB
    event SwapTokenBForTokenA(
        address indexed trader,
        uint256 amountBIn,
        uint256 amountAOut,
        uint256 feeTokenB
    );

    /// @notice Emitted when constant product check fails (safety check)
    event ConstantProductCheckFailed(
        uint256 previousK,
        uint256 newK,
        string operation
    );

    /// @notice Emitted when LP is initialized (first liquidity provision)
    event PoolInitialized(uint256 initialLiquidity);

    // ============================================================================
    // MODIFIERS
    // ============================================================================

    /// @dev Ensures the pool has been initialized with liquidity
    modifier poolInitialized() {
        require(totalLiquidityShares > 0, "Pool not initialized");
        _;
    }

    /// @dev Ensures pool has sufficient liquidity reserves
    modifier sufficientLiquidity(uint256 requiredReserveA, uint256 requiredReserveB) {
        require(
            reserveTokenA >= requiredReserveA && reserveTokenB >= requiredReserveB,
            "Insufficient pool liquidity"
        );
        _;
    }

    // ============================================================================
    // USER BALANCE MANAGEMENT (SIMULATED ERC20)
    // ============================================================================

    /**
     * @notice Mint tokens for testing purposes
     * @dev In production, this would interact with real ERC20 contracts
     * @param user Address to mint tokens to
     * @param amountA Amount of TokenA to mint
     * @param amountB Amount of TokenB to mint
     */
    function mintTestTokens(
        address user,
        uint256 amountA,
        uint256 amountB
    ) external {
        // Require non-zero amounts to prevent accidental zero transfers
        require(user != address(0), "Invalid address");
        require(amountA > 0 || amountB > 0, "Must mint positive amounts");

        // Increase user balances
        balanceTokenA[user] += amountA;
        balanceTokenB[user] += amountB;
    }

    /**
     * @notice Get user's total balance of TokenA (balance in wallet + LP value)
     * @param user Address to check
     * @return Total TokenA value owned by user
     */
    function getUserTokenABalance(address user) external view returns (uint256) {
        return balanceTokenA[user];
    }

    /**
     * @notice Get user's total balance of TokenB
     * @param user Address to check
     * @return Total TokenB value owned by user
     */
    function getUserTokenBBalance(address user) external view returns (uint256) {
        return balanceTokenB[user];
    }

    // ============================================================================
    // LIQUIDITY MANAGEMENT
    // ============================================================================

    /**
     * @notice Add liquidity to the pool (mints LP shares)
     * @dev Implements proportional liquidity addition ensuring constant product formula
     * @param amountA Amount of TokenA to add (must be approved by user)
     * @param amountB Amount of TokenB to add (must be approved by user)
     * @return sharesToMint Number of LP shares issued to the provider
     *
     * Logic:
     * - If pool is empty: issue sqrt(amountA * amountB) shares and init pool
     * - If pool exists: calculate shares as min(amountA/reserveA, amountB/reserveB) * totalShares
     *   This ensures the product constant x*y=k is maintained
     */
    function addLiquidity(uint256 amountA, uint256 amountB)
        external
        returns (uint256 sharesToMint)
    {
        // Input validation
        require(amountA > 0 && amountB > 0, "Amounts must be positive");

        // Check user has sufficient balances
        require(
            balanceTokenA[msg.sender] >= amountA,
            "Insufficient TokenA balance"
        );
        require(
            balanceTokenB[msg.sender] >= amountB,
            "Insufficient TokenB balance"
        );

        // If pool is empty (first liquidity provider)
        if (totalLiquidityShares == 0) {
            // Calculate initial LP shares using geometric mean: sqrt(amountA * amountB)
            // This gives the first LP a fair share
            sharesToMint = sqrt(amountA * amountB);

            // Validate minimum liquidity to prevent precision loss
            require(sharesToMint >= MINIMUM_LIQUIDITY, "Insufficient liquidity");

            // Initialize pool reserves
            reserveTokenA = amountA;
            reserveTokenB = amountB;
            lastK = reserveTokenA * reserveTokenB;

            emit PoolInitialized(sharesToMint);
        } else {
            // Pool exists - calculate fair share based on proportions
            // Share = min(amountA / reserveA, amountB / reserveB) * totalShares
            // This ensures existing LPs don't get diluted

            uint256 shareFromA = (amountA * totalLiquidityShares) / reserveTokenA;
            uint256 shareFromB = (amountB * totalLiquidityShares) / reserveTokenB;

            // Use the minimum to maintain proportional balance
            // (if one ratio is higher, we'd be adding too much of one token)
            sharesToMint = shareFromA < shareFromB ? shareFromA : shareFromB;

            require(sharesToMint > 0, "Liquidity too small");

            // Update pool reserves
            reserveTokenA += amountA;
            reserveTokenB += amountB;

            // Verify constant product formula still holds (k can only increase due to fees)
            uint256 newK = reserveTokenA * reserveTokenB;
            if (newK < lastK) {
                emit ConstantProductCheckFailed(lastK, newK, "addLiquidity");
            }
            lastK = newK;
        }

        // Transfer tokens from user to pool (deduct from user balance)
        balanceTokenA[msg.sender] -= amountA;
        balanceTokenB[msg.sender] -= amountB;

        // Mint LP shares to provider
        liquidityShares[msg.sender] += sharesToMint;
        totalLiquidityShares += sharesToMint;

        emit LiquidityAdded(msg.sender, amountA, amountB, sharesToMint);

        return sharesToMint;
    }

    /**
     * @notice Remove liquidity from the pool (burns LP shares)
     * @dev Withdraws proportional amounts of TokenA and TokenB based on LP share percentage
     * @param shares Number of LP shares to burn
     * @return amountA Amount of TokenA withdrawn
     * @return amountB Amount of TokenB withdrawn
     *
     * Logic:
     * - Calculate user's share percentage: shares / totalShares
     * - Withdraw proportional amounts: amountA = (shares/totalShares) * reserveA
     * - Maintains constant product formula as both reserves decrease proportionally
     */
    function removeLiquidity(uint256 shares)
        external
        poolInitialized
        returns (uint256 amountA, uint256 amountB)
    {
        require(shares > 0, "Shares must be positive");
        require(
            liquidityShares[msg.sender] >= shares,
            "Insufficient LP shares"
        );

        // Calculate user's share percentage as fraction
        // amountA = (shares / totalShares) * reserveA
        amountA = (shares * reserveTokenA) / totalLiquidityShares;
        amountB = (shares * reserveTokenB) / totalLiquidityShares;

        require(amountA > 0 && amountB > 0, "Withdrawal amounts too small");

        // Ensure pool has sufficient reserves to withdraw
        require(
            reserveTokenA >= amountA && reserveTokenB >= amountB,
            "Pool reserve error"
        );

        // Update pool reserves (remove withdrawn amounts)
        reserveTokenA -= amountA;
        reserveTokenB -= amountB;

        // Verify constant product formula (k should not decrease)
        uint256 newK = reserveTokenA * reserveTokenB;
        if (newK < lastK) {
            emit ConstantProductCheckFailed(lastK, newK, "removeLiquidity");
        }
        lastK = newK;

        // Burn LP shares from provider
        liquidityShares[msg.sender] -= shares;
        totalLiquidityShares -= shares;

        // Transfer withdrawn tokens back to user
        balanceTokenA[msg.sender] += amountA;
        balanceTokenB[msg.sender] += amountB;

        emit LiquidityRemoved(msg.sender, amountA, amountB, shares);

        return (amountA, amountB);
    }

    // ============================================================================
    // SWAP OPERATIONS (CORE AMM LOGIC)
    // ============================================================================

    /**
     * @notice Swap exact amount of TokenA for TokenB
     * @dev Uses constant product formula: (reserveA + amountIn) * (reserveB - amountOut) = k
     *      Implements fee deduction and slippage protection
     * @param amountIn Exact amount of TokenA to swap
     * @param minAmountOut Minimum acceptable amount of TokenB (slippage protection)
     * @return amountOut Actual amount of TokenB received
     *
     * Constant Product Formula Explanation:
     * - k = reserveA * reserveB (product before swap)
     * - After swap with fee:
     *   - User provides: amountIn TokenA
     *   - Fee taken: amountIn * 0.3%
     *   - Amount affecting pool: (amountIn - fee)
     *   - New reserves: (reserveA + amountIn - fee) * (reserveB - amountOut) = k
     *   - Solve for amountOut:
     *     amountOut = reserveB - k / (reserveA + amountIn - fee)
     *
     * Atomic Execution:
     * - If any check fails, entire transaction reverts
     * - Prevents partial execution or inconsistent state
     */
    function swapExactTokenAForTokenB(uint256 amountIn, uint256 minAmountOut)
        external
        poolInitialized
        returns (uint256 amountOut)
    {
        // Input validation
        require(amountIn > 0, "Amount must be positive");
        require(minAmountOut > 0, "Min amount must be positive");
        require(
            balanceTokenA[msg.sender] >= amountIn,
            "Insufficient TokenA balance"
        );

        // Calculate fee on input amount
        // fee = amountIn * 0.3% = amountIn * 3 / 1000
        uint256 fee = (amountIn * FEE_NUMERATOR) / FEE_DENOMINATOR;

        // Amount that actually enters the pool (after fee)
        uint256 amountInAfterFee = amountIn - fee;

        // Apply constant product formula: (x + dx) * (y - dy) = x * y
        // Where: x = reserveTokenA, y = reserveTokenB, dx = amountInAfterFee
        // Solve for dy (amountOut):
        // amountOut = (reserveTokenB * amountInAfterFee) / (reserveTokenA + amountInAfterFee)
        uint256 denominator = reserveTokenA + amountInAfterFee;
        amountOut =
            (reserveTokenB * amountInAfterFee) /
            denominator;

        // Verify output amount meets user's slippage requirement
        require(
            amountOut >= minAmountOut,
            "Slippage too high: output less than minimum"
        );

        // Ensure pool has sufficient TokenB to fulfill swap
        require(amountOut > 0, "Output amount is zero");
        require(reserveTokenB >= amountOut, "Insufficient pool TokenB");

        // === ATOMIC EXECUTION - Update all state or revert ===

        // 1. Deduct input TokenA from user
        balanceTokenA[msg.sender] -= amountIn;

        // 2. Update pool reserves
        reserveTokenA += amountInAfterFee;
        reserveTokenB -= amountOut;

        // 3. Accumulate fee in pool (reinvested for all LPs)
        accumulatedFeeTokenA += fee;

        // 4. Credit output TokenB to user
        balanceTokenB[msg.sender] += amountOut;

        // 5. Verify constant product formula still holds
        // After swap: newK >= oldK (due to fees helping the pool)
        uint256 newK = reserveTokenA * reserveTokenB;
        require(newK >= lastK, "Constant product formula violated");
        lastK = newK;

        emit SwapTokenAForTokenB(msg.sender, amountIn, amountOut, fee);

        return amountOut;
    }

    /**
     * @notice Swap exact amount of TokenB for TokenA
     * @dev Symmetric to swapExactTokenAForTokenB, applies same formula and safety checks
     * @param amountIn Exact amount of TokenB to swap
     * @param minAmountOut Minimum acceptable amount of TokenA (slippage protection)
     * @return amountOut Actual amount of TokenA received
     *
     * Same logic as swapExactTokenAForTokenB but with tokens reversed:
     * - User provides TokenB, receives TokenA
     * - Formula: amountOut = (reserveTokenA * amountInAfterFee) / (reserveTokenB + amountInAfterFee)
     */
    function swapExactTokenBForTokenA(uint256 amountIn, uint256 minAmountOut)
        external
        poolInitialized
        returns (uint256 amountOut)
    {
        // Input validation
        require(amountIn > 0, "Amount must be positive");
        require(minAmountOut > 0, "Min amount must be positive");
        require(
            balanceTokenB[msg.sender] >= amountIn,
            "Insufficient TokenB balance"
        );

        // Calculate fee on input amount (0.3%)
        uint256 fee = (amountIn * FEE_NUMERATOR) / FEE_DENOMINATOR;

        // Amount that enters the pool after fee deduction
        uint256 amountInAfterFee = amountIn - fee;

        // Apply constant product formula: (x - dx) * (y + dy) = x * y
        // Solve for dx (amountOut):
        // amountOut = (reserveTokenA * amountInAfterFee) / (reserveTokenB + amountInAfterFee)
        uint256 denominator = reserveTokenB + amountInAfterFee;
        amountOut =
            (reserveTokenA * amountInAfterFee) /
            denominator;

        // Verify output meets slippage requirement
        require(
            amountOut >= minAmountOut,
            "Slippage too high: output less than minimum"
        );

        require(amountOut > 0, "Output amount is zero");
        require(reserveTokenA >= amountOut, "Insufficient pool TokenA");

        // === ATOMIC EXECUTION - Update all state or revert ===

        // 1. Deduct input TokenB from user
        balanceTokenB[msg.sender] -= amountIn;

        // 2. Update pool reserves
        reserveTokenB += amountInAfterFee;
        reserveTokenA -= amountOut;

        // 3. Accumulate fee in pool
        accumulatedFeeTokenB += fee;

        // 4. Credit output TokenA to user
        balanceTokenA[msg.sender] += amountOut;

        // 5. Verify constant product formula
        uint256 newK = reserveTokenA * reserveTokenB;
        require(newK >= lastK, "Constant product formula violated");
        lastK = newK;

        emit SwapTokenBForTokenA(msg.sender, amountIn, amountOut, fee);

        return amountOut;
    }

    // ============================================================================
    // UTILITY FUNCTIONS
    // ============================================================================

    /**
     * @notice Get current pool state (reserves and k)
     * @return reserveA Current reserve of token A
     * @return reserveB Current reserve of token B
     * @return k Constant product
     * @return totalShares Total LP shares
     */
    function getPoolState()
        external
        view
        returns (
            uint256 reserveA,
            uint256 reserveB,
            uint256 k,
            uint256 totalShares
        )
    {
        return (reserveTokenA, reserveTokenB, lastK, totalLiquidityShares);
    }

    /**
     * @notice Calculate output amount for a given input (preview swap)
     * @dev Does not modify state, safe to call for previews
     * @param amountIn Amount of TokenA to swap
     * @return amountOut Estimated TokenB to receive
     */
    function getSwapOutputAToB(uint256 amountIn)
        external
        view
        returns (uint256 amountOut)
    {
        require(amountIn > 0, "Amount must be positive");

        // Calculate fee
        uint256 fee = (amountIn * FEE_NUMERATOR) / FEE_DENOMINATOR;
        uint256 amountInAfterFee = amountIn - fee;

        // Apply constant product formula
        amountOut =
            (reserveTokenB * amountInAfterFee) /
            (reserveTokenA + amountInAfterFee);

        return amountOut;
    }

    /**
     * @notice Calculate output amount for TokenB to TokenA swap (preview)
     * @dev Does not modify state
     * @param amountIn Amount of TokenB to swap
     * @return amountOut Estimated TokenA to receive
     */
    function getSwapOutputBToA(uint256 amountIn)
        external
        view
        returns (uint256 amountOut)
    {
        require(amountIn > 0, "Amount must be positive");

        // Calculate fee
        uint256 fee = (amountIn * FEE_NUMERATOR) / FEE_DENOMINATOR;
        uint256 amountInAfterFee = amountIn - fee;

        // Apply constant product formula
        amountOut =
            (reserveTokenA * amountInAfterFee) /
            (reserveTokenB + amountInAfterFee);

        return amountOut;
    }

    /**
     * @notice Get LP value (how much TokenA + TokenB a shareholder can withdraw)
     * @param lp Address of LP holder
     * @return valueTokenA Amount of TokenA that can be withdrawn
     * @return valueTokenB Amount of TokenB that can be withdrawn
     */
    function getLPValue(address lp)
        external
        view
        returns (uint256 valueTokenA, uint256 valueTokenB)
    {
        if (totalLiquidityShares == 0) {
            return (0, 0);
        }

        uint256 userShares = liquidityShares[lp];
        valueTokenA = (userShares * reserveTokenA) / totalLiquidityShares;
        valueTokenB = (userShares * reserveTokenB) / totalLiquidityShares;

        return (valueTokenA, valueTokenB);
    }

    // ============================================================================
    // HELPER FUNCTIONS
    // ============================================================================

    /**
     * @notice Calculate integer square root
     * @dev Uses Babylonian method for efficient computation
     * @param x Input number
     * @return Square root of x (rounded down)
     */
    function sqrt(uint256 x) internal pure returns (uint256) {
        if (x == 0) return 0;

        // Babylonian method: start with estimate and refine
        uint256 z = (x + 1) / 2;
        uint256 y = x;

        while (z < y) {
            y = z;
            z = (x / z + z) / 2;
        }

        return y;
    }
}
